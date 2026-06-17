package extractors

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"math/bits"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/brianvoe/gofakeit/v7"
)

type Filemoon struct{}

const (
	filemoonDefaultUA  = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"
	filemoonPoWTimeout = 20 * time.Second
)

type clientOutput struct {
	Architecture        string             `json:"architecture,omitempty"`
	Bitness             string             `json:"bitness,omitempty"`
	Platform            string             `json:"platform,omitempty"`
	PlatformVersion     string             `json:"platform_version,omitempty"`
	Model               string             `json:"model,omitempty"`
	UaFullVersion       string             `json:"ua_full_version,omitempty"`
	BrandFullVersions   []BrandFullVersion `json:"brand_full_versions,omitempty"`
	PixelRatio          float64            `json:"pixel_ratio"`
	ScreenWidth         int                `json:"screen_width"`
	ScreenHeight        int                `json:"screen_height"`
	ColorDepth          int                `json:"color_depth"`
	Languages           []string           `json:"languages,omitempty"`
	Timezone            string             `json:"timezone,omitempty"`
	HardwareConcurrency int                `json:"hardware_concurrency,omitempty"`
	DeviceMemory        float64            `json:"device_memory,omitempty"`
	TouchPoints         int                `json:"touch_points,omitempty"`
	WebglVendor         string             `json:"webgl_vendor,omitempty"`
	WebglRenderer       string             `json:"webgl_renderer,omitempty"`
	CanvasHash          string             `json:"canvas_hash,omitempty"`
	AudioHash           string             `json:"audio_hash,omitempty"`
	WebglParamsHash     string             `json:"webgl_params_hash,omitempty"`
	FontsHash           string             `json:"fonts_hash,omitempty"`
	CodecsHash          string             `json:"codecs_hash,omitempty"`
	MediaDevices        string             `json:"media_devices,omitempty"`
	PointerType         string             `json:"pointer_type,omitempty"`
	Extra               struct {
		Vendor     string `json:"vendor"`
		AppVersion string `json:"appVersion"`
	} `json:"extra"`
}

// BrandFullVersion mirrors a browser brand entry
type BrandFullVersion struct {
	Brand   string `json:"brand"`
	Version string `json:"version"`
}

// ProfileOutput is the top-level object in the output array
type filemoonDeviceProfile struct {
	UA     string       `json:"ua"`
	Client clientOutput `json:"client"`
}

// generateBase64URL creates a random base64url-encoded string of given byte length (without padding)
func generateBase64URL(byteLen int) string {
	buf := make([]byte, byteLen)
	_, _ = rand.Read(buf)
	return base64.RawURLEncoding.EncodeToString(buf)
}

// randomInt returns a random integer in [min, max]
func randomInt(min, max int) int {
	n, _ := rand.Int(rand.Reader, big.NewInt(int64(max-min+1)))
	return int(n.Int64()) + min
}

// randomChoice picks a random element from a slice
func randomChoice(options []string) string {
	return options[randomInt(0, len(options)-1)]
}

