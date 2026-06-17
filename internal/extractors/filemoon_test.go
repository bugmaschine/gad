package extractors

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"testing"
)

type filemoonRoundTripFunc func(*http.Request) (*http.Response, error)

func (fn filemoonRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func TestFilemoonSupportsKnownHosts(t *testing.T) {
	extractor := &Filemoon{}

	for _, rawURL := range []string{
		"https://filemoon.to/e/example",
		"https://www.filemoon.to/e/example",
		"https://filemoon.sx/e/example",
	} {
		if !extractor.SupportsUrl(rawURL) {
			t.Fatalf("expected Filemoon to support %s", rawURL)
		}
	}
}

func TestFilemoonAPIRootURL(t *testing.T) {
	webURL := getFilemoonBaseURL("filemoon.to", "584hob89v9l3")

	if got, want := filemoonRootURL(webURL), "https://filemoon.to/"; got != want {
		t.Fatalf("unexpected Filemoon API root: got %q want %q", got, want)
	}
}

func TestFilemoonWebAuthnIncludesDeviceClient(t *testing.T) {
	challenge := map[string]any{
		"challenge_id": "PRFswbZRBRnc7THgLfXTd8eV",
		"nonce":        "zy3pijnAVI5TM-0xs3GfkPdE4KZFeD-TCJUwn0IAmLU",
	}
	profile := generateRandomProfile()

	body, err := filemoonWebAuthn(challenge, profile)
	if err != nil {
		t.Fatalf("filemoonWebAuthn returned error: %v", err)
	}
	if body["client"] == nil {
		t.Fatal("expected attestation body to include client fingerprint")
	}
	signature, ok := body["signature"].(string)
	if !ok || signature == "" {
		t.Fatalf("expected signature string, got %T", body["signature"])
	}
	rawSignature, err := base64.RawURLEncoding.DecodeString(signature)
	if err != nil {
		t.Fatalf("signature is not base64url: %v", err)
	}
	if len(rawSignature) != 64 {
		t.Fatalf("expected raw r||s signature to be 64 bytes, got %d", len(rawSignature))
	}
}

func TestFilemoonFingerprintRequestBodyExcludesClient(t *testing.T) {
	body := filemoonFingerprintRequestBody(map[string]any{"token": "example"})

	if _, ok := body["client"]; ok {
		t.Fatalf("fingerprint request body should not include client: %v", body)
	}
	if _, ok := body["fingerprint"]; !ok {
		t.Fatalf("fingerprint request body missing fingerprint: %v", body)
	}
}

func TestFilemoonPostJSONUsesCookieJar(t *testing.T) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New returned error: %v", err)
	}

	client := &http.Client{
		Jar: jar,
		Transport: filemoonRoundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.Path {
			case "/challenge":
				if got := req.Header.Get("Cookie"); got != "" {
					t.Fatalf("first request unexpectedly included cookie header %q", got)
				}
				return filemoonTestJSONResponse(req, `{"ok": true}`, "byse_challenge=ok; Path=/"), nil
			case "/attest":
				cookie, err := req.Cookie("byse_challenge")
				if err != nil {
					t.Fatalf("second request did not include jar cookie: %v", err)
				}
				if cookie.Value != "ok" {
					t.Fatalf("unexpected jar cookie value: got %q want %q", cookie.Value, "ok")
				}
				return filemoonTestJSONResponse(req, `{"ok": true}`, ""), nil
			default:
				t.Fatalf("unexpected request path %q", req.URL.Path)
				return nil, nil
			}
		}),
	}

	extractor := &Filemoon{}
	headers := map[string]string{"User-Agent": filemoonDefaultUA}
	if _, err := extractor.postJSON(context.Background(), client, "http://filemoon.test/challenge", headers, map[string]any{}); err != nil {
		t.Fatalf("challenge postJSON returned error: %v", err)
	}
	if _, err := extractor.postJSON(context.Background(), client, "http://filemoon.test/attest", headers, map[string]any{}); err != nil {
		t.Fatalf("attest postJSON returned error: %v", err)
	}
}

