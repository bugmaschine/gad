package download

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/vbauerster/mpb/v8"
	"golang.org/x/time/rate"
)

func TestSimpleDownloadCommitsPartFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("video bytes"))
	}))
	defer server.Close()

	d := newTestDownloader(t, server.Client())
	outputPath := filepath.Join(t.TempDir(), "video.bin")
	task := NewDownloadTask(outputPath, server.URL)
	task.OutputPathHasExtension = true

	if err := d.DownloadToFile(context.Background(), task); err != nil {
		t.Fatalf("DownloadToFile returned error: %v", err)
	}

	got, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("reading output: %v", err)
	}
	if string(got) != "video bytes" {
		t.Fatalf("unexpected output: %q", got)
	}

	matches, err := filepath.Glob(filepath.Join(filepath.Dir(outputPath), "."+filepath.Base(outputPath)+".*.part"))
	if err != nil {
		t.Fatalf("glob part files: %v", err)
	}
	if len(matches) != 0 {
		t.Fatalf("leftover part files: %v", matches)
	}
}

func TestNewDownloaderUsesHTTP1TransportForParallelHLS(t *testing.T) {
	d := NewDownloader("", false, 0)
	t.Cleanup(d.Shutdown)

	transport, ok := d.client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected *http.Transport, got %T", d.client.Transport)
	}
	if transport.ForceAttemptHTTP2 {
		t.Fatal("expected HTTP/2 to be disabled for parallel HLS segment connections")
	}
	if transport.MaxIdleConnsPerHost < hlsParallelSegments {
		t.Fatalf("MaxIdleConnsPerHost = %d, want at least %d", transport.MaxIdleConnsPerHost, hlsParallelSegments)
	}

	fallbackTransport, ok := d.fallbackClient.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("expected fallback *http.Transport, got %T", d.fallbackClient.Transport)
	}
	if !fallbackTransport.ForceAttemptHTTP2 {
		t.Fatal("expected fallback transport to support HTTP/2")
	}
}

func TestHTTP1MalformedHTTP2ErrorDetection(t *testing.T) {
	err := errors.New(`Get "https://example.com/master.m3u8": net/http: HTTP/1.x transport connection broken: malformed HTTP response "\x00\x00\x12\x04"`)
	if !isHTTP1MalformedHTTP2Error(err) {
		t.Fatal("expected malformed HTTP/2-over-HTTP/1 error to be detected")
	}
	if isHTTP1MalformedHTTP2Error(errors.New("unexpected EOF")) {
		t.Fatal("unexpected EOF should not trigger HTTP/2 fallback detection")
	}
}

func TestSimpleDownloadRunsCompletionHookAfterCommit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("video bytes"))
	}))
	defer server.Close()

	d := newTestDownloader(t, server.Client())
	outputPath := filepath.Join(t.TempDir(), "video.bin")
	var hookCalls atomic.Int32
	task := NewDownloadTask(outputPath, server.URL).
		SetOnComplete(func(ctx context.Context) error {
			if _, err := os.Stat(outputPath); err != nil {
				t.Fatalf("completion hook ran before output existed: %v", err)
			}
			hookCalls.Add(1)
			return nil
		})
	task.OutputPathHasExtension = true

	if err := d.DownloadToFile(context.Background(), task); err != nil {
		t.Fatalf("DownloadToFile returned error: %v", err)
	}
	if hookCalls.Load() != 1 {
		t.Fatalf("expected one completion hook call, got %d", hookCalls.Load())
	}
}

func TestSimpleDownloadStopsStartHookAfterDownload(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("video bytes"))
	}))
	defer server.Close()

	d := newTestDownloader(t, server.Client())
	outputPath := filepath.Join(t.TempDir(), "video.bin")
	var starts atomic.Int32
	var stops atomic.Int32
	task := NewDownloadTask(outputPath, server.URL).
		SetOnStart(func(ctx context.Context) (func(), error) {
			starts.Add(1)
			return func() {
				stops.Add(1)
			}, nil
		})
	task.OutputPathHasExtension = true

	if err := d.DownloadToFile(context.Background(), task); err != nil {
		t.Fatalf("DownloadToFile returned error: %v", err)
	}
	if starts.Load() != 1 {
		t.Fatalf("expected one start hook call, got %d", starts.Load())
	}
	if stops.Load() != 1 {
		t.Fatalf("expected one stop hook call, got %d", stops.Load())
	}
}