// generateRandomProfile creates a plausible random fingerprint profile
func generateRandomProfile() filemoonDeviceProfile {
	gofakeit.Seed(time.Now().UnixNano())

	// Simulate realistic user agents
	userAgents := []string{
		gofakeit.ChromeUserAgent(),
		gofakeit.FirefoxUserAgent(),
		gofakeit.SafariUserAgent(),
	}
	ua := randomChoice(userAgents)

	// Simulate common platforms
	platforms := []string{"Windows", "macOS", "Linux", "Android", "iOS"}
	arch := "x86"
	bitness := "64"

	platform := randomChoice(platforms)
	switch platform {
	case "Windows":
		arch = "x86"
	case "macOS":
		arch = "arm"
	case "Android":
		arch = "arm"
	case "iOS":
		arch = "arm"
	}
	// Bitness always 64 in modern browsers, occasionally 32 for old Linux/Windows
	if gofakeit.Bool() && (platform == "Linux" || platform == "Windows") {
		bitness = "32"
	}

	languages := []string{gofakeit.LanguageAbbreviation()}
	if gofakeit.Bool() {
		languages = append(languages, gofakeit.LanguageAbbreviation())
	}

	timezone := gofakeit.TimeZoneRegion()

	concurrency := randomInt(2, 32)
	memory := []float64{0.25, 0.5, 1, 2, 4, 8, 16}[randomInt(0, 6)]

	touch := 0
	if strings.Contains(platform, "Android") || strings.Contains(platform, "iOS") {
		touch = randomInt(1, 10)
	}

	// WebGL vendor/renderer
	vendors := []string{
		"Google Inc. (Intel)",
		"Google Inc. (AMD)",
		"Google Inc. (NVIDIA)",
		"Google Inc. (Apple)",
		"Google Inc. (Qualcomm)",
	}
	renderers := map[string][]string{
		"Google Inc. (Intel)":    {"ANGLE (Intel, Intel(R) UHD Graphics 620 Direct3D11 vs_5_0 ps_5_0)"},
		"Google Inc. (AMD)":      {"ANGLE (AMD, Radeon RX 580 Direct3D11 vs_5_0 ps_5_0)"},
		"Google Inc. (NVIDIA)":   {"ANGLE (NVIDIA, GeForce RTX 3060 Direct3D11 vs_5_0 ps_5_0)"},
		"Google Inc. (Apple)":    {"ANGLE (Apple, Apple M1, OpenGL 4.1)"},
		"Google Inc. (Qualcomm)": {"ANGLE (Qualcomm, Adreno 650, OpenGL ES 3.2)"},
	}
	vendor := randomChoice(vendors)
	renderer := randomChoice(renderers[vendor])

	// Hashes (base64url, 32 bytes -> 43 characters)
	canvasHash := generateBase64URL(32)
	audioHash := generateBase64URL(32)
	webglParamsHash := generateBase64URL(32)
	fontsHash := generateBase64URL(32)
	codecsHash := generateBase64URL(32)

	// Media devices string
	ai := randomInt(0, 3)
	ao := randomInt(0, 3)
	vi := randomInt(0, 2)
	mediaDevices := fmt.Sprintf("ai%dao%dvi%d", ai, ao, vi)

	// Pointer type
	pointerTypes := []string{
		"coarse",
		"fine",
		"coarse,fine",
		"fine,hover",
		"coarse,hover",
		"coarse,fine,hover",
	}
	pointerType := randomChoice(pointerTypes)

	// Extra vendor strings
	extraVendors := []string{"Google Inc.", "Apple Computer, Inc.", "Mozilla", ""}
	extraVendor := randomChoice(extraVendors)
	appVersion := gofakeit.AppVersion()

	screenWidths := []int{1920, 2560, 1440, 1366, 1280, 375, 414}
	screenHeights := []int{1080, 1440, 900, 768, 720, 812, 896}
	idx := randomInt(0, len(screenWidths)-1)
	screenW := screenWidths[idx]
	screenH := screenHeights[idx]

	model := "" // model can be empty, as browsers often omit it too

	return filemoonDeviceProfile{
		UA: ua,
		Client: clientOutput{
			Architecture:    arch,
			Bitness:         bitness,
			Platform:        platform,
			PlatformVersion: gofakeit.AppVersion(),
			Model:           model,
			UaFullVersion:   gofakeit.AppVersion(),
			BrandFullVersions: []BrandFullVersion{
				{Brand: "Chromium", Version: fmt.Sprintf("%d", randomInt(110, 130))},
				{Brand: "Google Chrome", Version: fmt.Sprintf("%d", randomInt(110, 130))},
				{Brand: "Not/A)Brand", Version: "24"},
			},
			PixelRatio:          []float64{1, 1.5, 2, 3}[randomInt(0, 3)],
			ScreenWidth:         screenW,
			ScreenHeight:        screenH,
			ColorDepth:          []int{24, 30}[randomInt(0, 1)],
			Languages:           languages,
			Timezone:            timezone,
			HardwareConcurrency: concurrency,
			DeviceMemory:        memory,
			TouchPoints:         touch,
			WebglVendor:         vendor,
			WebglRenderer:       renderer,
			CanvasHash:          canvasHash,
			AudioHash:           audioHash,
			WebglParamsHash:     webglParamsHash,
			FontsHash:           fontsHash,
			CodecsHash:          codecsHash,
			MediaDevices:        mediaDevices,
			PointerType:         pointerType,
			Extra: struct {
				Vendor     string `json:"vendor"`
				AppVersion string `json:"appVersion"`
			}{
				Vendor:     extraVendor,
				AppVersion: appVersion,
			},
		},
	}
}

func (f *Filemoon) Names() []string {
	return []string{"Filemoon", "MoonF", "Byse"}
}

func (f *Filemoon) SupportedFrom() SupportedFrom {
	return SupportedFromUrl | SupportedFromSource
}

func (f *Filemoon) SupportsUrl(urlStr string) bool {
	_, _, err := parseFilemoonMediaURL(urlStr)
	return err == nil
}

func (f *Filemoon) ExtractVideoUrl(ctx context.Context, from ExtractFrom) (*ExtractedVideo, error) {
	if from.Url == "" {
		return nil, errors.New("Filemoon: URL is required")
	}

	host, mediaID, err := parseFilemoonMediaURL(from.Url)
	if err != nil {
		return nil, err
	}
	return f.loadByseMediaURL(ctx, host, mediaID)
}

