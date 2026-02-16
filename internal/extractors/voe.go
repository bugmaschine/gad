package extractors

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf16"
)

type Voe struct{}

func (v *Voe) Names() []string {
	return []string{"Voe"}
}

func (v *Voe) SupportedFrom() SupportedFrom {
	return SupportedFromUrl | SupportedFromSource
}

func (v *Voe) SupportsUrl(url string) bool {
	return false
}

func (v *Voe) ExtractVideoUrl(ctx context.Context, from ExtractFrom) (*ExtractedVideo, error) {
	redirectRe := regexp.MustCompile(`window\.location\.href *= *(?:'([^']+)'|"([^"]+)") *;`)

	source, err := GetSource(ctx, from)
	if err != nil {
		return nil, err
	}

	if matches := redirectRe.FindStringSubmatch(source); len(matches) > 1 {
		redirectUrl := matches[1]
		if redirectUrl == "" {
			redirectUrl = matches[2]
		}
		newFrom := from
		newFrom.Url = redirectUrl
		source, err = GetSource(ctx, newFrom)
		if err != nil {
			return nil, err
		}
	}

	if ev, err := v.extract1(source); err == nil {
		return ev, nil
	}
	if ev, err := v.extract2(source); err == nil {
		return ev, nil
	}
	if ev, err := v.extract3(source); err == nil {
		return ev, nil
	}

	return nil, fmt.Errorf("Voe: failed to retrieve sources")
}

func (v *Voe) extract1(source string) (*ExtractedVideo, error) {
	re := regexp.MustCompile(`'hls': '([^']+)'`)
	matches := re.FindStringSubmatch(source)
	if len(matches) < 2 {
		return nil, fmt.Errorf("no match")
	}

	videoUrl := matches[1]
	decoded, err := base64.StdEncoding.DecodeString(videoUrl)
	if err == nil {
		videoUrl = string(decoded)
	}

	return &ExtractedVideo{
		Url: videoUrl,
	}, nil
}

func (v *Voe) extract2(source string) (*ExtractedVideo, error) {
	re := regexp.MustCompile(`let \w+ = '((?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{4}|[A-Za-z0-9+/]{3}=|[A-Za-z0-9+/]{2}={2}))';`)
	matches := re.FindStringSubmatch(source)
	if len(matches) < 2 {
		return nil, fmt.Errorf("no match")
	}

	decoded, err := base64.StdEncoding.DecodeString(matches[1])
	if err != nil {
		return nil, err
	}

	for i, j := 0, len(decoded)-1; i < j; i, j = i+1, j-1 {
		decoded[i], decoded[j] = decoded[j], decoded[i]
	}

	var data map[string]interface{}
	if err := json.Unmarshal(decoded, &data); err != nil {
		return nil, err
	}

	if file, ok := data["file"].(string); ok {
		return &ExtractedVideo{
			Url: file,
		}, nil
	}

	return nil, fmt.Errorf("no file in json")
}

func (v *Voe) extract3(source string) (*ExtractedVideo, error) {
	re := regexp.MustCompile(`'((?:[A-Za-z0-9+/=_]|@\$|\^\^|~@|%\?|\*~|!!|#&)+)'|"((?:[A-Za-z0-9+/=_]|@\$|\^\^|~@|%\?|\*~|!!|#&)+)"`)
	matches := re.FindAllStringSubmatch(source, -1)

	for _, match := range matches {
		group := match[1]
		if group == "" {
			group = match[2]
		}

		videoUrl, ok := v.decode3(group)
		if ok {
			return &ExtractedVideo{
				Url: videoUrl,
			}, nil
		}
	}

	return nil, fmt.Errorf("no match")
}

func (v *Voe) decode3(input string) (string, bool) {
	s1 := v.rot13(input)
	s2 := v.cleanSymbols(s1)
	s3 := strings.ReplaceAll(s2, "_", "")
	s4Bytes, err := base64.StdEncoding.DecodeString(s3)
	if err != nil {
		return "", false
	}
	s4 := string(s4Bytes)
	s5, ok := v.shiftChars(s4, 3)
	if !ok {
		return "", false
	}
	// Reverse string
	runes := []rune(s5)
	for i, j := 0, len(runes)-1; i < j; i, j = i+1, j-1 {
		runes[i], runes[j] = runes[j], runes[i]
	}
	s6 := string(runes)
	s7Bytes, err := base64.StdEncoding.DecodeString(s6)
	if err != nil {
		return "", false
	}

	var data map[string]interface{}
	if err := json.Unmarshal(s7Bytes, &data); err != nil {
		return "", false
	}

	if file, ok := data["source"].(string); ok {
		return file, true
	}

	return "", false
}

func (v *Voe) rot13(input string) string {
	res := make([]byte, len(input))
	for i := 0; i < len(input); i++ {
		c := input[i]
		if c >= 'A' && c <= 'Z' {
			res[i] = ((c - 'A' + 13) % 26) + 'A'
		} else if c >= 'a' && c <= 'z' {
			res[i] = ((c - 'a' + 13) % 26) + 'a'
		} else {
			res[i] = c
		}
	}
	return string(res)
}

func (v *Voe) cleanSymbols(input string) string {
	patterns := []string{"@$", "^^", "~@", "%?", "*~", "!!", "#&"}
	res := input
	for _, p := range patterns {
		res = strings.ReplaceAll(res, p, "_")
	}
	return res
}

func (v *Voe) shiftChars(input string, shift int) (string, bool) {
	u16 := utf16.Encode([]rune(input))
	res := make([]uint16, len(u16))
	for i, c := range u16 {
		res[i] = uint16((int(c) - shift + 65536) % 65536)
	}
	return string(utf16.Decode(res)), true
}

func init() {
	Register(&Voe{})
}
