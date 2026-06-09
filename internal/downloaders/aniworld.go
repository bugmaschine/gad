package downloaders

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"bugmaschine/gad/internal/extractors"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/chromedp"
)

var urlRegex = regexp.MustCompile(`(?i)^https?://(?:www\.)?(?:(aniworld)\.to/anime|(s)\.to/serie)/stream/([^/\s]+)(?:/(?:(?:staffel-([1-9][0-9]*)(?:/(?:episode-([1-9][0-9]*)/?)?)?)|(?:(filme)(?:/(?:film-([1-9][0-9]*)/?)?)?))?)?$`)

type AniWorldSerienStream struct {
	ParsedUrl *ParsedUrl
}

func NewAniWorldSerienStream(urlStr string) (*AniWorldSerienStream, error) {
	parsed, err := ParseUrl(urlStr)
	if err != nil {
		return nil, err
	}
	return &AniWorldSerienStream{ParsedUrl: parsed}, nil
}

func (a *AniWorldSerienStream) GetSeriesInfo(ctx context.Context) (*SeriesInfo, error) {
	url := a.ParsedUrl.GetSeriesUrl()
	slog.Debug("Navigating to series page", "url", url)

	// Navigate with long timeout for ddos-guard
	navCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	navErr := chromedp.Run(navCtx,
		chromedp.Navigate(url),
		chromedp.WaitVisible(`body`, chromedp.ByQuery),
	)
	if navErr != nil {
		slog.Warn("Initial navigation failed or timed out", "error", navErr)
	}

	var pageInfo struct {
		Title       string `json:"title"`
		Description string `json:"description"`
	}

	// Extract optional selectors without waiting. Error pages do not contain the
	// normal title nodes, and waiting for them makes invalid links look hung.
	slog.Debug("Extracting series info...")
	extractErr := chromedp.Run(navCtx,
		chromedp.Evaluate(`(() => {
			const text = selector => document.querySelector(selector)?.textContent?.trim() || "";
			const attr = (selector, name) => document.querySelector(selector)?.getAttribute(name)?.trim() || "";

			return {
				title: text('.breadCrumbMenu li.currentActiveLink span[itemprop="name"]') || text('.series-title h1 span'),
				description: attr('meta[name="description"]', 'content') || attr('p.seri_des', 'data-full-description')
			};
		})()`, &pageInfo),
	)
	if extractErr != nil {
		return nil, fmt.Errorf("failed to extract series info: %w", extractErr)
	}

	title := strings.TrimSpace(pageInfo.Title)
	if title == "" {
		if navErr != nil {
			return nil, fmt.Errorf("failed to load series page: %w", navErr)
		}
		return nil, fmt.Errorf("series page did not contain a title (did you check if the url is correct?): %s", url)
	}

	return &SeriesInfo{
		Title:       title,
		Description: strings.TrimSpace(pageInfo.Description),
	}, nil
}

func (a *AniWorldSerienStream) Download(ctx context.Context, request DownloadRequest, settings DownloadSettings, sender chan<- *DownloadTaskWrapper) error {
	scraper := &Scraper{
		ParsedUrl: a.ParsedUrl,
		Request:   request,
		Settings:  settings,
		Sender:    sender,
	}
	return scraper.Scrape(ctx)
}

type Site int

const (
	SiteAniWorld Site = iota
	SiteSerienStream
)

func (s Site) BaseURL() string {
	if s == SiteAniWorld {
		return "https://aniworld.to/anime/stream"
	}
	return "https://s.to/serie/stream"
}

type ParsedUrl struct {
	Site   Site
	Name   string
	Season *ParsedUrlSeason
}

type ParsedUrlSeason struct {
	Season     uint32
	Episode    uint32
	HasEpisode bool
}