func (f *Filemoon) loadByseMediaURL(ctx context.Context, host, mediaID string) (*ExtractedVideo, error) {
	client, err := newFilemoonHTTPClient()
	if err != nil {
		return nil, err
	}

	webURL := getFilemoonBaseURL(host, mediaID)
	rootURL := filemoonRootURL(webURL)
	profile := generateRandomProfile()
	headers, err := filemoonRequestHeaders(webURL, rootURL, profile)
	if err != nil {
		return nil, err
	}

	challengeURL := rootURL + "api/videos/access/challenge"
	challenge, err := f.postForm(ctx, client, challengeURL, headers, url.Values{})
	if err != nil {
		return nil, fmt.Errorf("challenge request: %w", err)
	}

	attestBody, err := filemoonWebAuthn(challenge, profile)
	if err != nil {
		return nil, fmt.Errorf("attestation payload: %w", err)
	}
	attestURL := rootURL + "api/videos/access/attest"
	attest, err := f.postJSON(ctx, client, attestURL, headers, attestBody)
	if err != nil {
		return nil, fmt.Errorf("attestation request: %w", err)
	}

	fingerprint, err := filemoonFingerprintFromAttest(attest)
	if err != nil {
		return nil, fmt.Errorf("attestation response: %w", err)
	}
	if err := filemoonSetFingerprintCookies(client.Jar, rootURL, fingerprint); err != nil {
		return nil, fmt.Errorf("attestation cookies: %w", err)
	}

	captchaURL := fmt.Sprintf("%sapi/videos/%s/embed/captcha", rootURL, mediaID)
	captcha, err := f.postJSON(ctx, client, captchaURL, headers, filemoonFingerprintRequestBody(fingerprint))
	if err != nil {
		return nil, fmt.Errorf("captcha request: %w", err)
	}

	powNonce, err := filemoonStringField(captcha, "pow_nonce")
	if err != nil {
		return nil, fmt.Errorf("captcha response: %w", err)
	}
	powDifficulty, err := filemoonIntField(captcha, "pow_difficulty")
	if err != nil {
		return nil, fmt.Errorf("captcha response: %w", err)
	}
	powToken, err := filemoonStringField(captcha, "pow_token")
	if err != nil {
		return nil, fmt.Errorf("captcha response: %w", err)
	}
	slog.Debug("filemoon pow:", "powDifficulty", powDifficulty, "pow_token", powToken, "pow_nonce", powNonce)
	solution := solveFilemoonPoW(powNonce, powDifficulty, filemoonPoWTimeout.Seconds())
	if solution == "" {
		return nil, errors.New("filemoon: unable to solve captcha proof-of-work")
	}

	verifyURL := fmt.Sprintf("%sapi/videos/%s/embed/captcha/verify", rootURL, mediaID)
	verify, err := f.postJSON(ctx, client, verifyURL, headers, map[string]any{
		"pow_token":   powToken,
		"solution":    solution,
		"fingerprint": fingerprint,
	})
	if err != nil {
		return nil, fmt.Errorf("captcha verify request: %w", err)
	}
	verifyToken, err := filemoonVerifyToken(verify)
	if err != nil {
		return nil, fmt.Errorf("captcha verify response: %w", err)
	}
	headers["X-Captcha-Token"] = verifyToken

	playbackURL := fmt.Sprintf("%sapi/videos/%s/embed/playback", rootURL, mediaID)
	playback, err := f.postJSON(ctx, client, playbackURL, headers, filemoonFingerprintRequestBody(fingerprint))
	if err != nil {
		return nil, fmt.Errorf("playback request: %w", err)
	}

	video, err := extractVideoFromPlaybackResponse(playback, headers, rootURL)
	if err != nil {
		return nil, err
	}
	slog.Debug("Filemoon playback URL resolved", "media_id", mediaID, "url", video.Url)
	return video, nil
}

var filemoonURLPattern = regexp.MustCompile(
	`(?i)(?://|\.)(` +
		`(?:filemoon|cinegrab|moonmov|kerapoxy|furher|1azayf9w|81u6xl9d|f16px|embedplaybyse|` +
		`smdfs40r|bf0skv|z1ekv717|l1afav|222i8x|8mhlloqo|96ar|xcoic|f51rm|c1z39|boosteradx|streamlyplayer|vepoin|bysevepoin|` +
		`byse(?:sayeveum|tayico|zejataos|koze|sukior|jikuar|fujedu|dikamoum|buho|wihe|lapuix)?)` +
		`\.(?:sx|top?|s?k?in|link|nl|wf|com|eu|art|pro|cc|xyz|org|fun|net|lol|online)` +
		`)/(?:(?:e|d|download)/)?([0-9a-zA-Z]+)`,
)

