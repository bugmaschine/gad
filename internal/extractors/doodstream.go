package extractors

import (
	"context"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"regexp"
	"time"
)

type Doodstream struct{}

func (d *Doodstream) Names() []string {
	return []string{"Doodstream"}
}

func (d *Doodstream) SupportedFrom() SupportedFrom {
	return SupportedFromUrl
}

func (d *Doodstream) SupportsUrl(url string) bool {
	hosts := []string{
		"dood.li", "dood.la", "ds2video.com", "ds2play.com", "dood.yt", "dood.ws", "dood.so",
		"dood.to", "dood.pm", "dood.watch", "dood.sh", "dood.cx", "dood.wf", "dood.re",
		"dood.one", "dood.tech", "dood.work", "doods.pro", "dooood.com", "doodstream.com",
		"doodstream.co", "d000d.com", "d0000d.com", "doodapi.com", "d0o0d.com", "do0od.com",
		"dooodster.com", "vidply.com", "do7go.com", "all3do.com", "doply.net",
	}
	for _, host := range hosts {
		if IsUrlHostAndHasPath(url, host, true, true) {
			return true
		}
	}
	return false
}

func (d *Doodstream) ExtractVideoUrl(ctx context.Context, from ExtractFrom) (*ExtractedVideo, error) {
	if from.Url == "" {
		return nil, fmt.Errorf("Doodstream: from source is unsupported")
	}

	client := &http.Client{}
	req, err := http.NewRequestWithContext(ctx, "GET", from.Url, nil)
	if err != nil {
		return nil, err
	}
	if from.UserAgent != "" {
		req.Header.Set("User-Agent", from.UserAgent)
	}
	if from.Referer != "" {
		req.Header.Set("Referer", from.Referer)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Doodstream: failed to retrieve sources: %w", err)
	}
	defer resp.Body.Close()

	resolvedUrl := resp.Request.URL
	source, err := GetSource(ctx, from) // This might fetch again, but it's safer for now
	if err != nil {
		return nil, err
	}

	fetchRe := regexp.MustCompile(`(?s)\$\.get\(\s*['"](/pass_md5/[\w-]+/([\w-]+))['"]\s*,\s*function\(\s*data\s*\)`)
	matches := fetchRe.FindStringSubmatch(source)
	if len(matches) < 3 {
		return nil, fmt.Errorf("Doodstream: failed to retrieve sources (no match)")
	}

	relativeFetchUrl := matches[1]
	token := matches[2]

	fetchUrl, err := resolvedUrl.Parse(relativeFetchUrl)
	if err != nil {
		return nil, err
	}

	req2, err := http.NewRequestWithContext(ctx, "GET", fetchUrl.String(), nil)
	if err != nil {
		return nil, err
	}
	req2.Header.Set("Referer", resolvedUrl.String())
	if from.UserAgent != "" {
		req2.Header.Set("User-Agent", from.UserAgent)
	}

	resp2, err := client.Do(req2)
	if err != nil {
		return nil, err
	}
	defer resp2.Body.Close()

	videoBaseBytes, err := ioReadAll(resp2.Body)
	if err != nil {
		return nil, err
	}
	videoBaseUrl := string(videoBaseBytes)

	randomString := generateRandomString(10)
	unixTimeMillis := time.Now().UnixNano() / int64(time.Millisecond)

	finalVideoUrl := fmt.Sprintf("%s%s?token=%s&expiry=%d", videoBaseUrl, randomString, token, unixTimeMillis)

	videoUrlReferer, _ := resolvedUrl.Parse("/")

	return &ExtractedVideo{
		Url:     finalVideoUrl,
		Referer: videoUrlReferer.String(),
	}, nil
}

func generateRandomString(n int) string {
	const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, n)
	for i := range b {
		b[i] = chars[rand.Intn(len(chars))]
	}
	return string(b)
}

func ioReadAll(r io.Reader) ([]byte, error) {
	// Simple helper
	return io.ReadAll(r)
}

// Ensure init registers it
func init() {
	Register(&Doodstream{})
}