func ParseUrl(u string) (*ParsedUrl, error) {
	matches := urlRegex.FindStringSubmatch(u)
	if matches == nil {
		return nil, fmt.Errorf("invalid url")
	}

	siteName := matches[1]
	if siteName == "" {
		siteName = matches[2]
	}

	var site Site
	if strings.ToLower(siteName) == "aniworld" {
		site = SiteAniWorld
	} else {
		site = SiteSerienStream
	}

	name := matches[3]
	var season *ParsedUrlSeason

	// Staffel handling
	if matches[4] != "" {
		s, _ := strconv.ParseUint(matches[4], 10, 32)
		ps := &ParsedUrlSeason{Season: uint32(s)}
		if matches[5] != "" {
			e, _ := strconv.ParseUint(matches[5], 10, 32)
			ps.Episode = uint32(e)
			ps.HasEpisode = true
		}
		season = ps
	} else if matches[6] != "" { // Filme handling
		ps := &ParsedUrlSeason{Season: 0}
		if matches[7] != "" {
			e, _ := strconv.ParseUint(matches[7], 10, 32)
			ps.Episode = uint32(e)
			ps.HasEpisode = true
		}
		season = ps
	}

	return &ParsedUrl{
		Site:   site,
		Name:   name,
		Season: season,
	}, nil
}

func (p *ParsedUrl) GetSeriesUrl() string {
	return fmt.Sprintf("%s/%s", p.Site.BaseURL(), p.Name)
}

func (p *ParsedUrl) GetSeasonUrl(season uint32) string {
	if season == 0 {
		return fmt.Sprintf("%s/filme", p.GetSeriesUrl())
	}
	return fmt.Sprintf("%s/staffel-%d", p.GetSeriesUrl(), season)
}

func (p *ParsedUrl) GetEpisodeUrl(season, episode uint32) string {
	if season == 0 {
		return fmt.Sprintf("%s/film-%d", p.GetSeasonUrl(season), episode)
	}
	return fmt.Sprintf("%s/episode-%d", p.GetSeasonUrl(season), episode)
}

type Scraper struct {
	ParsedUrl *ParsedUrl
	Request   DownloadRequest
	Settings  DownloadSettings
	Sender    chan<- *DownloadTaskWrapper
}

func (s *Scraper) Scrape(ctx context.Context) error {
	switch s.Request.Episodes.Kind {
	case EpisodesRequestUnspecified:
		if s.ParsedUrl.Season != nil {
			if s.ParsedUrl.Season.HasEpisode {
				return s.scrapeEpisode(ctx, s.ParsedUrl.Season.Season, s.ParsedUrl.Season.Episode, s.ParsedUrl.Season.Episode) // Max is itself for single episode
			}
			return s.scrapeSeason(ctx, s.ParsedUrl.Season.Season, AllOrSpecific{All: true})
		}
		return s.scrapeSeasons(ctx, AllOrSpecific{All: true})
	case EpisodesRequestEpisodes:
		season := uint32(1)
		if s.ParsedUrl.Season != nil {
			season = s.ParsedUrl.Season.Season
		}
		return s.scrapeSeason(ctx, season, s.Request.Episodes.Payload)
	case EpisodesRequestSeasons:
		return s.scrapeSeasons(ctx, s.Request.Episodes.Payload)
	}
	return nil
}

func (s *Scraper) scrapeSeasons(ctx context.Context, payload AllOrSpecific) error {
	var nodes []*cdp.Node
	err := chromedp.Run(ctx,
		chromedp.Navigate(s.ParsedUrl.GetEpisodeUrl(1, 1)),
		chromedp.WaitVisible(`.hosterSiteDirectNav`, chromedp.ByQuery),
		chromedp.Nodes(`#stream > ul:first-of-type > li`, &nodes),
	)
	if err != nil {
		return err
	}

	var seasons []uint32
	if len(nodes) == 0 {
		return fmt.Errorf("no seasons found")
	}

	var seasonTexts []string
	err = chromedp.Run(ctx,
		chromedp.Evaluate(`Array.from(document.querySelectorAll("#stream > ul:first-of-type > li")).map(li => li.innerText.trim())`, &seasonTexts),
	)
	if err != nil {
		return err
	}

	for _, t := range seasonTexts {
		if strings.EqualFold(t, "Filme") {
			seasons = append(seasons, 0)
			continue
		}
		num, err := strconv.ParseUint(t, 10, 32)
		if err == nil {
			seasons = append(seasons, uint32(num))
		}
	}
	slog.Debug("Found seasons", "raw", seasonTexts, "parsed", seasons)
	sort.Slice(seasons, func(i, j int) bool { return seasons[i] < seasons[j] })

	for _, season := range seasons {
		if err := ctx.Err(); err != nil {
			return err
		}

		if s.shouldDownloadSeason(season, payload) {
			slog.Debug("Queueing season for scraping", "season", season)

			if err := s.scrapeSeason(ctx, season, AllOrSpecific{All: true}); err != nil {
				if ctx.Err() != nil {
					return err
				}
				slog.Error("Failed to scrape season", "season", season, "error", err)
			}
		} else {
			slog.Debug("Skipping season due to filter", "season", season)
		}
	}
	return nil
}