func parseFilemoonMediaURL(rawURL string) (host, mediaID string, err error) {
	if _, err := url.Parse(rawURL); err != nil {
		return "", "", err
	}
	matches := filemoonURLPattern.FindStringSubmatch(rawURL)
	if len(matches) < 3 {
		return "", "", fmt.Errorf("filemoon: cannot extract media ID from %s", rawURL)
	}
	return strings.ToLower(matches[1]), matches[2], nil
}

func getFilemoonBaseURL(host, mediaID string) string {
	redirectDomains := map[string]string{
		"boosteradx.online": "streamlyplayer.online",
		"byse.sx":           "streamlyplayer.online",
	}
	host = strings.ToLower(host)
	if target, ok := redirectDomains[host]; ok {
		host = target
	}
	return fmt.Sprintf("https://%s/e/%s", host, mediaID)
}

func filemoonRootURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	u.Path = "/"
	u.RawPath = ""
	u.RawQuery = ""
	u.Fragment = ""
	return u.String()
}

func filemoonRequestHeaders(webURL, rootURL string, profile filemoonDeviceProfile) (map[string]string, error) {
	parsed, err := url.Parse(webURL)
	if err != nil {
		return nil, err
	}

	headers := map[string]string{
		"Accept":          "application/json, text/plain, */*",
		"Origin":          strings.TrimRight(rootURL, "/"),
		"Referer":         webURL,
		"X-Embed-Origin":  parsed.Host,
		"X-Embed-Referer": webURL,
		"X-Embed-Parent":  webURL,
	}
	filemoonApplyDeviceHeaders(headers, profile)
	return headers, nil
}

func filemoonApplyDeviceHeaders(headers map[string]string, profile filemoonDeviceProfile) {
	ua := strings.TrimSpace(profile.UA)
	if ua == "" {
		ua = filemoonDefaultUA
	}

	headers["User-Agent"] = ua
	headers["Accept"] = "application/json, text/plain, */*"
	headers["Accept-Language"] = filemoonAcceptLanguage(profile)
	headers["Sec-CH-UA-Mobile"] = "?0"
	if strings.Contains(ua, " Mobile ") || filemoonClientStringDefault(profile.Client, "platform", "") == "Android" {
		headers["Sec-CH-UA-Mobile"] = "?1"
	}
	if value := filemoonSecCHUA(profile.Client, false); value != "" {
		headers["Sec-CH-UA"] = value
	}
	if value := filemoonSecCHUA(profile.Client, true); value != "" {
		headers["Sec-CH-UA-Full-Version-List"] = value
	}
	for header, field := range map[string]string{
		"Sec-CH-UA-Arch":             "architecture",
		"Sec-CH-UA-Bitness":          "bitness",
		"Sec-CH-UA-Model":            "model",
		"Sec-CH-UA-Platform":         "platform",
		"Sec-CH-UA-Platform-Version": "platform_version",
	} {
		headers[header] = strconv.Quote(filemoonClientStringDefault(profile.Client, field, ""))
	}
}

func filemoonAcceptLanguage(profile filemoonDeviceProfile) string {
	languages := profile.Client.Languages
	if len(languages) == 0 {
		return "en-US,en;q=0.9"
	}

	parts := make([]string, 0, len(languages))
	for i, language := range languages {
		if i == 0 {
			parts = append(parts, language)
			continue
		}
		q := 1.0 - float64(i)*0.1
		if q < 0.1 {
			q = 0.1
		}
		parts = append(parts, fmt.Sprintf("%s;q=%.1f", language, q))
	}
	return strings.Join(parts, ",")
}

func filemoonClientStringDefault(client clientOutput, name string, fallback string) string {
	if client.UaFullVersion != "" {
		return client.UaFullVersion
	}
	return fallback
}

func filemoonSecCHUA(client clientOutput, fullVersion bool) string {
	rawVersions := client.BrandFullVersions

	parts := make([]string, 0, len(rawVersions))
	for _, rawVersion := range rawVersions {

		brand := rawVersion.Brand
		version := rawVersion.Version
		if brand == "" || version == "" {
			continue
		}
		if !fullVersion {
			version = strings.SplitN(version, ".", 2)[0]
		}
		parts = append(parts, fmt.Sprintf("%q;v=%q", brand, version))
	}
	return strings.Join(parts, ", ")
}

func filemoonFingerprintRequestBody(fingerprint map[string]any) map[string]any {
	return map[string]any{
		"fingerprint": fingerprint,
	}
}

func newFilemoonHTTPClient() (*http.Client, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, fmt.Errorf("filemoon: creating cookie jar: %w", err)
	}
	return &http.Client{Jar: jar}, nil
}

