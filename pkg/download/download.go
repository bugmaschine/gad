package download

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"bugmaschine/gad/pkg/logger"

	"github.com/grafov/m3u8"
	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
	"golang.org/x/time/rate"
)

type Downloader struct {
	client         *http.Client
	fallbackClient *http.Client
	progress       *mpb.Progress
	totalBar       *mpb.Bar
	totalSize      int64
	limiter        *rate.Limiter
	userAgent      string
	ffmpegPath     string
	debug          bool
	mu             sync.Mutex
	finishOnce     sync.Once
	activeBars     int
	closed         bool
}

const (
	hlsProgressSegmentInterval = 10
	hlsProgressUpdateInterval  = 250 * time.Millisecond
	hlsRequestAttempts         = 3
	hlsRetryDelay              = 250 * time.Millisecond
	maxRateLimitBurst          = 1 << 20
	maxIdleConns               = 64
	maxIdleConnsPerHost        = 16
)

func NewDownloader(userAgent string, debug bool, limitRate float64) *Downloader {
	var rLimit *rate.Limiter
	if limitRate > 0 {
		rLimit = rate.NewLimiter(rate.Limit(limitRate), rateLimitBurst(limitRate))
	}

	p := mpb.New(
		mpb.WithOutput(os.Stdout),
		mpb.WithRefreshRate(50*time.Millisecond),
	)

	d := &Downloader{
		client:         newHTTPClient(),
		fallbackClient: newHTTP2FallbackClient(),
		progress:       p,
		limiter:        rLimit,
		userAgent:      userAgent,
		debug:          debug,
	}
	logger.SetWriter(d)
	return d
}

func newHTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ForceAttemptHTTP2 = false
	transport.MaxIdleConns = maxIdleConns
	transport.MaxIdleConnsPerHost = maxIdleConnsPerHost
	return &http.Client{Transport: transport}
}

func newHTTP2FallbackClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ForceAttemptHTTP2 = true
	transport.MaxIdleConns = maxIdleConns
	transport.MaxIdleConnsPerHost = maxIdleConnsPerHost
	return &http.Client{Transport: transport}
}

func (d *Downloader) SetFfmpegPath(path string) {
	d.ffmpegPath = path
}

func (d *Downloader) DownloadToFile(ctx context.Context, task *DownloadTask) error {
	slog.Debug("Starting download to file", "url", task.Url, "path", task.OutputPath)

	outputPath := task.OutputPath
	if !task.OutputPathHasExtension {
		outputPath += ".mp4"
	}

	if task.SkipExisting {
		if _, err := os.Stat(outputPath); err == nil {
			slogInfo("skipping download for %s: file already exists", filepath.Base(outputPath))
			return nil
		}
	}
	if !task.OverwriteFile {
		if _, err := os.Stat(outputPath); err == nil {
			return fmt.Errorf("file already exists: %s", outputPath)
		} else if !os.IsNotExist(err) {
			return err
		}
	}

	req, err := d.newRequest(ctx, task.Url, task.Referer, task.UserAgent)
	if err != nil {
		return err
	}

	resp, err := d.do(req)
	if err != nil {
		return err
	}
	slog.Debug("Got response", "status", resp.Status, "content-type", resp.Header.Get("Content-Type"))
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}

	stopDownloadHook, err := runDownloadStartHook(ctx, task)
	if err != nil {
		return err
	}
	defer stopDownloadHook()

	contentType := resp.Header.Get("Content-Type")
	isM3U8 := strings.Contains(strings.ToLower(resp.Request.URL.String()), ".m3u8") ||
		strings.Contains(strings.ToLower(contentType), "application/vnd.apple.mpegurl") ||
		strings.Contains(strings.ToLower(contentType), "application/x-mpegURL")

	message := task.CustomMessage
	if message == "" {
		message = filepath.Base(outputPath)
	}

	if isM3U8 {
		slog.Debug("Detected M3U8 playlist, starting HLS download")
		if err := d.m3u8Download(ctx, resp, task.Referer, task.UserAgent, outputPath, message, task.OverwriteFile); err != nil {
			return err
		}
		return runDownloadCompleteHook(ctx, task)
	}

	targetFile, partPath, err := createPartFile(outputPath)
	if err != nil {
		return err
	}
	partFileClosed := false
	committed := false
	defer func() {
		if !partFileClosed {
			_ = targetFile.Close()
		}
		if !committed {
			_ = os.Remove(partPath)
		}
	}()

	slog.Debug("Starting simple file download")
	if err := d.simpleDownload(ctx, resp, targetFile, message); err != nil {
		return err
	}
	if err := targetFile.Close(); err != nil {
		return err
	}
	partFileClosed = true
	if err := commitPartFile(partPath, outputPath, task.OverwriteFile); err != nil {
		return err
	}
	committed = true
	return runDownloadCompleteHook(ctx, task)
}