func (s *Scraper) shouldDownloadSeason(season uint32, payload AllOrSpecific) bool {
	if payload.All {
		return true
	}
	for _, r := range payload.Specific {
		if season >= r.Begin && season <= r.End {
			return true
		}
	}
	return false
}

func (s *Scraper) scrapeSeason(ctx context.Context, season uint32, payload AllOrSpecific) error {
	err := chromedp.Run(ctx,
		chromedp.Navigate(s.ParsedUrl.GetSeasonUrl(season)),
		chromedp.WaitVisible(`.hosterSiteDirectNav`, chromedp.ByQuery),
	)
	if err != nil {
		return err
	}

	var epEntries []struct {
		Number string `json:"number"`
		Flags  []struct {
			Title string `json:"title"`
			Src   string `json:"src"`
		} `json:"flags"`
	}

	err = chromedp.Run(ctx,
		chromedp.Evaluate(`
			Array.from(document.querySelectorAll('table.seasonEpisodesList tbody tr')).map(tr => {
				const epNumMeta = tr.querySelector('meta[itemprop="episodeNumber"]');
				const epNum = epNumMeta ? epNumMeta.content : tr.querySelector('td:first-child')?.innerText.trim().match(/\d+/)?.[0];
				const flags = Array.from(tr.querySelectorAll('td.editFunctions img.flag')).map(img => ({
					title: img.title || img.alt || "",
					src: img.getAttribute("src") || ""
				}));
				return {number: epNum, flags: flags};
			})
		`, &epEntries),
	)
	if err != nil || len(epEntries) == 0 {
		slog.Debug("Failed to extract episode entries from table, falling back to basic list", "error", err)
		// Fallback to basic list if table is missing
		var episodeTexts []string
		err = chromedp.Run(ctx,
			chromedp.Evaluate(`Array.from(document.querySelectorAll("li > a[data-episode-id]")).map(a => a.innerText.trim())`, &episodeTexts),
		)
		if err != nil {
			return err
		}
		for _, t := range episodeTexts {
			if num, err := strconv.ParseUint(t, 10, 32); err == nil {
				epEntries = append(epEntries, struct {
					Number string `json:"number"`
					Flags  []struct {
						Title string `json:"title"`
						Src   string `json:"src"`
					} `json:"flags"`
				}{Number: strconv.FormatUint(num, 10), Flags: nil})
			}
		}
	}

	var episodes []uint32
	epMap := make(map[uint32][]struct {
		Title string `json:"title"`
		Src   string `json:"src"`
	})
	for _, entry := range epEntries {
		num, err := strconv.ParseUint(entry.Number, 10, 32)
		if err == nil {
			ep := uint32(num)
			episodes = append(episodes, ep)
			epMap[ep] = entry.Flags
		}
	}
	sort.Slice(episodes, func(i, j int) bool { return episodes[i] < episodes[j] })

	// Find max episode for padding
	var maxEpisodes uint32
	if len(episodes) > 0 {
		maxEpisodes = episodes[len(episodes)-1]
	}
	slog.Debug("Found episodes", "raw", epEntries, "parsed", episodes, "maxEpisodes", maxEpisodes)
	for _, episode := range episodes {
		if err := ctx.Err(); err != nil {
			return err
		}

		slog.Debug("Processing episode", "season", season, "episode", episode)
		if s.Settings.CheckIfExists != nil && s.Settings.CheckIfExists(season, episode, maxEpisodes, nil) {
			slog.Info("Skipping episode because it already exists", "season", season, "episode", episode)
			continue
		}

		if s.shouldDownloadEpisode(episode, payload) {
			// Pre-filter by language if flags are available
			flags := epMap[episode]
			if flags != nil && (s.Request.Language.Type != VideoTypeUnspecified || s.Request.Language.Language != LanguageUnspecified) {
				match := false
				for _, flag := range flags {
					vt := parseFlagToVideoType(flag.Title, flag.Src)
					slog.Debug("Checking flag", "episode", episode, "flagTitle", flag.Title, "flagSrc", flag.Src, "videoType", vt)
					if (s.Request.Language.Type == VideoTypeUnspecified || s.Request.Language.Type == vt.Type) &&
						(s.Request.Language.Language == LanguageUnspecified || s.Request.Language.Language == vt.Language) {
						match = true
						break
					}
				}
				if !match {
					slog.Info("Skipping episode because requested language/type is not available", "season", season, "episode", episode, "requested", s.Request.Language)
					continue
				}
			}

			slog.Debug("Queueing episode for scraping", "season", season, "episode", episode)
			if err := s.scrapeEpisode(ctx, season, episode, maxEpisodes); err != nil {
				if ctx.Err() != nil {
					return err
				}
				slog.Error("Failed to scrape episode", "season", season, "episode", episode, "error", err)
			}
		} else {
			slog.Debug("Skipping episode due to filter", "season", season, "episode", episode)
		}
	}
	return nil
}

