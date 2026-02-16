package extractors

import (
	"context"
	"strings"
)

type SupportedFrom int

const (
	SupportedFromUrl SupportedFrom = 1 << iota
	SupportedFromSource
)

type ExtractedVideo struct {
	Url       string
	Referer   string
	UserAgent string
	IsM3U8    bool
	Filename  string
}

type Extractor interface {
	Names() []string
	SupportedFrom() SupportedFrom
	SupportsUrl(url string) bool
	ExtractVideoUrl(ctx context.Context, source ExtractFrom) (*ExtractedVideo, error)
}

type ExtractFrom struct {
	Url       string
	UserAgent string
	Referer   string
	Source    string
}

var registry []Extractor

func Register(e Extractor) {
	registry = append(registry, e)
}

func GetExtractors() []Extractor {
	return registry
}

func GetExtractorByName(name string) Extractor {
	for _, e := range registry {
		for _, n := range e.Names() {
			if strings.EqualFold(n, name) {
				return e
			}
		}
	}
	return nil
}

func ExistsExtractorWithName(name string) bool {
	return GetExtractorByName(name) != nil
}

func ExtractVideoUrl(ctx context.Context, url string, userAgent, referer string) (*ExtractedVideo, error) {
	for _, e := range registry {
		if (e.SupportedFrom()&SupportedFromUrl) != 0 && e.SupportsUrl(url) {
			res, err := e.ExtractVideoUrl(ctx, ExtractFrom{Url: url, UserAgent: userAgent, Referer: referer})
			if err == nil && res != nil {
				return res, nil
			}
		}
	}
	return nil, nil
}

func ExtractVideoUrlWithExtractor(ctx context.Context, url string, name string, userAgent, referer string) (*ExtractedVideo, error) {
	e := GetExtractorByName(name)
	if e == nil {
		return nil, nil
	}
	return e.ExtractVideoUrl(ctx, ExtractFrom{Url: url, UserAgent: userAgent, Referer: referer})
}
