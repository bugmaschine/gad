package extractors

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"sync"
	"time"
)

type Vidmoly struct{}

func (v *Vidmoly) Names() []string {
	return []string{"Vidmoly"}
}

func (v *Vidmoly) SupportedFrom() SupportedFrom {
	return SupportedFromUrl | SupportedFromSource
}

func (v *Vidmoly) SupportsUrl(urlStr string) bool {
	for _, host := range []string{"vidmoly.to", "vidmoly.biz", "vidmoly.me"} {
		if IsUrlHostAndHasPath(urlStr, host, true, true) {
			return true
		}
	}
	return false
}

// VidmolyPingConfig holds everything needed to send verification pings
// to sw.vidmoly.me/v1/ping while playback is running.
type VidmolyPingConfig struct {
	PingUrl       string // always "https://sw.vidmoly.me/v1/ping"
	WatchUrl      string // always "https://sw.vidmoly.me/v1/watch"
	PingInterval  int    // seconds between pings (parsed from JS, typically 20)
	WatchInterval int    // seconds between watch pings (parsed from JS, typically 25)
	SessionId     string // mSid / moly_sid
	Token         string // mToken
	FileCode      string // mFile / file_code
	FileId        string // mFileID / file_id
	ContentId     string // mCd
	UserId        string // mUsr / usr_id
	EarlyAccess   string // mEar / ear
	Asn           string // mAsn
	Referer       string // base URL of the vidmoly page (e.g. https://vidmoly.biz/)
	Duration      string // video duration in seconds
}

func (v *Vidmoly) ExtractVideoUrl(ctx context.Context, from ExtractFrom) (*ExtractedVideo, error) {
	source, finalURL, err := GetSourceWithFinalURL(ctx, from)
	if err != nil {
		return nil, err
	}

	// --- HLS URL ---
	videoUrlRe := regexp.MustCompile(`(?s)file:\s*['"]([^'"]+\.m3u8[^'"]*)['"]`)
	matches := videoUrlRe.FindStringSubmatch(source)
	if len(matches) < 2 {
		return nil, fmt.Errorf("Vidmoly: failed to retrieve sources")
	}

	// --- Base URL (referer) ---
	baseRe := regexp.MustCompile(`^(https?://[^/]+/)`)
	baseMatches := baseRe.FindStringSubmatch(finalURL)
	if len(baseMatches) < 2 {
		return nil, fmt.Errorf("Vidmoly: failed to extract base url")
	}

	slog.Debug("Vidmoly page resolved", "input_url", from.Url, "final_url", finalURL, "referer", baseMatches[1])
	slog.Debug("Vidmoly video URL found", "url", matches[1])

	// --- Ping config ---
	pingCfg, err := extractPingConfig(source, baseMatches[1])
	if err != nil {
		// non-fatal: log and continue without ping support
		slog.Warn("Vidmoly: could not extract ping config", "err", err)
	}

	return &ExtractedVideo{
		Url:             matches[1],
		Referer:         baseMatches[1],
		OnDownloadStart: newVidmolyPingStartHook(pingCfg),
	}, nil
}

func newVidmolyPingStartHook(cfg *VidmolyPingConfig) func(context.Context) (func(), error) {
	if cfg == nil {
		return nil
	}

	return func(ctx context.Context) (func(), error) {
		pingCtx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		started := time.Now()
		watchSessionId := newVidmolyWatchSessionID()

		if _, err := sendVidmolyPing(pingCtx, cfg, started); err != nil {
			slog.Warn("Vidmoly: initial ping failed", "error", err)
		}
		if err := sendVidmolyWatchPing(pingCtx, cfg, watchSessionId); err != nil {
			slog.Warn("Vidmoly: initial watch ping failed", "error", err)
		}

		go runVidmolyPingLoop(pingCtx, cfg, started, watchSessionId, done)

		var once sync.Once
		return func() {
			once.Do(func() {
				cancel()
				<-done
			})
		}, nil
	}
}

