package downloaders

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bugmaschine/gad/internal/extractors"
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
	var title, description string
	url := a.ParsedUrl.GetSeriesUrl()
	slog.Info("Navigating to series page", "url", url)

	// Navigate with long timeout for ddos-guard
	navCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()

	err := chromedp.Run(navCtx,
		chromedp.Navigate(url),
		chromedp.WaitVisible(`body`, chromedp.ByQuery),
	)
	if err != nil {
		slog.Warn("Initial navigation failed or timed out", "error", err)
	}

	// Wait for actual content using visible selectors (case-corrected)
	slog.Info("Extracting series info...")
	_ = chromedp.Run(ctx,
		chromedp.Text(`.breadCrumbMenu li.currentActiveLink span[itemprop="name"]`, &title, chromedp.ByQuery),
		chromedp.AttributeValue(`meta[name="description"]`, "content", &description, nil, chromedp.ByQuery),
	)

	// Fallback to hidden elements if needed
	if title == "" {
		slog.Debug("Visible title not found, trying hidden elements...")
		_ = chromedp.Run(ctx,
			chromedp.Text(`.series-title h1 span`, &title, chromedp.ByQuery),
			chromedp.AttributeValue(`p.seri_des`, "data-full-description", &description, nil, chromedp.ByQuery),
		)
	}

	if title == "" {
		// Final fallback: use the slug
		title = strings.Title(strings.ReplaceAll(a.ParsedUrl.Name, "-", " "))
	}

	return &SeriesInfo{
		Title:       strings.TrimSpace(title),
		Description: strings.TrimSpace(description),
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
		if s.shouldDownloadSeason(season, payload) {
			slog.Debug("Queueing season for scraping", "season", season)
			if err := s.scrapeSeason(ctx, season, AllOrSpecific{All: true}); err != nil {
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

	var episodeTexts []string
	err = chromedp.Run(ctx,
		chromedp.Evaluate(`Array.from(document.querySelectorAll("li > a[data-episode-id]")).map(a => a.innerText.trim())`, &episodeTexts),
	)
	if err != nil {
		return err
	}

	var episodes []uint32
	for _, t := range episodeTexts {
		num, err := strconv.ParseUint(t, 10, 32)
		if err == nil {
			episodes = append(episodes, uint32(num))
		}
	}
	sort.Slice(episodes, func(i, j int) bool { return episodes[i] < episodes[j] })

	// Find max episode for padding
	var maxEpisodes uint32
	for _, ep := range episodes {
		if ep > maxEpisodes {
			maxEpisodes = ep
		}
	}

	for _, episode := range episodes {
		if s.Settings.CheckIfExists != nil && s.Settings.CheckIfExists(season, episode, maxEpisodes, nil) {
			slog.Info("Skipping episode because it already exists", "season", season, "episode", episode)
			continue
		}

		if s.shouldDownloadEpisode(episode, payload) {
			slog.Debug("Queueing episode for scraping", "season", season, "episode", episode)
			if err := s.scrapeEpisode(ctx, season, episode, maxEpisodes); err != nil {
				slog.Error("Failed to scrape episode", "season", season, "episode", episode, "error", err)
			}
		} else {
			slog.Debug("Skipping episode due to filter", "season", season, "episode", episode)
		}
	}
	return nil
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
	slog.Info("Navigating to episode page", "url", url)

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

	var langInfo struct {
		Key  string `json:"key"`
		Type string `json:"type"`
	}

	err = chromedp.Run(ctx,
		chromedp.Evaluate(`
			(() => {
				const imgs = Array.from(document.querySelectorAll('div.changeLanguageBox img'));
				// Priority: Dub (German without "Untertitel"), then Sub (German with "Untertitel")
				const sub = imgs.find(img => (img.title || img.alt || "").includes("Untertitel"));
				const dub = imgs.find(img => (img.title || img.alt || "").includes("Deutsch") && !(img.title || img.alt || "").includes("Untertitel"));
				
				if (dub) return {key: dub.getAttribute("data-lang-key"), type: "dub"};
				if (sub) return {key: sub.getAttribute("data-lang-key"), type: "sub"};
				return null;
			})()
		`, &langInfo),
	)
	if err != nil || langInfo.Key == "" {
		return fmt.Errorf("failed to find language info")
	}
	slog.Debug("Found language info", "key", langInfo.Key, "type", langInfo.Type)

	var videoType VideoType
	if langInfo.Type == "dub" {
		videoType = VideoType{Type: VideoTypeDub, Language: LanguageGerman}
	} else {
		videoType = VideoType{Type: VideoTypeSub, Language: LanguageGerman}
	}

	if s.Settings.CheckIfExists != nil && s.Settings.CheckIfExists(season, episode, maxEpisodes, &videoType) {
		slog.Info("Skipping episode because it already exists", "season", season, "episode", episode)
		return nil
	}
	return s.sendStreamToDownloader(ctx, season, episode, maxEpisodes, langInfo.Key, videoType)
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
		slog.Info("Trying hoster", "name", stream.Name, "url", absoluteUrl)

		// Try to extract
		extracted, err := extractors.ExtractVideoUrlWithExtractor(ctx, absoluteUrl, stream.Name, "", currentUrl)
		if err == nil && extracted != nil {
			s.Sender <- &DownloadTaskWrapper{
				Episode: EpisodeInfo{Season: season, Episode: episode, MaxEpisodes: maxEpisodes},
				Lang:    videoType,
				Url:     extracted.Url,
				Referer: extracted.Referer,
			}
			return nil
		}
	}

	return fmt.Errorf("no valid hoster found")
}

func init() {
	Register(func(u string) (Downloader, error) {
		if urlRegex.MatchString(u) {
			return NewAniWorldSerienStream(u)
		}
		return nil, nil
	})
}