func runDownloadCompleteHook(ctx context.Context, task *DownloadTask) error {
	if task.OnComplete == nil {
		return nil
	}
	if err := task.OnComplete(ctx); err != nil {
		return fmt.Errorf("download completion hook failed: %w", err)
	}
	return nil
}

func runDownloadStartHook(ctx context.Context, task *DownloadTask) (func(), error) {
	if task.OnStart == nil {
		return func() {}, nil
	}
	stop, err := task.OnStart(ctx)
	if err != nil {
		return nil, fmt.Errorf("download start hook failed: %w", err)
	}
	if stop == nil {
		return func() {}, nil
	}
	return stop, nil
}

func createPartFile(outputPath string) (*os.File, string, error) {
	dir := filepath.Dir(outputPath)
	base := filepath.Base(outputPath)
	ext := filepath.Ext(base)
	name := strings.TrimSuffix(base, ext)

	// due to ffmpeg, the end must always contains the real extension.
	pattern := "." + name + ".*.part" + ext

	file, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return nil, "", err
	}
	return file, file.Name(), nil
}

func commitPartFile(partPath, outputPath string, overwrite bool) error {
	if overwrite {
		// Guard against accidentally targeting a directory
		if info, err := os.Stat(outputPath); err == nil && info.IsDir() {
			return fmt.Errorf("output path %q is a directory", outputPath)
		}
		return os.Rename(partPath, outputPath)
	}
	if err := os.Link(partPath, outputPath); err != nil {
		return err
	}
	return os.Remove(partPath)
}

func (d *Downloader) ensureTotalBar() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.totalBar == nil {
		if !d.closed {
			d.activeBars++
		}
		d.totalBar = d.progress.AddBar(0,
			mpb.BarPriority(100), // Ensure it's at the bottom
			mpb.PrependDecorators(
				decor.Name("Total ", decor.WC{W: 6}),
				decor.CountersKibiByte("% .2f / % .2f"),
			),
			d.downloadInfo(),
		)
	}
}

func (d *Downloader) downloadInfo() mpb.BarOption {
	return mpb.AppendDecorators(
		decor.Percentage(decor.WCSyncSpace),
		decor.Name(" | "),
		decor.AverageSpeed(decor.SizeB1024(0), "% .2f"),
		decor.Name(" | "),
		decor.AverageETA(decor.ET_STYLE_GO),
	)
}

func (d *Downloader) addTotalPos(n int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.totalBar != nil {
		d.totalBar.IncrBy(int(n))
	}
}

func (d *Downloader) addTotalSize(n int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.totalSize += n
	if d.totalBar != nil {
		d.totalBar.SetTotal(d.totalSize, false)
	}
}

func (d *Downloader) completeTotalBar() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.totalBar != nil {
		d.totalBar.SetTotal(d.totalSize, true)
	}
}

func (d *Downloader) newRequest(ctx context.Context, url, referer, userAgent string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if userAgent == "" {
		userAgent = d.userAgent
	}
	if userAgent != "" {
		req.Header.Set("User-Agent", userAgent)
	}
	if referer != "" {
		req.Header.Set("Referer", referer)
	}
	return req, nil
}

