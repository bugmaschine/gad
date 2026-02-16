package extractors

import (
	"context"
	"fmt"
	"regexp"
)

type Vidmoly struct{}

func (v *Vidmoly) Names() []string {
	return []string{"Vidmoly"}
}

func (v *Vidmoly) SupportedFrom() SupportedFrom {
	return SupportedFromUrl | SupportedFromSource
}

func (v *Vidmoly) SupportsUrl(urlStr string) bool {
	return IsUrlHostAndHasPath(urlStr, "vidmoly.to", true, true)
}

func (v *Vidmoly) ExtractVideoUrl(ctx context.Context, from ExtractFrom) (*ExtractedVideo, error) {
	source, err := GetSource(ctx, from)
	if err != nil {
		return nil, err
	}

	videoUrlRe := regexp.MustCompile(`(?s)file:\s*"([^"]+\.m3u8[^"]*)"`)
	matches := videoUrlRe.FindStringSubmatch(source)
	if len(matches) < 2 {
		return nil, fmt.Errorf("Vidmoly: failed to retrieve sources")
	}

	return &ExtractedVideo{
		Url:     matches[1],
		Referer: "https://vidmoly.to/",
	}, nil
}

func init() {
	Register(&Vidmoly{})
}
