package extractors

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestVidmolyExtractVideoUrlSupportsSingleQuotedSource(t *testing.T) {
	source := `
		<script>
			jwplayer("vplayer").setup({
				sources: [{ file: 'https://example.com/master.m3u8?t=token' }]
			});
		</script>
	`

	got, err := (&Vidmoly{}).ExtractVideoUrl(context.Background(), ExtractFrom{
		Url:    "https://vidmoly.biz/embed-vmmb2cmh6we0.html",
		Source: source,
	})
	if err != nil {
		t.Fatalf("ExtractVideoUrl returned error: %v", err)
	}
	if got == nil {
		t.Fatal("ExtractVideoUrl returned nil")
	}
	if got.Url != "https://example.com/master.m3u8?t=token" {
		t.Fatalf("unexpected url: %q", got.Url)
	}
}

func TestVidmolyExtractVideoUrlSupportsDoubleQuotedSource(t *testing.T) {
	source := `file: "https://example.com/master.m3u8?t=token"`

	got, err := (&Vidmoly{}).ExtractVideoUrl(context.Background(), ExtractFrom{
		Url:    "https://vidmoly.biz/embed-vmmb2cmh6we0.html",
		Source: source,
	})
	if err != nil {
		t.Fatalf("ExtractVideoUrl returned error: %v", err)
	}
	if got == nil {
		t.Fatal("ExtractVideoUrl returned nil")
	}
	if got.Url != "https://example.com/master.m3u8?t=token" {
		t.Fatalf("unexpected url: %q", got.Url)
	}
}

func TestVidmolyExtractVideoUrlAddsStartHookWhenPingConfigExists(t *testing.T) {
	source := `
		file: 'https://example.com/master.m3u8'
		var mSid = 'sid';
		var mToken = 'token';
		var mFile = 'file';
		var mFileID = 'file-id';
		var mCd = 'content';
		var mUsr = 'user';
		var mEar = 'early';
		var mAsn = 'asn';
		var pingUrl = 'https://sw.vidmoly.me/v1/ping';
		var watchUrl = 'https://sw.vidmoly.me/v1/watch';
		setInterval(sendMolyPing, 20000);
		watchInterval = setInterval(sendWatchPing, 25000);
	`

	got, err := (&Vidmoly{}).ExtractVideoUrl(context.Background(), ExtractFrom{
		Url:    "https://vidmoly.biz/embed-vmmb2cmh6we0.html",
		Source: source,
	})
	if err != nil {
		t.Fatalf("ExtractVideoUrl returned error: %v", err)
	}
	if got.OnDownloadStart == nil {
		t.Fatal("expected start hook")
	}
}

func TestExtractPingConfigSupportsDeobfuscatedVariableNames(t *testing.T) {
	source := `
		duration: "1451",
		var sessionId = "sid";
		var authToken = "token";
		var fileCode = "file";
		var fileId = "file-id";
		var contentId = "content";
		var userId = "user";
		var earlyAccess = "early";
		var asn = "asn";
		var molyReferer = "https://vidmoly.biz/";
		var pingUrl = "https://ping.example/v1/ping";
		var watchUrl = "https://ping.example/v1/watch";
		setInterval(sendVerificationPing, 20000);
		watchInterval = setInterval(sendWatchPing, 25000);
	`

	got, err := extractPingConfig(source, "https://fallback.example/")
	if err != nil {
		t.Fatalf("extractPingConfig returned error: %v", err)
	}
	if got.SessionId != "sid" || got.Token != "token" || got.FileCode != "file" {
		t.Fatalf("unexpected required fields: %+v", got)
	}
	if got.FileId != "file-id" || got.ContentId != "content" || got.UserId != "user" {
		t.Fatalf("unexpected metadata fields: %+v", got)
	}
	if got.Duration != "1451" {
		t.Fatalf("unexpected duration: %q", got.Duration)
	}
	if got.Referer != "https://vidmoly.biz/" {
		t.Fatalf("unexpected referer: %q", got.Referer)
	}
	if got.PingInterval != 20 {
		t.Fatalf("unexpected interval: %d", got.PingInterval)
	}
	if got.WatchInterval != 25 {
		t.Fatalf("unexpected watch interval: %d", got.WatchInterval)
	}
	if got.PingUrl != "https://ping.example/v1/ping" {
		t.Fatalf("unexpected ping url: %q", got.PingUrl)
	}
	if got.WatchUrl != "https://ping.example/v1/watch" {
		t.Fatalf("unexpected watch url: %q", got.WatchUrl)
	}
}