func filemoonSetFingerprintCookies(jar http.CookieJar, rootURL string, fingerprint map[string]any) error {
	if jar == nil {
		return nil
	}

	viewerID, err := filemoonStringField(fingerprint, "viewer_id")
	if err != nil {
		return err
	}
	deviceID, err := filemoonStringField(fingerprint, "device_id")
	if err != nil {
		return err
	}
	parsedRootURL, err := url.Parse(rootURL)
	if err != nil {
		return err
	}

	jar.SetCookies(parsedRootURL, []*http.Cookie{
		{Name: "byse_viewer_id", Value: viewerID, Path: "/"},
		{Name: "byse_device_id", Value: deviceID, Path: "/"},
	})
	return nil
}

func filemoonFingerprintFromAttest(attest map[string]any) (map[string]any, error) {
	token, err := filemoonStringField(attest, "token")
	if err != nil {
		return nil, err
	}
	viewerID, err := filemoonStringField(attest, "viewer_id")
	if err != nil {
		return nil, err
	}
	deviceID, err := filemoonStringField(attest, "device_id")
	if err != nil {
		return nil, err
	}
	confidence, ok := attest["confidence"]
	if !ok {
		return nil, fmt.Errorf("missing %q in response keys %v", "confidence", filemoonMapKeys(attest))
	}
	return map[string]any{
		"token":      token,
		"viewer_id":  viewerID,
		"device_id":  deviceID,
		"confidence": confidence,
	}, nil
}

func (f *Filemoon) postForm(ctx context.Context, client *http.Client, endpoint string, headers map[string]string, form url.Values) (map[string]any, error) {
	return f.doJSON(ctx, client, http.MethodPost, endpoint, headers, "application/x-www-form-urlencoded", strings.NewReader(form.Encode()))
}

func (f *Filemoon) postJSON(ctx context.Context, client *http.Client, endpoint string, headers map[string]string, body any) (map[string]any, error) {
	data, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	return f.doJSON(ctx, client, http.MethodPost, endpoint, headers, "application/json", bytes.NewReader(data))
}

func (f *Filemoon) doJSON(ctx context.Context, client *http.Client, method, endpoint string, headers map[string]string, contentType string, body io.Reader) (map[string]any, error) {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, body)
	if err != nil {
		return nil, err
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}

	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("http %d: %s", resp.StatusCode, filemoonBodyPreview(respBody))
	}

	var out map[string]any
	decoder := json.NewDecoder(bytes.NewReader(respBody))
	decoder.UseNumber()
	if err := decoder.Decode(&out); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", endpoint, err)
	}
	return out, nil
}

func filemoonBodyPreview(body []byte) string {
	body = bytes.TrimSpace(body)
	if len(body) > 512 {
		body = body[:512]
	}
	return string(body)
}

func filemoonStringField(data map[string]any, name string) (string, error) {
	value, ok := data[name].(string)
	if !ok || value == "" {
		return "", fmt.Errorf("missing %q in response keys %v", name, filemoonMapKeys(data))
	}
	return value, nil
}

func filemoonVerifyToken(data map[string]any) (string, error) {
	token, ok := data["token"].(string)
	if ok && token != "" {
		return token, nil
	}

	status, _ := data["status"].(string)
	reason, _ := data["reason"].(string)
	if status != "" || reason != "" {
		return "", fmt.Errorf("missing %q in response status=%q reason=%q keys %v", "token", status, reason, filemoonMapKeys(data))
	}
	return "", fmt.Errorf("missing %q in response keys %v", "token", filemoonMapKeys(data))
}

func filemoonIntField(data map[string]any, name string) (int, error) {
	switch value := data[name].(type) {
	case float64:
		return int(value), nil
	case int:
		return value, nil
	case json.Number:
		n, err := value.Int64()
		if err != nil {
			return 0, fmt.Errorf("invalid %q in response keys %v", name, filemoonMapKeys(data))
		}
		return int(n), nil
	default:
		return 0, fmt.Errorf("missing %q in response keys %v", name, filemoonMapKeys(data))
	}
}