func TestFilemoonSetFingerprintCookiesStoresViewerAndDevice(t *testing.T) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New returned error: %v", err)
	}

	err = filemoonSetFingerprintCookies(jar, "https://filemoon.to/", map[string]any{
		"viewer_id":  "viewer-123",
		"device_id":  "device-456",
		"token":      "token",
		"confidence": 1,
	})
	if err != nil {
		t.Fatalf("filemoonSetFingerprintCookies returned error: %v", err)
	}

	requestURL, err := url.Parse("https://filemoon.to/api/videos/example/embed/captcha")
	if err != nil {
		t.Fatalf("url.Parse returned error: %v", err)
	}
	cookies := map[string]string{}
	for _, cookie := range jar.Cookies(requestURL) {
		cookies[cookie.Name] = cookie.Value
	}

	for name, want := range map[string]string{
		"byse_viewer_id": "viewer-123",
		"byse_device_id": "device-456",
	} {
		if got := cookies[name]; got != want {
			t.Fatalf("unexpected %s cookie: got %q want %q", name, got, want)
		}
	}
}

func filemoonTestJSONResponse(req *http.Request, body, setCookie string) *http.Response {
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Status:     "200 OK",
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
		Request:    req,
	}
	if setCookie != "" {
		resp.Header.Set("Set-Cookie", setCookie)
	}
	return resp
}

func TestSolveFilemoonPoWUsesByseCustomHash(t *testing.T) {
	const nonce = "ae21175f369e2f0f3cb66100745bbdde"
	const difficulty = 8

	solution := solveFilemoonPoW(nonce, difficulty, 1.0)
	if solution != "291" {
		t.Fatalf("unexpected PoW solution: got %q want %q", solution, "291")
	}
	if got := filemoonPoWLeadingZeroBits(nonce + ":" + solution); got < difficulty {
		t.Fatalf("solution has %d leading zero bits, want at least %d", got, difficulty)
	}

	sha := sha256.Sum256([]byte(nonce + ":" + solution))
	if got := countLeadingZeroBitsBytes(sha[:]); got >= difficulty {
		t.Fatalf("captured Byse custom-hash solution unexpectedly satisfies SHA-256 with %d leading zero bits", got)
	}
}

func TestFilemoonVerifyTokenErrorIncludesStatusReason(t *testing.T) {
	_, err := filemoonVerifyToken(map[string]any{
		"status": "error",
		"reason": "invalid_pow",
	})
	if err == nil {
		t.Fatal("expected missing token error")
	}
	message := err.Error()
	for _, want := range []string{`status="error"`, `reason="invalid_pow"`} {
		if !strings.Contains(message, want) {
			t.Fatalf("expected error %q to contain %q", message, want)
		}
	}
}