func TestHLSegmentRetry(t *testing.T) {
	var segmentHits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/playlist.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			w.Write([]byte("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:1\n#EXT-X-MEDIA-SEQUENCE:0\n#EXTINF:1.0,\nseg.ts\n#EXT-X-ENDLIST\n"))
		case "/seg.ts":
			if segmentHits.Add(1) == 1 {
				http.Error(w, "temporary failure", http.StatusInternalServerError)
				return
			}
			w.Write([]byte("segment"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	d := newTestDownloader(t, server.Client())
	outputPath := filepath.Join(t.TempDir(), "video.ts")
	task := NewDownloadTask(outputPath, server.URL+"/playlist.m3u8")
	task.OutputPathHasExtension = true

	if err := d.DownloadToFile(context.Background(), task); err != nil {
		t.Fatalf("DownloadToFile returned error: %v", err)
	}
	if segmentHits.Load() != 2 {
		t.Fatalf("expected one retry, got %d segment requests", segmentHits.Load())
	}

	got, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("reading output: %v", err)
	}
	if string(got) != "segment" {
		t.Fatalf("unexpected output: %q", got)
	}
}

func TestHLSRequestsUseTaskUserAgent(t *testing.T) {
	const taskUserAgent = "filemoon-profile-agent"

	var badUserAgents atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != taskUserAgent {
			badUserAgents.Add(1)
		}
		switch r.URL.Path {
		case "/playlist.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			w.Write([]byte("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:1\n#EXT-X-MEDIA-SEQUENCE:0\n#EXTINF:1.0,\nseg.ts\n#EXT-X-ENDLIST\n"))
		case "/seg.ts":
			w.Write([]byte("segment"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	d := newTestDownloader(t, server.Client())
	d.userAgent = "default-agent"
	outputPath := filepath.Join(t.TempDir(), "video.ts")
	task := NewDownloadTask(outputPath, server.URL+"/playlist.m3u8").
		SetUserAgent(taskUserAgent)
	task.OutputPathHasExtension = true

	if err := d.DownloadToFile(context.Background(), task); err != nil {
		t.Fatalf("DownloadToFile returned error: %v", err)
	}
	if badUserAgents.Load() != 0 {
		t.Fatalf("expected all HLS requests to use task user-agent, saw %d mismatches", badUserAgents.Load())
	}
}

func TestHLSegmentReadFailureRetries(t *testing.T) {
	var segmentHits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/playlist.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			w.Write([]byte("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:1\n#EXT-X-MEDIA-SEQUENCE:0\n#EXTINF:1.0,\nseg.ts\n#EXT-X-ENDLIST\n"))
		case "/seg.ts":
			w.Header().Set("Content-Length", "12")
			if segmentHits.Add(1) == 1 {
				w.Write([]byte("partial"))
				return
			}
			w.Write([]byte("complete.seg"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	d := newTestDownloader(t, server.Client())
	outputPath := filepath.Join(t.TempDir(), "video.ts")
	task := NewDownloadTask(outputPath, server.URL+"/playlist.m3u8")
	task.OutputPathHasExtension = true

	if err := d.DownloadToFile(context.Background(), task); err != nil {
		t.Fatalf("DownloadToFile returned error: %v", err)
	}
	if segmentHits.Load() != 2 {
		t.Fatalf("expected one read retry, got %d segment requests", segmentHits.Load())
	}

	got, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("reading output: %v", err)
	}
	if string(got) != "complete.seg" {
		t.Fatalf("unexpected output: %q", got)
	}
}

func TestFetchHLSKeyCachesValidKeys(t *testing.T) {
	var keyHits atomic.Int32
	key := []byte("0123456789abcdef")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		keyHits.Add(1)
		w.Write(key)
	}))
	defer server.Close()

	d := newTestDownloader(t, server.Client())
	keyURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	cache := make(map[string][]byte)

	first, err := d.fetchHLSKey(context.Background(), keyURL, "", "", cache, false)
	if err != nil {
		t.Fatalf("first key fetch returned error: %v", err)
	}
	second, err := d.fetchHLSKey(context.Background(), keyURL, "", "", cache, false)
	if err != nil {
		t.Fatalf("cached key fetch returned error: %v", err)
	}
	if string(first) != string(key) || string(second) != string(key) {
		t.Fatalf("unexpected key values: %x %x", first, second)
	}
	if keyHits.Load() != 1 {
		t.Fatalf("expected one key request, got %d", keyHits.Load())
	}
}

func TestFetchHLSKeyRejectsInvalidLength(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("short"))
	}))
	defer server.Close()

	d := newTestDownloader(t, server.Client())
	keyURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	_, err = d.fetchHLSKey(context.Background(), keyURL, "", "", make(map[string][]byte), false)
	if err == nil {
		t.Fatal("expected invalid key length error")
	}
}

