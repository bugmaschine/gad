package extractors

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

func IsUrlHostAndHasPath(rawUrl string, expectedHost string, mustHavePath bool, ignoreCase bool) bool {
	u, err := url.Parse(rawUrl)
	if err != nil {
		return false
	}

	host := u.Host
	if ignoreCase {
		host = strings.ToLower(host)
		expectedHost = strings.ToLower(expectedHost)
	}

	if host != expectedHost && !strings.HasSuffix(host, "."+expectedHost) {
		return false
	}

	if mustHavePath && (u.Path == "" || u.Path == "/") {
		return false
	}

	return true
}

func GetSource(ctx context.Context, from ExtractFrom) (string, error) {
	if from.Source != "" {
		return from.Source, nil
	}

	req, err := http.NewRequestWithContext(ctx, "GET", from.Url, nil)
	if err != nil {
		return "", err
	}

	if from.UserAgent != "" {
		req.Header.Set("User-Agent", from.UserAgent)
	}
	if from.Referer != "" {
		req.Header.Set("Referer", from.Referer)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to fetch source: status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(body), nil
}

func DecodePackedCodes(code string) (string, bool) {
	re := regexp.MustCompile(`}\('(.+)',(\d+),(\d+),'([^']+)'\.split\('\|'\)`)
	matches := re.FindStringSubmatch(code)
	if len(matches) < 5 {
		return "", false
	}

	obfuscated := matches[1]
	base, _ := strconv.Atoi(matches[2])
	count, _ := strconv.Atoi(matches[3])
	symbols := strings.Split(matches[4], "|")

	symbolTable := make(map[string]string)
	for i := count - 1; i >= 0; i-- {
		baseNCount := EncodeBaseN(i, base)
		symbolValue := ""
		if i < len(symbols) {
			symbolValue = symbols[i]
		}

		if symbolValue == "" {
			symbolTable[baseNCount] = baseNCount
		} else {
			symbolTable[baseNCount] = symbolValue
		}
	}

	wordRe := regexp.MustCompile(`\b(\w+)\b`)
	result := wordRe.ReplaceAllStringFunc(obfuscated, func(word string) string {
		if val, ok := symbolTable[word]; ok {
			return val
		}
		return word
	})

	return result, true
}

func EncodeBaseN(num int, base int) string {
	const table = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"
	if num == 0 {
		return string(table[0])
	}

	var res []byte
	n := num
	for n > 0 {
		res = append([]byte{table[n%base]}, res...)
		n /= base
	}
	return string(res)
}