func filemoonMapKeys(data map[string]any) []string {
	keys := make([]string, 0, len(data))
	for key := range data {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

// ---------------------------------------------------------------------------
// ported from https://github.com/icarok99/script.module.resolveurl
// The code below is licensed under GPL and is NOT MIT licensed.
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU General Public License for more details.
//
// You should have received a copy of the GNU General Public License
// along with this program. If not, see <http://www.gnu.org/licenses/>.
// ---------------------------------------------------------------------------

func filemoonWebAuthn(challenge map[string]any, profile filemoonDeviceProfile) (map[string]any, error) {
	nonce, err := filemoonStringField(challenge, "nonce")
	if err != nil {
		return nil, err
	}
	challengeID, err := filemoonStringField(challenge, "challenge_id")
	if err != nil {
		return nil, err
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	hash := sha256.Sum256([]byte(nonce))
	r, s, err := ecdsa.Sign(rand.Reader, key, hash[:])
	if err != nil {
		return nil, err
	}
	signatureBytes := make([]byte, 64)
	r.FillBytes(signatureBytes[:32])
	s.FillBytes(signatureBytes[32:])

	x := key.PublicKey.X.Bytes()
	y := key.PublicKey.Y.Bytes()
	paddedX := make([]byte, 32)
	paddedY := make([]byte, 32)
	copy(paddedX[32-len(x):], x)
	copy(paddedY[32-len(y):], y)

	client := profile.Client

	return map[string]any{
		"viewer_id":    "",
		"device_id":    "",
		"challenge_id": challengeID,
		"nonce":        nonce,
		"signature":    base64.RawURLEncoding.EncodeToString(signatureBytes),
		"public_key": map[string]any{
			"alg":     "ES256",
			"crv":     "P-256",
			"ext":     true,
			"key_ops": []string{"verify"},
			"kty":     "EC",
			"x":       base64.RawURLEncoding.EncodeToString(paddedX),
			"y":       base64.RawURLEncoding.EncodeToString(paddedY),
		},
		"client":     client,
		"storage":    map[string]any{},
		"attributes": map[string]any{"entropy": "high"},
	}, nil
}
func solveFilemoonPoW(nonce string, difficulty int, timeoutSec float64) string {
	if difficulty <= 0 {
		return "0"
	}

	prefix := []byte(nonce)
	prefix = append(prefix, ':')

	deadline := time.Now().Add(time.Duration(timeoutSec * float64(time.Second)))

	var scratch [filemoonPoWBlockWords]uint32
	var out [filemoonPoWOutputWords]uint32

	buf := make([]byte, len(prefix), len(prefix)+20)
	copy(buf, prefix)

	for i := int64(0); ; i++ {
		candidate := strconv.AppendInt(buf[:len(prefix)], i, 10)

		if difficulty <= 32 {
			firstWord := filemoonCustomHashFirstWord(candidate, &scratch)
			if bits.LeadingZeros32(firstWord) >= difficulty {
				return strconv.FormatInt(i, 10)
			}
		} else {
			filemoonCustomHashBytesInto(candidate, &scratch, out[:])
			if countLeadingZeroBits(out[:]) >= difficulty {
				return strconv.FormatInt(i, 10)
			}
		}

		if i&1023 == 1023 && time.Now().After(deadline) {
			return ""
		}
	}
}

func filemoonPoWLeadingZeroBits(input string) int {
	var scratch [filemoonPoWBlockWords]uint32
	var out [filemoonPoWOutputWords]uint32

	filemoonCustomHashStringInto(input, &scratch, out[:])
	return countLeadingZeroBits(out[:])
}

func filemoonPoWHash(input string) []uint32 {
	var scratch [filemoonPoWBlockWords]uint32
	out := make([]uint32, filemoonPoWOutputWords)

	filemoonCustomHashStringInto(input, &scratch, out)
	return out
}

func filemoonCustomHash(data []byte) []uint32 {
	var scratch [filemoonPoWBlockWords]uint32
	out := make([]uint32, filemoonPoWOutputWords)

	filemoonCustomHashBytesInto(data, &scratch, out)
	return out
}

const (
	filemoonPoWBlockWords  = 512
	filemoonPoWOutputWords = 8
	filemoonPoWWordMask    = filemoonPoWBlockWords - 1
	filemoonPoWMulA        = uint32(2654435761)
	filemoonPoWMulB        = uint32(2246822519)
)

func filemoonCustomHashFirstWord(data []byte, scratch *[filemoonPoWBlockWords]uint32) uint32 {
	state := filemoonInitialPoWState()

	for _, b := range data {
		state[0] += uint32(b)
		state[0] = bits.RotateLeft32(state[0], 7)
		filemoonMix(&state)
	}

	return filemoonCustomHashFinalizeFirstWord(&state, scratch)
}

func filemoonCustomHashBytesInto(data []byte, scratch *[filemoonPoWBlockWords]uint32, out []uint32) {
	state := filemoonInitialPoWState()

	for _, b := range data {
		state[0] += uint32(b)
		state[0] = bits.RotateLeft32(state[0], 7)
		filemoonMix(&state)
	}

	filemoonCustomHashFinalizeInto(&state, scratch, out)
}

func filemoonCustomHashStringInto(input string, scratch *[filemoonPoWBlockWords]uint32, out []uint32) {
	state := filemoonInitialPoWState()

	for i := 0; i < len(input); i++ {
		state[0] += uint32(input[i])
		state[0] = bits.RotateLeft32(state[0], 7)
		filemoonMix(&state)
	}

	filemoonCustomHashFinalizeInto(&state, scratch, out)
}

func filemoonInitialPoWState() [4]uint32 {
	return [4]uint32{1779033703, 3144134277, 1013904242, 2773480762}
}

func filemoonCustomHashFinalizeFirstWord(state *[4]uint32, scratch *[filemoonPoWBlockWords]uint32) uint32 {
	filemoonPreparePoWScratch(state, scratch)

	filemoonMix(state)

	s := state[0]
	for _, d := range scratch[:filemoonPoWBlockWords/filemoonPoWOutputWords] {
		s += d
		s = bits.RotateLeft32(s, 5)
		s ^= d * filemoonPoWMulB
	}

	return s ^ state[2]
}

func filemoonCustomHashFinalizeInto(state *[4]uint32, scratch *[filemoonPoWBlockWords]uint32, out []uint32) {
	filemoonPreparePoWScratch(state, scratch)

	if len(out) > filemoonPoWOutputWords {
		out = out[:filemoonPoWOutputWords]
	}

	chunkSize := filemoonPoWBlockWords / filemoonPoWOutputWords
	for i := range out {
		filemoonMix(state)

		s := state[0]
		offset := i * chunkSize

		for _, d := range scratch[offset : offset+chunkSize] {
			s += d
			s = bits.RotateLeft32(s, 5)
			s ^= d * filemoonPoWMulB
		}

		out[i] = s ^ state[2]
	}
}

func filemoonPreparePoWScratch(state *[4]uint32, scratch *[filemoonPoWBlockWords]uint32) {
	for i := 0; i < 8; i++ {
		filemoonMix(state)
	}

	for i := range scratch {
		filemoonMix(state)
		scratch[i] = state[0] ^ state[2]
	}

	for pass := 0; pass < 2; pass++ {
		for i := range scratch {
			a := scratch[i] & filemoonPoWWordMask
			c := scratch[i] + scratch[a]
			c = bits.RotateLeft32(c, 13)
			c ^= scratch[(i+1)&filemoonPoWWordMask] * filemoonPoWMulA
			scratch[i] = c
			state[0] ^= c
			filemoonMix(state)
		}
	}
}

func filemoonMix(state *[4]uint32) {
	state[0] += state[1]
	state[3] = bits.RotateLeft32(state[3]^state[0], 16)
	state[2] += state[3]
	state[1] = bits.RotateLeft32(state[1]^state[2], 12)
	state[0] += state[1]
	state[3] = bits.RotateLeft32(state[3]^state[0], 8)
	state[2] += state[3]
	state[1] = bits.RotateLeft32(state[1]^state[2], 7)
}

func countLeadingZeroBitsBytes(data []byte) int {
	total := 0
	for _, b := range data {
		if b == 0 {
			total += 8
			continue
		}
		return total + bits.LeadingZeros8(b)
	}
	return total
}

func countLeadingZeroBits(state []uint32) int {
	total := 0
	for _, word := range state {
		if word == 0 {
			total += 32
			continue
		}
		return total + bits.LeadingZeros32(word)
	}
	return total
}

type EncryptedPayload struct {
	Algorithm string   `json:"algorithm"`
	IV        string   `json:"iv"`
	Payload   string   `json:"payload"`
	KeyParts  []string `json:"key_parts"`
	Version   string   `json:"version"`
	ExpiresAt string   `json:"expires_at"`
}

type Input struct {
	Playback    EncryptedPayload `json:"playback"`
	CacheStatus string           `json:"cache_status"`
}

type filemoonPlaybackPayload struct {
	Sources []filemoonPlaybackSource `json:"sources"`
}

type filemoonPlaybackSource struct {
	URL      string `json:"url"`
	MimeType string `json:"mime_type"`
	Label    string `json:"label"`
}

func extractFilemoonPlaybackM3U8(body []byte) (string, error) {
	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		return "", fmt.Errorf("unmarshaling playback response: %w", err)
	}
	source, err := extractFilemoonPlaybackSource(data, "")
	if err != nil {
		return "", err
	}
	return source.URL, nil
}

func extractVideoFromPlaybackResponse(data map[string]any, headers map[string]string, baseURL string) (*ExtractedVideo, error) {
	source, err := extractFilemoonPlaybackSource(data, baseURL)
	if err != nil {
		return nil, err
	}

	return &ExtractedVideo{
		Url:       source.URL,
		Referer:   headers["Referer"],
		UserAgent: headers["User-Agent"],
		IsM3U8:    isM3U8URL(source.URL),
	}, nil
}

func extractFilemoonPlaybackSource(data map[string]any, baseURL string) (filemoonPlaybackSource, error) {
	if sourcesRaw, ok := data["sources"]; ok {
		sources, err := parseFilemoonSources(sourcesRaw)
		if err != nil {
			return filemoonPlaybackSource{}, err
		}
		if source, ok := pickFilemoonPlaybackSource(sources, baseURL); ok {
			return source, nil
		}
	}

	playbackRaw, ok := data["playback"]
	if !ok {
		return filemoonPlaybackSource{}, errors.New("playback response did not include sources or encrypted playback")
	}

	playbackBytes, err := json.Marshal(playbackRaw)
	if err != nil {
		return filemoonPlaybackSource{}, err
	}
	var encrypted EncryptedPayload
	if err := json.Unmarshal(playbackBytes, &encrypted); err != nil {
		return filemoonPlaybackSource{}, err
	}
	if encrypted.Payload == "" {
		return filemoonPlaybackSource{}, errors.New("playback response did not include encrypted payload")
	}

	var payload filemoonPlaybackPayload
	if err := decryptPayload(encrypted, &payload); err != nil {
		return filemoonPlaybackSource{}, err
	}
	if source, ok := pickFilemoonPlaybackSource(payload.Sources, baseURL); ok {
		return source, nil
	}
	return filemoonPlaybackSource{}, errors.New("playback payload did not include an m3u8 source")
}

func parseFilemoonSources(raw any) ([]filemoonPlaybackSource, error) {
	data, err := json.Marshal(raw)
	if err != nil {
		return nil, err
	}
	var sources []filemoonPlaybackSource
	if err := json.Unmarshal(data, &sources); err != nil {
		return nil, err
	}
	return sources, nil
}

func pickFilemoonPlaybackSource(sources []filemoonPlaybackSource, baseURL string) (filemoonPlaybackSource, bool) {
	for _, source := range sources {
		if source.URL == "" {
			continue
		}
		if !isM3U8URL(source.URL) && !strings.EqualFold(source.MimeType, "application/vnd.apple.mpegurl") {
			continue
		}
		source.URL = filemoonResolveURL(baseURL, source.URL)
		return source, true
	}
	return filemoonPlaybackSource{}, false
}

func filemoonResolveURL(baseURL, rawURL string) string {
	if baseURL == "" {
		return rawURL
	}
	u, err := url.Parse(rawURL)
	if err != nil || u.IsAbs() {
		return rawURL
	}
	base, err := url.Parse(baseURL)
	if err != nil {
		return rawURL
	}
	return base.ResolveReference(u).String()
}

func isM3U8URL(rawURL string) bool {
	return strings.Contains(strings.ToLower(rawURL), ".m3u8")
}

func selectKeyIndices(version string, partCount int) (int, int, bool) {
	if version == "" {
		return 0, 0, false
	}
	n, err := strconv.Atoi(version)
	if err != nil || n < 1 || n > partCount {
		return 0, 0, false
	}
	return n, partCount - n + 1, true
}

func assembleKey(ep EncryptedPayload) ([]byte, error) {
	parts := ep.KeyParts
	if len(parts) == 0 {
		return nil, errors.New("no key parts")
	}

	var selectedParts []string
	if a, b, ok := selectKeyIndices(ep.Version, len(parts)); ok {
		if p := parts[a-1]; p != "" {
			selectedParts = append(selectedParts, p)
		}
		if p := parts[b-1]; p != "" {
			selectedParts = append(selectedParts, p)
		}
	}

	if len(selectedParts) == 0 {
		for _, p := range parts {
			if p != "" {
				selectedParts = append(selectedParts, p)
			}
		}
	}

	var key []byte
	for _, p := range selectedParts {
		decoded, err := decodeBase64Flex(p)
		if err != nil {
			return nil, fmt.Errorf("decoding key part: %w", err)
		}
		key = append(key, decoded...)
	}
	return key, nil
}

func decodeBase64Flex(s string) ([]byte, error) {
	s = strings.ReplaceAll(s, "-", "+")
	s = strings.ReplaceAll(s, "_", "/")
	if pad := len(s) % 4; pad != 0 {
		s += strings.Repeat("=", 4-pad)
	}
	return base64.StdEncoding.DecodeString(s)
}

func decryptPayload(ep EncryptedPayload, dst any) error {
	key, err := assembleKey(ep)
	if err != nil {
		return fmt.Errorf("assembling key: %w", err)
	}

	iv, err := decodeBase64Flex(ep.IV)
	if err != nil {
		return fmt.Errorf("decoding iv: %w", err)
	}

	ciphertext, err := decodeBase64Flex(ep.Payload)
	if err != nil {
		return fmt.Errorf("decoding payload: %w", err)
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return fmt.Errorf("creating cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return fmt.Errorf("creating GCM: %w", err)
	}

	plaintext, err := gcm.Open(nil, iv, ciphertext, nil)
	if err != nil {
		return fmt.Errorf("decryption failed: %w", err)
	}

	return json.Unmarshal(plaintext, dst)
}

func init() {
	Register(&Filemoon{})
}
