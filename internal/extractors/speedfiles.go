package extractors

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
)

type Speedfiles struct{}

func (s *Speedfiles) Names() []string {
	return []string{"Speedfiles"}
}

func (s *Speedfiles) SupportedFrom() SupportedFrom {
	return SupportedFromUrl | SupportedFromSource
}

func (s *Speedfiles) SupportsUrl(urlStr string) bool {
	return IsUrlHostAndHasPath(urlStr, "speedfiles.net", true, false)
}

func (s *Speedfiles) ExtractVideoUrl(ctx context.Context, from ExtractFrom) (*ExtractedVideo, error) {
	source, err := GetSource(ctx, from)
	if err != nil {
		return nil, err
	}

	videoUrlRe := regexp.MustCompile(`(?:var|let|const) \w+ = ["']((?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{4}|[A-Za-z0-9+/]{3}=|[A-Za-z0-9+/]{2}={2}))["'];`)
	matches := videoUrlRe.FindAllStringSubmatch(source, -1)

	for _, match := range matches {
		if result, ok := s.decodeUrl(match[1]); ok {
			return &ExtractedVideo{
				Url: result,
			}, nil
		}
	}

	return nil, fmt.Errorf("Speedfiles: failed to retrieve sources")
}

func (s *Speedfiles) decodeUrl(input string) (string, bool) {
	// Step 1: Decode base64
	d, err := base64.StdEncoding.DecodeString(input)
	if err != nil {
		return "", false
	}

	// Step 2: Flip case and reverse
	d = s.flipCase(d)
	d = s.reverseBytes(d)

	// Step 3: Decode base64 and reverse
	d, err = base64.StdEncoding.DecodeString(string(d))
	if err != nil {
		return "", false
	}
	d = s.reverseBytes(d)

	// Step 4: Parse hex chunks of 2 and subtract 3
	var hexDecoded []byte
	for i := 0; i+1 < len(d); i += 2 {
		val, err := strconv.ParseUint(string(d[i:i+2]), 16, 8)
		if err != nil || val < 3 {
			return "", false
		}
		hexDecoded = append(hexDecoded, byte(val-3))
	}
	d = hexDecoded

	// Step 5: Flip case and reverse
	d = s.flipCase(d)
	d = s.reverseBytes(d)

	// Step 6: Final base64 decode
	d, err = base64.StdEncoding.DecodeString(string(d))
	if err != nil {
		return "", false
	}

	finalStr := string(d)
	if _, err := url.Parse(finalStr); err != nil {
		return "", false
	}

	return finalStr, true
}

func (s *Speedfiles) flipCase(b []byte) []byte {
	res := make([]byte, len(b))
	for i, x := range b {
		if (x >= 'a' && x <= 'z') || (x >= 'A' && x <= 'Z') {
			res[i] = x ^ 32
		} else {
			res[i] = x
		}
	}
	return res
}

func (s *Speedfiles) reverseBytes(b []byte) []byte {
	res := make([]byte, len(b))
	for i := 0; i < len(b); i++ {
		res[i] = b[len(b)-1-i]
	}
	return res
}

func init() {
	Register(&Speedfiles{})
}