func runVidmolyPingLoop(ctx context.Context, cfg *VidmolyPingConfig, started time.Time, watchSessionId string, done chan<- struct{}) {
	defer close(done)

	pingInterval := time.Duration(cfg.PingInterval) * time.Second
	if pingInterval <= 0 {
		pingInterval = 20 * time.Second
	}
	watchInterval := time.Duration(cfg.WatchInterval) * time.Second
	if watchInterval <= 0 {
		watchInterval = 25 * time.Second
	}

	verified := false

	pingTicker := time.NewTicker(pingInterval)
	defer pingTicker.Stop()
	watchTicker := time.NewTicker(watchInterval)
	defer watchTicker.Stop()
	var heartbeatTicker *time.Ticker
	var heartbeatC <-chan time.Time
	defer func() {
		if heartbeatTicker != nil {
			heartbeatTicker.Stop()
		}
	}()

	sendVerification := func() {
		if verified {
			return
		}
		prog, err := sendVidmolyPing(ctx, cfg, started)
		if err != nil {
			slog.Warn("Vidmoly: ping failed", "error", err)
			return
		}
		slog.Debug("Vidmoly: ping sent", "prog", prog)
		if prog == 100 {
			verified = true
			pingTicker.Stop()
			heartbeatTicker = time.NewTicker(time.Minute)
			heartbeatC = heartbeatTicker.C
		}
	}
	sendHeartbeat := func() {
		if _, err := sendVidmolyPing(ctx, cfg, started); err != nil {
			slog.Warn("Vidmoly: heartbeat failed", "error", err)
			return
		}
		slog.Debug("Vidmoly: heartbeat sent")
	}
	sendWatch := func() {
		if err := sendVidmolyWatchPing(ctx, cfg, watchSessionId); err != nil {
			slog.Warn("Vidmoly: watch ping failed", "error", err)
			return
		}
		slog.Debug("Vidmoly: watch ping sent")
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-pingTicker.C:
			sendVerification()
		case <-watchTicker.C:
			sendWatch()
		case <-heartbeatC:
			sendHeartbeat()
		}
	}
}

func newVidmolyWatchSessionID() string {
	return fmt.Sprintf("sid_%d", time.Now().UnixNano())
}

// vidmolyOrigin derives the Origin header value from the page referer URL.
// e.g. "https://vidmoly.biz/" → "https://vidmoly.biz"
func vidmolyOrigin(referer string) string {
	u, err := url.Parse(referer)
	if err != nil || u.Host == "" {
		return "https://vidmoly.biz"
	}
	return u.Scheme + "://" + u.Host
}

func sendVidmolyPing(ctx context.Context, cfg *VidmolyPingConfig, started time.Time) (int, error) {
	// Construct the payload with the dynamic referer instead of "direct"
	payload := map[string]string{
		"sid":       cfg.SessionId,
		"token":     cfg.Token,
		"file_code": cfg.FileCode,
		"file_id":   cfg.FileId,
		"cd":        cfg.ContentId,
		"asn":       cfg.Asn,
		"usr_id":    cfg.UserId,
		"ear":       cfg.EarlyAccess,
		"ref":       cfg.Referer,
		"dur":       cfg.Duration,
		"ab":        "0",
		"dev":       "Desktop",
		"tw":        fmt.Sprintf("%d", int(time.Since(started).Seconds())),
		"ov":        "0",
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal ping payload: %w", err)
	}

	slog.Debug("Vidmoly: sending ping", "payload", payload)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.PingUrl, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("failed to create ping request: %w", err)
	}

	origin := vidmolyOrigin(cfg.Referer)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", origin)
	req.Header.Set("Pragma", "no-cache")
	req.Header.Set("Referer", cfg.Referer)
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/148.0.0.0 Safari/537.36")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("ping request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return 0, fmt.Errorf("bad ping status: %s", resp.Status)
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("failed to read ping response: %w", err)
	}

	slog.Debug("Vidmoly: ping response", "body", string(bodyBytes))

	var result struct {
		Prog int `json:"prog"`
	}

	// Proper error handling if the response is HTML (e.g., Cloudflare block) instead of JSON
	if err := json.Unmarshal(bodyBytes, &result); err != nil {
		slog.Warn("Vidmoly: unexpected server response (not JSON)", "body", string(bodyBytes))
		return 0, fmt.Errorf("json unmarshal failed: %w", err)
	}

	return result.Prog, nil
}

// sendVidmolyWatchPing sends the secondary watch ping required for playback.
func sendVidmolyWatchPing(ctx context.Context, cfg *VidmolyPingConfig, watchSessionId string) error {
	u, err := url.Parse(cfg.WatchUrl)
	if err != nil {
		return fmt.Errorf("failed to parse watch url: %w", err)
	}

	q := u.Query()
	q.Set("file_code", cfg.FileCode)
	q.Set("sid", watchSessionId)
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return fmt.Errorf("failed to create watch ping request: %w", err)
	}

	origin := vidmolyOrigin(cfg.Referer)
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Origin", origin)
	req.Header.Set("Pragma", "no-cache")
	req.Header.Set("Referer", cfg.Referer)
	req.Header.Set("Sec-Fetch-Dest", "empty")
	req.Header.Set("Sec-Fetch-Mode", "no-cors")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/148.0.0.0 Safari/537.36")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("watch ping request failed: %w", err)
	}
	defer resp.Body.Close()

	// no-cors responses may return non-2xx — treat anything as ok
	return nil
}