func (d *Downloader) do(req *http.Request) (*http.Response, error) {
	resp, err := d.client.Do(req)
	if err == nil || d.fallbackClient == nil || !isHTTP1MalformedHTTP2Error(err) {
		return resp, err
	}

	// i specifically disabled this log, because it spams everything! its nice for debugging extractors, but spams your logs otherwise full
	//slog.Debug("Retrying request with HTTP/2-capable transport", "url", req.URL.String(), "error", err)
	return d.fallbackClient.Do(req.Clone(req.Context()))
}

func isHTTP1MalformedHTTP2Error(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "HTTP/1.x transport connection broken") &&
		strings.Contains(msg, "malformed HTTP response")
}

func (d *Downloader) readURLWithRetry(ctx context.Context, rawURL, referer, userAgent, description string) ([]byte, error) {
	return d.readURLWithRetryProgress(ctx, rawURL, referer, userAgent, description, nil, nil)
}

func (d *Downloader) readURLWithRetryProgress(ctx context.Context, rawURL, referer, userAgent, description string, onContentLength, onBytes func(int64)) ([]byte, error) {
	var lastErr error
	for attempt := 1; attempt <= hlsRequestAttempts; attempt++ {
		var attemptContentLength int64
		var attemptBytes int64

		body, err := d.readURLOnceProgress(
			ctx,
			rawURL,
			referer,
			userAgent,
			description,
			func(n int64) {
				attemptContentLength += n
				if onContentLength != nil {
					onContentLength(n)
				}
			},
			func(n int64) {
				attemptBytes += n
				if onBytes != nil {
					onBytes(n)
				}
			},
		)
		if err == nil {
			return body, nil
		}

		lastErr = err
		if onContentLength != nil && attemptContentLength > 0 {
			onContentLength(-attemptContentLength)
		}
		if onBytes != nil && attemptBytes > 0 {
			onBytes(-attemptBytes)
		}

		if attempt < hlsRequestAttempts {
			slog.Debug("Retrying HLS read", "target", description, "attempt", attempt, "error", lastErr)
			if err := waitForRetry(ctx, attempt); err != nil {
				return nil, err
			}
		}
	}

	return nil, fmt.Errorf("failed to read %s after %d attempts: %w", description, hlsRequestAttempts, lastErr)
}