func TestFetchHLSKeyAllowsNilCacheOnForceRefresh(t *testing.T) {
	key := []byte("0123456789abcdef")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(key)
	}))
	defer server.Close()

	d := newTestDownloader(t, server.Client())
	keyURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}

	got, err := d.fetchHLSKey(context.Background(), keyURL, "", "", nil, true)
	if err != nil {
		t.Fatalf("forced key fetch returned error: %v", err)
	}
	if string(got) != string(key) {
		t.Fatalf("unexpected key: %x", got)
	}
}

func TestHLSRemuxFailureReturnsError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/playlist.m3u8":
			w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
			w.Write([]byte("#EXTM3U\n#EXT-X-VERSION:3\n#EXT-X-TARGETDURATION:1\n#EXT-X-MEDIA-SEQUENCE:0\n#EXTINF:1.0,\nseg.ts\n#EXT-X-ENDLIST\n"))
		case "/seg.ts":
			w.Write([]byte("segment"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	tempDir := t.TempDir()
	ffmpegPath := filepath.Join(tempDir, "ffmpeg-fail")
	if err := os.WriteFile(ffmpegPath, []byte("#!/bin/sh\nexit 7\n"), 0755); err != nil {
		t.Fatalf("writing ffmpeg stub: %v", err)
	}

	d := newTestDownloader(t, server.Client())
	d.SetFfmpegPath(ffmpegPath)
	outputBase := filepath.Join(tempDir, "video")

	err := d.DownloadToFile(context.Background(), NewDownloadTask(outputBase, server.URL+"/playlist.m3u8"))
	if err == nil || !strings.Contains(err.Error(), "ffmpeg remux failed") {
		t.Fatalf("expected remux failure, got %v", err)
	}
	if _, statErr := os.Stat(outputBase + ".mp4"); !os.IsNotExist(statErr) {
		t.Fatalf("final mp4 should not exist, stat error: %v", statErr)
	}
}

func TestRateLimitedReaderAllowsReadsLargerThanBurst(t *testing.T) {
	input := strings.Repeat("x", 4096)
	reader := &rateLimitedReader{
		r:       strings.NewReader(input),
		limiter: rate.NewLimiter(rate.Limit(1<<30), 1),
		ctx:     context.Background(),
	}

	got, err := io.ReadAll(reader)
	if err != nil {
		t.Fatalf("ReadAll returned error: %v", err)
	}
	if string(got) != input {
		t.Fatalf("unexpected output length: got %d want %d", len(got), len(input))
	}
}

func newTestDownloader(t *testing.T, client *http.Client) *Downloader {
	t.Helper()

	d := &Downloader{
		client:   client,
		progress: mpb.New(mpb.WithOutput(io.Discard)),
	}
	t.Cleanup(d.Shutdown)
	return d
}