// extractPingConfig parses the inline JS for all fields needed to send
// verification pings. Returns nil if any required field is missing.
func extractPingConfig(source, referer string) (*VidmolyPingConfig, error) {
	extract := func(pattern string) string {
		re := regexp.MustCompile(pattern)
		m := re.FindStringSubmatch(source)
		if len(m) < 2 {
			return ""
		}
		return m[1]
	}
	first := func(patterns ...string) string {
		for _, pattern := range patterns {
			if value := extract(pattern); value != "" {
				return value
			}
		}
		return ""
	}

	sid := first(`var\s+moly_sid\s*=\s*['"]([^'"]+)['"]`, `var\s+mSid\s*=\s*['"]([^'"]+)['"]`, `var\s+sessionId\s*=\s*['"]([^'"]+)['"]`)
	token := first(`var\s+mToken\s*=\s*['"]([^'"]+)['"]`, `var\s+authToken\s*=\s*['"]([^'"]+)['"]`)
	file := first(`var\s+file_code\s*=\s*['"]([^'"]+)['"]`, `var\s+mFile\s*=\s*['"]([^'"]+)['"]`, `var\s+fileCode\s*=\s*['"]([^'"]+)['"]`)
	fileId := first(`var\s+file_id\s*=\s*['"]?(\d+)['"]?`, `var\s+mFileID\s*=\s*['"]([^'"]+)['"]`, `var\s+fileId\s*=\s*['"]([^'"]+)['"]`)
	cd := first(`var\s+mCd\s*=\s*['"]([^'"]+)['"]`, `var\s+contentId\s*=\s*['"]([^'"]+)['"]`)
	usr := first(`var\s+usr_id\s*=\s*['"]?(\d+)['"]?`, `var\s+mUsr\s*=\s*['"]([^'"]+)['"]`, `var\s+userId\s*=\s*['"]([^'"]+)['"]`)
	ear := first(`var\s+ear\s*=\s*['"]?(\d+)['"]?`, `var\s+mEar\s*=\s*['"]([^'"]+)['"]`, `var\s+earlyAccess\s*=\s*['"]([^'"]+)['"]`)
	asn := first(`var\s+mAsn\s*=\s*['"]([^'"]+)['"]`, `var\s+asn\s*=\s*['"]([^'"]+)['"]`)
	duration := first(`duration:\s*['"]?(\d+)['"]?`, `var\s+duration\s*=\s*['"]?(\d+)['"]?`)
	pingURL := first(`var\s+pingUrl\s*=\s*['"]([^'"]+)['"]`)
	watchURL := first(`var\s+watchUrl\s*=\s*['"]([^'"]+)['"]`)
	pageReferer := first(`var\s+mRef\s*=\s*['"]([^'"]+)['"]`, `var\s+molyReferer\s*=\s*['"]([^'"]+)['"]`)
	if pageReferer != "" {
		referer = pageReferer
	}

	// Intervals: setInterval(sendMolyPing, 20000) → 20s
	intervalStr := first(`setInterval\s*\(\s*(?:sendMolyPing|sendVerificationPing)\s*,\s*(\d+)\s*\)`)
	watchIntervalStr := extract(`setInterval\s*\(\s*sendWatchPing\s*,\s*(\d+)\s*\)`)

	interval, watchInterval := 20, 25
	if intervalStr != "" {
		ms := 0
		fmt.Sscanf(intervalStr, "%d", &ms)
		if ms > 0 {
			interval = ms / 1000
		}
	}
	if watchIntervalStr != "" {
		ms := 0
		fmt.Sscanf(watchIntervalStr, "%d", &ms)
		if ms > 0 {
			watchInterval = ms / 1000
		}
	}

	if sid == "" || token == "" || file == "" {
		return nil, fmt.Errorf("missing required fields: sid=%q token=%q file=%q", sid, token, file)
	}
	if pingURL == "" {
		pingURL = "https://sw.vidmoly.me/v1/ping"
	}
	if watchURL == "" {
		watchURL = "https://sw.vidmoly.me/v1/watch"
	}
	if cd == "" {
		cd = fileId
	}

	return &VidmolyPingConfig{
		PingUrl:       pingURL,
		WatchUrl:      watchURL,
		PingInterval:  interval,
		WatchInterval: watchInterval,
		SessionId:     sid,
		Token:         token,
		FileCode:      file,
		FileId:        fileId,
		ContentId:     cd,
		UserId:        usr,
		EarlyAccess:   ear,
		Asn:           asn,
		Referer:       referer,
		Duration:      duration,
	}, nil
}

func init() {
	Register(&Vidmoly{})
}