func (d *Downloader) readURLOnceProgress(ctx context.Context, rawURL, referer, userAgent, description string, onContentLength, onBytes func(int64)) ([]byte, error) {
	resp, err := d.getOnce(ctx, rawURL, referer, userAgent, description)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.ContentLength > 0 && onContentLength != nil {
		onContentLength(resp.ContentLength)
	}

	reader := d.readerFor(ctx, resp.Body)

	var body bytes.Buffer
	buf := make([]byte, 64*1024)
	for {
		n, readErr := reader.Read(buf)
		if n > 0 {
			if _, err := body.Write(buf[:n]); err != nil {
				return nil, fmt.Errorf("failed to buffer %s: %w", description, err)
			}
			if onBytes != nil {
				onBytes(int64(n))
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return nil, fmt.Errorf("failed to read %s: %w", description, readErr)
		}
	}

	return body.Bytes(), nil
}

func (d *Downloader) readerFor(ctx context.Context, body io.Reader) io.Reader {
	if d.limiter == nil {
		return body
	}
	return &rateLimitedReader{
		r:       body,
		limiter: d.limiter,
		ctx:     ctx,
	}
}

func (d *Downloader) getWithRetry(ctx context.Context, rawURL, referer, userAgent, description string) (*http.Response, error) {
	var lastErr error
	for attempt := 1; attempt <= hlsRequestAttempts; attempt++ {
		resp, err := d.getOnce(ctx, rawURL, referer, userAgent, description)
		if err == nil {
			return resp, nil
		}

		lastErr = err
		if !isRetryableFetchError(err) {
			return nil, fmt.Errorf("failed to fetch %s: %w", description, lastErr)
		}

		if attempt < hlsRequestAttempts {
			slog.Debug("Retrying HLS request", "target", description, "attempt", attempt, "error", lastErr)
			if err := waitForRetry(ctx, attempt); err != nil {
				return nil, err
			}
		}
	}
	return nil, fmt.Errorf("failed to fetch %s after %d attempts: %w", description, hlsRequestAttempts, lastErr)
}

type nonRetryableFetchError struct {
	err error
}

func (e nonRetryableFetchError) Error() string {
	return e.err.Error()
}

func (e nonRetryableFetchError) Unwrap() error {
	return e.err
}

func (d *Downloader) getOnce(ctx context.Context, rawURL, referer, userAgent, description string) (*http.Response, error) {
	req, err := d.newRequest(ctx, rawURL, referer, userAgent)
	if err != nil {
		return nil, err
	}

	resp, err := d.do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusOK {
		return resp, nil
	}

	err = fmt.Errorf("%s", resp.Status)
	retry := shouldRetryStatus(resp.StatusCode)
	resp.Body.Close()
	if !retry {
		return nil, nonRetryableFetchError{err: err}
	}
	return nil, err
}

func isRetryableFetchError(err error) bool {
	var nonRetryable nonRetryableFetchError
	return !errors.As(err, &nonRetryable)
}

func shouldRetryStatus(statusCode int) bool {
	return statusCode == http.StatusForbidden ||
		statusCode == http.StatusRequestTimeout ||
		statusCode == http.StatusTooManyRequests ||
		statusCode >= http.StatusInternalServerError
}

func waitForRetry(ctx context.Context, attempt int) error {
	timer := time.NewTimer(time.Duration(attempt) * hlsRetryDelay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func (d *Downloader) beginProgressLogging() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if !d.closed {
		d.activeBars++
	}
}

func (d *Downloader) endProgressLogging() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.activeBars > 0 {
		d.activeBars--
	}
}

func (d *Downloader) closeProgressLogging() {
	d.mu.Lock()
	d.closed = true
	d.activeBars = 0
	d.mu.Unlock()
	logger.ResetWriter()
}

func (d *Downloader) Write(p []byte) (int, error) {
	d.mu.Lock()
	logWithMPB := d.activeBars > 0 && !d.closed
	d.mu.Unlock()

	if logWithMPB {
		n, err := d.progress.Write(p)
		if err == nil {
			return n, nil
		}
	}

	return os.Stderr.Write(p)
}

// simpleDownload is meant for things like updating chrome.
func (d *Downloader) simpleDownload(ctx context.Context, resp *http.Response, targetFile *os.File, message string) error {
	contentLength := resp.ContentLength

	//d.ensureTotalBar()
	//d.addTotalSize(contentLength)

	d.beginProgressLogging()
	bar := d.progress.AddBar(contentLength,
		mpb.BarRemoveOnComplete(),
		mpb.PrependDecorators(
			decor.Name(message+" ", decor.WC{W: len(message) + 1}),
			decor.CountersKibiByte("% .2f / % .2f"),
		),
		d.downloadInfo(),
	)
	completed := false
	defer func() {
		if !completed {
			bar.Abort(true)
		}
		d.endProgressLogging()
	}()

	var reader io.Reader = resp.Body
	if d.limiter != nil {
		reader = &rateLimitedReader{
			r:       resp.Body,
			limiter: d.limiter,
			ctx:     ctx,
		}
	}

	proxyReader := bar.ProxyReader(reader)
	defer proxyReader.Close()

	// Wrap proxyReader to update totalBar
	var finalReader io.Reader = proxyReader
	if d.totalBar != nil {
		finalReader = io.TeeReader(proxyReader, totalWriter{d})
	}

	_, err := io.Copy(targetFile, finalReader)
	if err != nil {
		return err
	}

	bar.SetCurrent(contentLength)
	bar.SetTotal(contentLength, true) // this should remove the bar
	completed = true

	return nil
}

// segmentTask holds all pre-resolved info needed to fetch one HLS segment.
// Keys are resolved sequentially before any parallel fetching begins,
// because the key/IV can change on a per-segment basis.
type segmentTask struct {
	index  int
	url    string
	keyURL *url.URL
	key    []byte
	iv     []byte
}

// segmentResult is sent back from a fetch worker to the in-order writer.
type segmentResult struct {
	data []byte
	err  error
}

const hlsParallelSegments = 8

func (d *Downloader) m3u8Download(ctx context.Context, resp *http.Response, referer, userAgent, outputPath, message string, overwrite bool) error {
	mediaPlaylist, mediaPlaylistURL, err := d.loadMediaPlaylist(ctx, resp, referer, userAgent)
	if err != nil {
		return err
	}

	d.ensureTotalBar()
	d.beginProgressLogging()

	bar := d.progress.AddBar(0,
		mpb.PrependDecorators(
			decor.Name(message+" ", decor.WC{W: len(message) + 1}),
			decor.CountersKibiByte("% .2f / % .2f"),
		),
		d.downloadInfo(),
	)
	completed := false
	defer func() {
		if !completed {
			bar.Abort(true)
		}
		d.endProgressLogging()
	}()

	tsPath := outputPath
	if strings.HasSuffix(outputPath, ".mp4") {
		tsPath = strings.TrimSuffix(outputPath, ".mp4") + ".ts"
	}

	targetFile, tsPartPath, err := createPartFile(tsPath)
	if err != nil {
		return err
	}
	targetFileClosed := false
	tsCommitted := false
	defer func() {
		if !targetFileClosed {
			_ = targetFile.Close()
		}
		if !tsCommitted {
			_ = os.Remove(tsPartPath)
		}
	}()

	var tasks []segmentTask

	var currentKey []byte
	var currentIV []byte
	var currentKeyURL *url.URL
	var currentKeyHasExplicitIV bool
	keyCache := make(map[string][]byte)

	if mediaPlaylist.Key != nil {
		slog.Debug("HLS media playlist default key",
			"method", mediaPlaylist.Key.Method,
			"key_uri", mediaPlaylist.Key.URI,
			"iv", mediaPlaylist.Key.IV,
		)

		switch mediaPlaylist.Key.Method {
		case "AES-128":
			keyURL, err := mediaPlaylistURL.Parse(mediaPlaylist.Key.URI)
			if err != nil {
				return err
			}

			currentKeyURL = keyURL
			currentKey, err = d.fetchHLSKey(ctx, keyURL, referer, userAgent, keyCache, false)
			if err != nil {
				return err
			}

			currentIV, currentKeyHasExplicitIV, err = hlsKeyIV(mediaPlaylist.Key.IV)
			if err != nil {
				return err
			}

		case "NONE":
			currentKey = nil
			currentIV = nil
			currentKeyURL = nil
			currentKeyHasExplicitIV = false

		default:
			return fmt.Errorf("unsupported encryption method: %s", mediaPlaylist.Key.Method)
		}
	} else {
		slog.Debug("HLS media playlist has no default AES key")
	}

	for i, segment := range mediaPlaylist.Segments {
		if segment == nil {
			break
		}

		if segment.Key != nil {
			switch segment.Key.Method {
			case "AES-128":
				keyURL, err := mediaPlaylistURL.Parse(segment.Key.URI)
				if err != nil {
					return err
				}

				currentKeyURL = keyURL
				currentKey, err = d.fetchHLSKey(ctx, keyURL, referer, userAgent, keyCache, false)
				if err != nil {
					return err
				}

				currentIV, currentKeyHasExplicitIV, err = hlsKeyIV(segment.Key.IV)
				if err != nil {
					return err
				}

			case "NONE":
				slog.Debug("HLS AES key disabled for segment", "segment", i)

				currentKey = nil
				currentIV = nil
				currentKeyURL = nil
				currentKeyHasExplicitIV = false

			default:
				return fmt.Errorf("unsupported encryption method: %s", segment.Key.Method)
			}
		}

		iv := currentIV
		if currentKey != nil && !currentKeyHasExplicitIV {
			iv = hlsSegmentIV(mediaPlaylist.SeqNo, i)
		}

		if currentKey != nil {
			keyURL := ""
			if currentKeyURL != nil {
				keyURL = currentKeyURL.String()
			}

			slog.Debug("Using HLS AES key for segment",
				"segment", i,
				"key_url", keyURL,
				"key", hex.EncodeToString(currentKey),
				"iv", hex.EncodeToString(iv),
			)
		} else {
			slog.Debug("HLS segment has no AES key", "segment", i)
		}

		segmentURL, err := mediaPlaylistURL.Parse(segment.URI)
		if err != nil {
			return err
		}

		keyCopy := append([]byte(nil), currentKey...)
		ivCopy := append([]byte(nil), iv...)

		var keyURLCopy *url.URL
		if currentKeyURL != nil {
			copied := *currentKeyURL
			keyURLCopy = &copied
		}

		tasks = append(tasks, segmentTask{
			index:  i,
			url:    segmentURL.String(),
			keyURL: keyURLCopy,
			key:    keyCopy,
			iv:     ivCopy,
		})
	}

	resultChans := make([]chan segmentResult, len(tasks))
	for i := range resultChans {
		resultChans[i] = make(chan segmentResult, 1)
	}

	var progressMu sync.Mutex
	var downloadedBytes int64
	var totalBytes int64

	recordContentLength := func(n int64) {
		progressMu.Lock()
		totalBytes += n
		if totalBytes < 0 {
			totalBytes = 0
		}
		total := totalBytes
		bar.SetTotal(total, false)
		progressMu.Unlock()

		d.addTotalSize(n)
	}

	recordBytes := func(n int64) {
		progressMu.Lock()
		downloadedBytes += n
		if downloadedBytes < 0 {
			downloadedBytes = 0
		}
		if n >= 0 {
			bar.IncrBy(int(n))
		} else {
			bar.SetCurrent(downloadedBytes)
		}
		progressMu.Unlock()

		d.addTotalPos(n)
	}

	sem := make(chan struct{}, hlsParallelSegments)

	fetchCtx, cancelFetch := context.WithCancel(ctx)
	defer cancelFetch()

	for idx, task := range tasks {
		idx, task := idx, task

		go func() {
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-fetchCtx.Done():
				resultChans[idx] <- segmentResult{err: fetchCtx.Err()}
				return
			}

			select {
			case <-fetchCtx.Done():
				resultChans[idx] <- segmentResult{err: fetchCtx.Err()}
				return
			default:
			}

			data, err := d.readURLWithRetryProgress(
				fetchCtx,
				task.url,
				referer,
				userAgent,
				fmt.Sprintf("HLS segment %d", task.index),
				recordContentLength,
				recordBytes,
			)
			if err != nil {
				resultChans[idx] <- segmentResult{err: err}
				return
			}

			data, err = decryptHLSSegment(data, task.key, task.iv)
			if err != nil && task.keyURL != nil {
				slog.Warn("HLS segment decrypt failed, retrying key fetch",
					"segment", task.index,
					"error", err,
				)

				newKey, keyErr := d.fetchHLSKey(fetchCtx, task.keyURL, referer, userAgent, nil, true)
				if keyErr == nil {
					data, err = decryptHLSSegment(data, newKey, task.iv)
				}
			}

			resultChans[idx] <- segmentResult{data: data, err: err}
		}()
	}

	for _, ch := range resultChans {
		result := <-ch
		if result.err != nil {
			cancelFetch()
			return result.err
		}

		n, err := targetFile.Write(result.data)
		if err != nil {
			cancelFetch()
			return err
		}
		_ = n
	}

	progressMu.Lock()
	finalDownloadedBytes := downloadedBytes
	missingTotalBytes := finalDownloadedBytes - totalBytes
	if missingTotalBytes > 0 {
		totalBytes = finalDownloadedBytes
		bar.SetTotal(totalBytes, false)
	}
	bar.SetCurrent(finalDownloadedBytes)
	bar.SetTotal(finalDownloadedBytes, true)
	progressMu.Unlock()
	if missingTotalBytes > 0 {
		d.addTotalSize(missingTotalBytes)
	}

	completed = true

	if err := targetFile.Close(); err != nil {
		return err
	}
	targetFileClosed = true

	tsInputPath := tsPartPath
	if tsPath == outputPath {
		if err := commitPartFile(tsPartPath, tsPath, overwrite); err != nil {
			return err
		}

		tsCommitted = true
		tsInputPath = tsPath
	}

	if tsPath != outputPath {
		if d.ffmpegPath == "" {
			return fmt.Errorf("ffmpeg path is not configured for HLS remux")
		}

		outputPartFile, outputPartPath, err := createPartFile(outputPath)
		if err != nil {
			return err
		}

		if err := outputPartFile.Close(); err != nil {
			_ = os.Remove(outputPartPath)
			return err
		}

		outputCommitted := false
		defer func() {
			if !outputCommitted {
				_ = os.Remove(outputPartPath)
			}
		}()

		slog.Debug("Remuxing with FFmpeg", "in", tsInputPath, "out", outputPath)

		cmd := exec.CommandContext(ctx, d.ffmpegPath, "-y", "-i", tsInputPath, "-c", "copy", outputPartPath)
		if !d.debug {
			cmd.Stdout = nil
			cmd.Stderr = nil
		} else {
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			slog.Debug("FFmpeg command", "args", cmd.Args)
		}

		if err := cmd.Run(); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return ctxErr
			}

			return fmt.Errorf("ffmpeg remux failed: %w", err)
		}

		if err := commitPartFile(outputPartPath, outputPath, overwrite); err != nil {
			return err
		}

		outputCommitted = true
	}

	return nil
}
func (d *Downloader) loadMediaPlaylist(ctx context.Context, resp *http.Response, referer, userAgent string) (*m3u8.MediaPlaylist, *url.URL, error) {
	m3u8Bytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, err
	}

	p, listType, err := m3u8.DecodeFrom(bytes.NewReader(m3u8Bytes), true)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to decode m3u8: %w", err)
	}

	mediaPlaylistURL := resp.Request.URL

	switch listType {
	case m3u8.MASTER:
		master := p.(*m3u8.MasterPlaylist)
		if len(master.Variants) == 0 {
			return nil, nil, fmt.Errorf("no variants in master playlist")
		}

		sort.Slice(master.Variants, func(i, j int) bool {
			return master.Variants[i].Bandwidth > master.Variants[j].Bandwidth
		})

		bestVariant := master.Variants[0]
		variantURL, err := mediaPlaylistURL.Parse(bestVariant.URI)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to parse variant URL: %w", err)
		}
		mediaPlaylist, err := d.fetchMediaPlaylist(ctx, variantURL, referer, userAgent)
		return mediaPlaylist, variantURL, err
	case m3u8.MEDIA:
		return p.(*m3u8.MediaPlaylist), mediaPlaylistURL, nil
	}

	return nil, nil, fmt.Errorf("unsupported playlist type")
}

