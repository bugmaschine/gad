package extractors

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
)

type Streamtape struct{}

func (s *Streamtape) Names() []string {
	return []string{"Streamtape"}
}

func (s *Streamtape) SupportedFrom() SupportedFrom {
	return SupportedFromUrl | SupportedFromSource
}

func (s *Streamtape) SupportsUrl(urlStr string) bool {
	hosts := []string{
		"streamtape.com",
		"shavetape.cash",
		"streamtape.xyz",
		"streamtape.net",
	}
	for _, host := range hosts {
		if IsUrlHostAndHasPath(urlStr, host, true, true) {
			return true
		}
	}
	return false
}

func (s *Streamtape) ExtractVideoUrl(ctx context.Context, from ExtractFrom) (*ExtractedVideo, error) {
	source, err := GetSource(ctx, from)
	if err != nil {
		return nil, err
	}

	robotLinkRe := regexp.MustCompile(`<div\s*[^>]*?id="robotlink"[^>]*?>[^<]*?(/get_video[^<]+?)</div>`)
	tokenRe := regexp.MustCompile(`&token=([^&?\s'"]+)`)

	robotMatches := robotLinkRe.FindStringSubmatch(source)
	if len(robotMatches) < 2 {
		return nil, fmt.Errorf("Streamtape: failed to find robotlink")
	}
	robotUrl := robotMatches[1]

	tokenMatches := tokenRe.FindAllStringSubmatch(source, -1)
	if len(tokenMatches) == 0 {
		return nil, fmt.Errorf("Streamtape: failed to find token")
	}
	// Use the last token as per Rust implementation
	token := tokenMatches[len(tokenMatches)-1][1]

	finalUrl, err := url.Parse("https://streamtape.com" + robotUrl)
	if err != nil {
		return nil, err
	}

	q := finalUrl.Query()
	q.Set("token", token)
	q.Set("stream", "1")
	finalUrl.RawQuery = q.Encode()

	return &ExtractedVideo{
		Url: finalUrl.String(),
	}, nil
}

func init() {
	Register(&Streamtape{})
}