func parseFlagToVideoType(title, src string) VideoType {
	vt := VideoType{Type: VideoTypeDub, Language: LanguageGerman}
	// On AniWorld, "japanese" in the SVG filename usually means it's a sub (original audio + subtitles)
	if strings.Contains(title, "Untertitel") || strings.Contains(title, "Subtitles") || strings.Contains(src, "japanese") {
		vt.Type = VideoTypeSub
	}

	if strings.Contains(title, "English") || strings.Contains(title, "Englisch") || strings.Contains(src, "english") {
		vt.Language = LanguageEnglish
	} else if strings.Contains(title, "Deutsch") || strings.Contains(title, "German") || strings.Contains(src, "german") {
		vt.Language = LanguageGerman
	}
	return vt
}

func (s *Scraper) shouldDownloadEpisode(episode uint32, payload AllOrSpecific) bool {
	if payload.All {
		return true
	}
	for _, r := range payload.Specific {
		if episode >= r.Begin && episode <= r.End {
			return true
		}
	}
	return false
}

func (s *Scraper) scrapeEpisode(ctx context.Context, season, episode, maxEpisodes uint32) error {
	url := s.ParsedUrl.GetEpisodeUrl(season, episode)
	slog.Debug("Navigating to episode page", "url", url)

	// Long timeout for potential challenges
	eCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	err := chromedp.Run(eCtx,
		chromedp.Navigate(url),
		chromedp.WaitVisible(`.changeLanguageBox`, chromedp.ByQuery),
	)
	if err != nil {
		return fmt.Errorf("failed to load episode page: %w", err)
	}

	var options []struct {
		Key   string `json:"key"`
		Type  string `json:"type"`
		Lang  string `json:"lang"`
		Title string `json:"title"`
		Src   string `json:"src"`
	}

	err = chromedp.Run(ctx,
		chromedp.Evaluate(`
			Array.from(document.querySelectorAll('div.changeLanguageBox img')).map(img => {
				const title = img.title || img.alt || "";
				const src = img.getAttribute("src") || "";
				let type = "dub";
				let lang = "de";
				
				if (title.includes("Untertitel") || title.includes("Subtitles") || src.includes("japanese")) {
					type = "sub";
				}
				
				if (title.includes("English") || title.includes("Englisch") || src.includes("english")) {
					lang = "en";
				} else if (title.includes("Deutsch") || title.includes("German") || src.includes("german")) {
					lang = "de";
				}
				
				return {
					key: img.getAttribute("data-lang-key"),
					type: type,
					lang: lang,
					title: title,
					src: src
				};
			})
		`, &options),
	)
	if err != nil {
		return fmt.Errorf("failed to extract language options: %w", err)
	}

	var selectedKey string
	var selectedType VideoType
	filter := s.Request.Language

	for _, opt := range options {
		vt := VideoType{}
		if opt.Type == "dub" {
			vt.Type = VideoTypeDub
		} else {
			vt.Type = VideoTypeSub
		}

		switch opt.Lang {
		case "de":
			vt.Language = LanguageGerman
		case "en":
			vt.Language = LanguageEnglish
		}

		match := true
		if filter.Type != VideoTypeUnspecified && filter.Type != vt.Type {
			match = false
		}
		if filter.Language != LanguageUnspecified && filter.Language != vt.Language {
			match = false
		}

		if match {
			// Priority: Dub > Sub if no specific type requested
			if selectedKey == "" || (filter.Type == VideoTypeUnspecified && vt.Type == VideoTypeDub && selectedType.Type == VideoTypeSub) {
				selectedKey = opt.Key
				selectedType = vt
			}
		}
	}

	if selectedKey == "" {
		return fmt.Errorf("no language option matches the requested filter (type: %v, lang: %v). options: %+v", filter.Type, filter.Language, options)
	}

	slog.Debug("Selected language option", "key", selectedKey, "type", selectedType.Type, "lang", selectedType.Language)

	if s.Settings.CheckIfExists != nil && s.Settings.CheckIfExists(season, episode, maxEpisodes, &selectedType) {
		slog.Info("Skipping episode because it already exists", "season", season, "episode", episode)
		return nil
	}
	return s.sendStreamToDownloader(ctx, season, episode, maxEpisodes, selectedKey, selectedType)
}

