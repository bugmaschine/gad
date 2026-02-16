package extractors

import (
	"context"
	"fmt"
	"regexp"
)

type Vidoza struct{}

func (v *Vidoza) Names() []string {
	return []string{"Vidoza"}
}

func (v *Vidoza) SupportedFrom() SupportedFrom {
	return SupportedFromUrl | SupportedFromSource
}

func (v *Vidoza) SupportsUrl(urlStr string) bool {
	hosts := []string{
		"vidoza.net",
		"videzz.net",
	}
	for _, host := range hosts {
		if IsUrlHostAndHasPath(urlStr, host, true, true) {
			return true
		}
	}
	return false
}

func (v *Vidoza) ExtractVideoUrl(ctx context.Context, from ExtractFrom) (*ExtractedVideo, error) {
	source, err := GetSource(ctx, from)
	if err != nil {
		return nil, err
	}

	videoUrlRe := regexp.MustCompile(`(?s)sourcesCode:\s\[\{\ssrc:\s"(.+)", type`)
	matches := videoUrlRe.FindStringSubmatch(source)
	if len(matches) < 2 {
		return nil, fmt.Errorf("Vidoza: failed to retrieve sources")
	}

	return &ExtractedVideo{
		Url: matches[1],
	}, nil
}

func init() {
	Register(&Vidoza{})
}