func (d *Downloader) fetchMediaPlaylist(ctx context.Context, playlistURL *url.URL, referer, userAgent string) (*m3u8.MediaPlaylist, error) {
	playlistBytes, err := d.readURLWithRetry(ctx, playlistURL.String(), referer, userAgent, "media playlist")
	if err != nil {
		return nil, err
	}

	p, listType, err := m3u8.DecodeFrom(bytes.NewReader(playlistBytes), true)
	if err != nil {
		return nil, fmt.Errorf("failed to decode media playlist: %w", err)
	}
	if listType != m3u8.MEDIA {
		return nil, fmt.Errorf("variant playlist is not a media playlist")
	}
	return p.(*m3u8.MediaPlaylist), nil
}

func (d *Downloader) fetchHLSKey(ctx context.Context, keyURL *url.URL, referer, userAgent string, cache map[string][]byte, forceRefresh bool) ([]byte, error) {
	cacheKey := keyURL.String()
	if !forceRefresh {
		if key, ok := cache[cacheKey]; ok {
			slog.Debug("Using cached HLS AES key", "key_url", cacheKey, "key", hex.EncodeToString(key))
			return key, nil
		}
	}

	key, err := d.readURLWithRetry(ctx, cacheKey, referer, userAgent, "HLS AES key")
	if err != nil {
		return nil, err
	}
	if len(key) != aes.BlockSize {
		return nil, fmt.Errorf("invalid HLS AES key length from %s: %d", cacheKey, len(key))
	}
	if cache != nil {
		cache[cacheKey] = key
	}
	slog.Debug("Fetched HLS AES key", "key_url", cacheKey, "key", hex.EncodeToString(key), "force_refresh", forceRefresh)
	return key, nil
}