func (s *Scraper) sendStreamToDownloader(ctx context.Context, season, episode, maxEpisodes uint32, langKey string, videoType VideoType) error {
	var streams []struct {
		Name string `json:"name"`
		Href string `json:"href"`
	}

	err := chromedp.Run(ctx,
		chromedp.Evaluate(fmt.Sprintf(`
			Array.from(document.querySelectorAll('.hosterSiteVideo ul li[data-lang-key="%s"]')).map(li => ({
				name: li.querySelector("h4").innerText.trim(),
				href: li.getAttribute("data-link-target")
			}))
		`, langKey), &streams),
	)
	if err != nil {
		return err
	}

	var currentUrl string
	err = chromedp.Run(ctx, chromedp.Location(&currentUrl))
	if err != nil {
		return err
	}
	base, _ := url.Parse(currentUrl)

	for _, stream := range streams {
		rel, err := url.Parse(stream.Href)
		if err != nil {
			continue
		}
		absoluteUrl := base.ResolveReference(rel).String()

		slog.Debug("Found stream hoster", "name", stream.Name, "url", absoluteUrl)
		slog.Debug("Trying hoster", "name", stream.Name, "url", absoluteUrl)

		// Try to extract
		extracted, err := extractors.ExtractVideoUrlWithExtractor(ctx, absoluteUrl, stream.Name, "", currentUrl)
		if err != nil {
			slog.Warn("Extractor failed", "name", stream.Name, "url", absoluteUrl, "error", err)
			continue
		}
		if extracted == nil {
			slog.Warn("Extractor returned nothing", "name", stream.Name, "url", absoluteUrl)
			continue
		}
		if err := validateExtractedStream(ctx, extracted); err != nil {
			slog.Warn("Extractor returned unusable stream", "name", stream.Name, "url", extracted.Url, "error", err)
			continue
		}
		if err == nil && extracted != nil {
			task := &DownloadTaskWrapper{
				Episode:            EpisodeInfo{Season: season, Episode: episode, MaxEpisodes: maxEpisodes},
				Lang:               videoType,
				Url:                extracted.Url,
				Referer:            extracted.Referer,
				UserAgent:          extracted.UserAgent,
				OnDownloadStart:    extracted.OnDownloadStart,
				OnDownloadComplete: extracted.OnDownloadComplete,
			}
			select {
			case s.Sender <- task:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}

	return fmt.Errorf("no valid hoster found")
}

func validateExtractedStream(ctx context.Context, extracted *extractors.ExtractedVideo) error {
	if extracted.OnDownloadStart != nil {
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, extracted.Url, nil)
	if err != nil {
		return err
	}
	if extracted.Referer != "" {
		req.Header.Set("Referer", extracted.Referer)
	}
	if extracted.UserAgent != "" {
		req.Header.Set("User-Agent", extracted.UserAgent)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("bad status: %s", resp.Status)
	}
	return nil
}

func init() {
	Register(func(u string) (Downloader, error) {
		if urlRegex.MatchString(u) {
			return NewAniWorldSerienStream(u)
		}
		return nil, nil
	})
}
