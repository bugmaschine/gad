package extractors

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

type Filemoon struct{}

func (f *Filemoon) Names() []string {
	return []string{"Filemoon", "MoonF"}
}

func (f *Filemoon) SupportedFrom() SupportedFrom {
	return SupportedFromUrl | SupportedFromSource
}

func (f *Filemoon) SupportsUrl(url string) bool {
	// Filemoon support is usually through source or direct match, mirror Rust logic
	return false
}

func (f *Filemoon) ExtractVideoUrl(ctx context.Context, from ExtractFrom) (*ExtractedVideo, error) {
	redirectRe := regexp.MustCompile(`<iframe *(?:[^>]+ )?src=(?:'([^']+)'|"([^"]+)")[^>]*>`)
	scriptRe := regexp.MustCompile(`(?s)<script\s+[^>]*?data-cfasync=["']?false["']?[^>]*>(.+?)</script>`)
	videoUrlRe := regexp.MustCompile(`(?s)file:\s*"([^"]+\.m3u8[^"]*)"`)

	source, err := GetSource(ctx, from)
	if err != nil {
		return nil, err
	}

	if matches := redirectRe.FindStringSubmatch(source); len(matches) > 1 {
		redirectUrl := matches[1]
		if redirectUrl == "" {
			redirectUrl = matches[2]
		}

		req, err := http.NewRequestWithContext(ctx, "GET", redirectUrl, nil)
		if err != nil {
			return nil, err
		}
		if from.UserAgent != "" {
			req.Header.Set("User-Agent", from.UserAgent)
		}
		if from.Referer != "" {
			req.Header.Set("Referer", from.Referer)
		}
		req.Header.Set("sec-fetch-dest", "iframe")

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		body, _ := io.ReadAll(resp.Body)
		source = string(body)
	}

	scripts := scriptRe.FindAllStringSubmatch(source, -1)
	for _, script := range scripts {
		content := strings.TrimSpace(script[1])
		if !strings.HasPrefix(content, "eval(") {
			continue
		}

		unpacked, ok := DecodePackedCodes(content)
		if !ok {
			continue
		}

		if videoMatches := videoUrlRe.FindStringSubmatch(unpacked); len(videoMatches) > 1 {
			return &ExtractedVideo{
				Url: videoMatches[1],
			}, nil
		}
	}

	return nil, fmt.Errorf("Filemoon: failed to retrieve sources")
}

func init() {
	Register(&Filemoon{})
}