func estimateHLSTotal(downloadedBytes int64, downloadedDuration, totalDuration float64) int64 {
	if downloadedDuration <= 0 || totalDuration <= 0 {
		return downloadedBytes
	}
	return int64((float64(downloadedBytes) * totalDuration) / downloadedDuration)
}

func shouldUpdateHLSProgress(segmentIndex int, lastUpdate time.Time) bool {
	return segmentIndex%hlsProgressSegmentInterval == 0 || time.Since(lastUpdate) >= hlsProgressUpdateInterval
}

func hlsSegmentIV(sequenceNumber uint64, segmentIndex int) []byte {
	iv := make([]byte, aes.BlockSize)
	binary.BigEndian.PutUint64(iv[8:], sequenceNumber+uint64(segmentIndex))
	return iv
}

func hlsKeyIV(rawIV string) ([]byte, bool, error) {
	if rawIV == "" {
		return nil, false, nil
	}

	iv, err := hex.DecodeString(strings.TrimPrefix(rawIV, "0x"))
	if err != nil {
		return nil, false, err
	}
	if len(iv) != aes.BlockSize {
		return nil, false, fmt.Errorf("invalid AES IV length: %d", len(iv))
	}
	return iv, true, nil
}

func decryptHLSSegment(segmentBytes, key, iv []byte) ([]byte, error) {
	if key == nil {
		return segmentBytes, nil
	}
	if len(segmentBytes) == 0 || len(segmentBytes)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("encrypted segment size is not a multiple of AES block size")
	}
	if len(iv) != aes.BlockSize {
		return nil, fmt.Errorf("invalid AES IV length: %d", len(iv))
	}

	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}

	decrypted := make([]byte, len(segmentBytes))
	cipher.NewCBCDecrypter(block, iv).CryptBlocks(decrypted, segmentBytes)

	paddingLen := int(decrypted[len(decrypted)-1])
	if paddingLen <= 0 || paddingLen > aes.BlockSize || paddingLen > len(decrypted) {
		return nil, fmt.Errorf("invalid AES padding length: %d", paddingLen)
	}
	for _, b := range decrypted[len(decrypted)-paddingLen:] {
		if int(b) != paddingLen {
			return nil, fmt.Errorf("invalid AES padding bytes")
		}
	}
	return decrypted[:len(decrypted)-paddingLen], nil
}