func TestSendVidmolyPingUsesJsonPayload(t *testing.T) {
	var gotPayload map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Fatalf("unexpected content type: %q", r.Header.Get("Content-Type"))
		}
		if err := json.NewDecoder(r.Body).Decode(&gotPayload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"prog":100}`))
	}))
	defer server.Close()

	cfg := testVidmolyPingConfig()
	cfg.PingUrl = server.URL

	prog, err := sendVidmolyPing(context.Background(), cfg, time.Now().Add(-3*time.Second))
	if err != nil {
		t.Fatalf("sendVidmolyPing returned error: %v", err)
	}
	if prog != 100 {
		t.Fatalf("unexpected prog: %d", prog)
	}
	if gotPayload["sid"] != cfg.SessionId ||
		gotPayload["token"] != cfg.Token ||
		gotPayload["file_code"] != cfg.FileCode ||
		gotPayload["file_id"] != cfg.FileId ||
		gotPayload["cd"] != cfg.ContentId ||
		gotPayload["usr_id"] != cfg.UserId ||
		gotPayload["ear"] != cfg.EarlyAccess ||
		gotPayload["asn"] != cfg.Asn ||
		gotPayload["ref"] != cfg.Referer ||
		gotPayload["dur"] != cfg.Duration {
		t.Fatalf("unexpected payload: %+v", gotPayload)
	}
	if gotPayload["ab"] != "0" || gotPayload["dev"] != "Desktop" || gotPayload["ov"] != "0" {
		t.Fatalf("unexpected emulation fields: %+v", gotPayload)
	}
}

func TestVidmolyExtractVideoUrlUsesFinalRedirectURLForReferer(t *testing.T) {
	pageServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`file: 'https://example.com/master.m3u8'`))
	}))
	defer pageServer.Close()

	redirectServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, pageServer.URL+"/embed-video.html", http.StatusFound)
	}))
	defer redirectServer.Close()

	got, err := (&Vidmoly{}).ExtractVideoUrl(context.Background(), ExtractFrom{
		Url: redirectServer.URL + "/redirect/123",
	})
	if err != nil {
		t.Fatalf("ExtractVideoUrl returned error: %v", err)
	}

	wantReferer := pageServer.URL + "/"
	if got.Referer != wantReferer {
		t.Fatalf("unexpected referer: got %q want %q", got.Referer, wantReferer)
	}
}

func TestSendVidmolyWatchPingUsesQueryPayload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if r.URL.Query().Get("file_code") != "file" {
			t.Fatalf("unexpected file_code: %q", r.URL.Query().Get("file_code"))
		}
		if r.URL.Query().Get("sid") != "watch-session" {
			t.Fatalf("unexpected sid: %q", r.URL.Query().Get("sid"))
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	cfg := testVidmolyPingConfig()
	cfg.WatchUrl = server.URL

	if err := sendVidmolyWatchPing(context.Background(), cfg, "watch-session"); err != nil {
		t.Fatalf("sendVidmolyWatchPing returned error: %v", err)
	}
}

func testVidmolyPingConfig() *VidmolyPingConfig {
	return &VidmolyPingConfig{
		PingUrl:       "https://sw.vidmoly.me/v1/ping",
		WatchUrl:      "https://sw.vidmoly.me/v1/watch",
		PingInterval:  20,
		WatchInterval: 25,
		SessionId:     "sid",
		Token:         "token",
		FileCode:      "file",
		FileId:        "file-id",
		ContentId:     "content",
		UserId:        "user",
		EarlyAccess:   "early",
		Asn:           "asn",
		Referer:       "https://vidmoly.biz/",
		Duration:      "1451",
	}
}