func TestExtractFilemoonPlaybackM3U8(t *testing.T) {
	body := []byte(`{
		"playback": {
			"algorithm": "AES-256-GCM",
			"iv": "fjhrNeGjQi4PQ9nL",
			"payload": "Epv3yIGCTQTXyftDenzVxt891uKUjA_0n1LRSVu6l3XPsYTSp-XAqhZITYyjTXBtFbF-w7wUVnImWaej7ChY9UnuFPEKyFbgdIgfSqb077UleYvOIc8pWbR1i8DWIPhvVNU0L_n0jeSiXErUCjGYBAATbzNBqo_EW9LtroXlHpAGiImJnbGtjKUGsrtq44AnVLlyOhUMPIddLK6pRG_rUb_U4HGm_E9Lyc9zDe_b0ZcKeNYcIQ83_CbYX6pyj2lNaZ7ocl6ZHfbJTx8-u_BBTlTl4TrkHW0ggRkyDJfQPkgtv1-hlRTT7cglEzhjQtZ8JRFZmQuiumpHXtbPwFUYD5UevTHaR_5P6eJcYoY49cYtVuH5uCFG9ANBscPd0weqN9wT5otMdnBjaYKKxypK7fC7aG_FQMzs5uZaH4RzA6k4WmGTuOKV6J8LIeJ_2xwcN0vHEwqyhWLVVWRWyiqeEgNEhTFpda8WcbP8gVXlx53UjmhURD3beZGF98Oi0UfvP0eIOYQpY2MIpX4CGIxvNErLXUtQe-xwWYWpI-6jxxjIhX2LCXgfILcrvQD4bZA6AdE6q0fFBpIHM8v5c7vFwpmz74Gx40IqjUCLooc4IhE4QQ3IsRLZOOK-io79Yx-uithMy8C_Xe_vcnaOkR2MltJPjm707buLI9IvgcLYP06pyoqhkRBNYOwj4qJ2CdqEw0CLETMIrt7cAZx42qbt4rw-uY7Df2xACLRr9A",
			"key_parts": [
				"gCOn19-RN8m5fTwNM8FSYX3oaU0jdhd4",
				"ei_S1FYKjxXLpe1aNx7CT1yKH0szHAO2",
				"eULyN-UMv4HWt-5tRLvGzxuXHbw4vJhR",
				"2jDMoZpdTJnlwCKvxfVF0l_ddeIHvQPr",
				"CYYfZhGqsCAF0D5WZJvJO74HFzbc4BCB",
				"xj1XoNAAwr9mY4-keu3BjiuuFDNt1qwF",
				"iNvp5XZe7ZOwx82hnk9YGBbL4SJpZTqW",
				"zItW5gfeFWwObKOwCse-RDWsbMC90m0f",
				"P6OHJZM3BzkYedzX5Kg1y_u7FbC_s-GG",
				"70DZUr5p0b3NMYXloiAKWA",
				"sKmgSTM8gcINAQ_6r1KGNouFt5kX7r_V",
				"VBrlP4bJksXILS59_PvTHY9aROaemafx",
				"2D0SVAIwi9OJqAaU4g-e4sb3QH5oH37d",
				"a4oNy0cKi7W_gJOqkOJNNJiFgqRLwGus",
				"hV1EDGzM5LWykGeL2b2wu9sDK36Gt9GW",
				"ytjTb43NRyj5X4Sn_wFXH6G8TyN8GN0k",
				"VSFFHI2tx_wsxizyFmT2iYCoVbCKagQX",
				"peYzYl0MQ1Z2KkmhCnc4KJkjpOJjDo1F",
				"v_aSAAVGVhSGh-WWJYdGPOjlD8PXbeyX",
				"LciNdURkgmGRxrt2IXTlEr235TSkm-6b",
				"eRrPLctEPIJTTH9n5IsYdw",
				"WI5qzhy51NNKthGVnrwsEBljjtqjVmsd",
				"AzG2whWVK1KeAlN-E_R4skJxcX2ddvZs",
				"0Jf90wms6jK3zRiEfgL62MVlGl-7s8q-",
				"0iQ25a-6Km2OKKKw6g2kkX8gyaskkXcE",
				"Cjx6QKxhf_KKtyRQHufi7Wa6fxGpUfdz",
				"Gqmju7pJDu_TiYHSE22ejdhOxzhQplpf",
				"52UfL26Ptu9pY2Mr7tbrb3zEWwsgJtMZ",
				"54UwyMUw2qdK6GlClhwj1bmLr4mDSymJ",
				"GdUtPxsxYtL9zWHRZUbiMEOGf7hRrYLV"
			],
			"version": "10",
			"expires_at": "2026-06-04T17:55:18.825612875Z"
		},
		"cache_status": "hit"
	}`)

	got, err := extractFilemoonPlaybackM3U8(body)
	if err != nil {
		t.Fatalf("extractFilemoonPlaybackM3U8 returned error: %v", err)
	}
	want := "https://edge1-vienna-sprintcdn.owphbf24.com/hls2/04/09334/av9wz4o9e1c6_h/master.m3u8?t=sOdIDHaSBquyjGew4xp_LJk713zoE5XMNJ2YhQ8PUfs&s=1780594818&e=10800&f=46673869&srv=1065&asn=3209&sp=4000&p=0"
	if got != want {
		t.Fatalf("unexpected playback m3u8 url: %q", got)
	}
}