type totalWriter struct {
	d *Downloader
}

func (tw totalWriter) Write(p []byte) (int, error) {
	tw.d.addTotalPos(int64(len(p)))
	return len(p), nil
}

func (d *Downloader) Wait() {
	d.Close()
}

func (d *Downloader) Close() {
	d.finishOnce.Do(func() {
		d.completeTotalBar()
		d.progress.Wait()
		d.closeProgressLogging()
	})
}

func (d *Downloader) Shutdown() {
	d.finishOnce.Do(func() {
		d.progress.Shutdown()
		d.closeProgressLogging()
	})
}

type rateLimitedReader struct {
	r       io.Reader
	limiter *rate.Limiter
	ctx     context.Context
}

func (r *rateLimitedReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if n > 0 {
		if err := r.wait(n); err != nil {
			return n, err
		}
	}
	return n, err
}

func (r *rateLimitedReader) wait(n int) error {
	burst := r.limiter.Burst()
	if burst <= 0 {
		burst = 1
	}

	for n > 0 {
		chunk := n
		if chunk > burst {
			chunk = burst
		}
		if err := r.limiter.WaitN(r.ctx, chunk); err != nil {
			return err
		}
		n -= chunk
	}
	return nil
}

func rateLimitBurst(limitRate float64) int {
	if limitRate < 1 {
		return 1
	}
	if limitRate > maxRateLimitBurst {
		return maxRateLimitBurst
	}
	return int(limitRate)
}

func slogInfo(format string, args ...interface{}) {
	slog.Info(fmt.Sprintf(format, args...))
}
