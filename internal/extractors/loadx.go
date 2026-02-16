package extractors

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

type LoadX struct{}

func (l *LoadX) Names() []string {
	return []string{"LoadX"}
}

func (l *LoadX) SupportedFrom() SupportedFrom {
	return SupportedFromUrl | SupportedFromSource
}

func (l *LoadX) SupportsUrl(urlStr string) bool {
	return IsUrlHostAndHasPath(urlStr, "loadx.ws", true, false)
}

func (l *LoadX) ExtractVideoUrl(ctx context.Context, from ExtractFrom) (*ExtractedVideo, error) {
	evalRe := regexp.MustCompile(`(?s)(eval\(function\(p,a,c,k,e,d\).+?)</script>`)
	idRe := regexp.MustCompile(`FirePlayer\(\s*"([^"]+)"`)

	source, err := GetSource(ctx, from)
	if err != nil {
		return nil, err
	}

	var id string
	evals := evalRe.FindAllStringSubmatch(source, -1)
	for _, eval := range evals {
		unpacked, ok := DecodePackedCodes(eval[1])
		if !ok {
			continue
		}

		if matches := idRe.FindStringSubmatch(unpacked); len(matches) > 1 {
			id = matches[1]
			break
		}
	}

	if id == "" {
		return nil, fmt.Errorf("LoadX: failed to retrieve id")
	}

	// POST request to get video source
	apiUrl := fmt.Sprintf("https://loadx.ws/player/index.php?data=%s&do=getVideo", id)
	bodyData := url.Values{}
	bodyData.Set("hash", id)
	bodyData.Set("r", "")

	req, err := http.NewRequestWithContext(ctx, "POST", apiUrl, strings.NewReader(bodyData.Encode()))
	if err != nil {
		return nil, err
	}

	ua := from.UserAgent
	if ua == "" {
		ua = "Mozilla/5.0 (X11; Linux x86_64; rv:109.0) Gecko/20100101 Firefox/115.0"
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var data map[string]interface{}
	if err := json.Unmarshal(respBytes, &data); err != nil {
		return nil, err
	}

	if videoSource, ok := data["videoSource"].(string); ok {
		return &ExtractedVideo{
			Url: videoSource,
		}, nil
	}

	return nil, fmt.Errorf("LoadX: failed to retrieve sources from API response")
}

func init() {
	Register(&LoadX{})
}
