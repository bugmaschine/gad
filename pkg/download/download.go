package download

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/grafov/m3u8"
	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
	"golang.org/x/time/rate"
)

type Downloader struct {
	client     *http.Client
	progress   *mpb.Progress
	totalBar   *mpb.Bar
	totalSize  int64
	limiter    *rate.Limiter
	userAgent  string
	ffmpegPath string
	debug      bool
	mu         sync.Mutex
}

func NewDownloader(userAgent string, debug bool, limitRate float64) *Downloader {
	var rLimit *rate.Limiter
	if limitRate > 0 {
		rLimit = rate.NewLimiter(rate.Limit(limitRate), int(limitRate))
	}

	p := mpb.New()
	return &Downloader{
		client:    &http.Client{},
		progress:  p,
		limiter:   rLimit,
		userAgent: userAgent,
		debug:     debug,
	}
}

func (d *Downloader) SetFfmpegPath(path string) {
	d.ffmpegPath = path
}

func (d *Downloader) DownloadToFile(ctx context.Context, task *DownloadTask) error {
	slog.Debug("Starting download to file", "url", task.Url, "path", task.OutputPath)
	if task.SkipExisting {
		if _, err := os.Stat(task.OutputPath); err == nil {
			slogInfo("skipping download for %s: file already exists", filepath.Base(task.OutputPath))
			return nil
		}
	}

	req, err := http.NewRequestWithContext(ctx, "GET", task.Url, nil)
	if err != nil {
		return err
	}

	if d.userAgent != "" {
		req.Header.Set("User-Agent", d.userAgent)
	}
	if task.Referer != "" {
		req.Header.Set("Referer", task.Referer)
	}

	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	slog.Debug("Got response", "status", resp.Status, "content-type", resp.Header.Get("Content-Type"))
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status: %s", resp.Status)
	}

	contentType := resp.Header.Get("Content-Type")
	isM3U8 := strings.Contains(strings.ToLower(resp.Request.URL.String()), ".m3u8") ||
		strings.Contains(strings.ToLower(contentType), "application/vnd.apple.mpegurl") ||
		strings.Contains(strings.ToLower(contentType), "application/x-mpegURL")

	outputPath := task.OutputPath
	if !task.OutputPathHasExtension {
		outputPath += ".mp4"
	}

	message := task.CustomMessage
	if message == "" {
		message = filepath.Base(outputPath)
	}

	flags := os.O_CREATE | os.O_WRONLY
	if task.OverwriteFile {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_EXCL
	}

	targetFile, err := os.OpenFile(outputPath, flags, 0644)
	if err != nil {
		return err
	}
	defer targetFile.Close()

	if isM3U8 {
		slog.Debug("Detected M3U8 playlist, starting HLS download")
		return d.m3u8Download(ctx, resp, task.Referer, outputPath, message)
	} else {
		slog.Debug("Starting simple file download")
		return d.simpleDownload(ctx, resp, targetFile, message)
	}
}

func (d *Downloader) ensureTotalBar() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.totalBar == nil {
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

func (d *Downloader) simpleDownload(ctx context.Context, resp *http.Response, targetFile *os.File, message string) error {
	contentLength := resp.ContentLength

	d.ensureTotalBar()
	d.addTotalSize(contentLength)
	// idk???????????
	bar := d.progress.AddBar(contentLength,
		mpb.PrependDecorators(
			decor.Name(message, decor.WC{W: len(message) + 1}),
			decor.CountersKibiByte("% .2f / % .2f"),
		),
		d.downloadInfo(),
	)

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

	return nil
}

func (d *Downloader) m3u8Download(ctx context.Context, resp *http.Response, referer, outputPath, message string) error {
	m3u8Bytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}

	p, listType, err := m3u8.DecodeFrom(bytes.NewReader(m3u8Bytes), true)
	if err != nil {
		return fmt.Errorf("failed to decode m3u8: %w", err)
	}

	var mediaPlaylist *m3u8.MediaPlaylist
	mediaPlaylistURL := resp.Request.URL

	if listType == m3u8.MASTER {
		master := p.(*m3u8.MasterPlaylist)
		if len(master.Variants) == 0 {
			return fmt.Errorf("no variants in master playlist")
		}

		// Sort variants by bandwidth (descending) as simple quality heuristic
		sort.Slice(master.Variants, func(i, j int) bool {
			return master.Variants[i].Bandwidth > master.Variants[j].Bandwidth
		})

		bestVariant := master.Variants[0]
		variantURL, err := mediaPlaylistURL.Parse(bestVariant.URI)
		if err != nil {
			return fmt.Errorf("failed to parse variant URL: %w", err)
		}

		mediaPlaylistURL = variantURL
		req, err := http.NewRequestWithContext(ctx, "GET", variantURL.String(), nil)
		if err != nil {
			return err
		}
		if d.userAgent != "" {
			req.Header.Set("User-Agent", d.userAgent)
		}
		if referer != "" {
			req.Header.Set("Referer", referer)
		}

		vResp, err := d.client.Do(req)
		if err != nil {
			return err
		}
		defer vResp.Body.Close()

		vp, vt, err := m3u8.DecodeFrom(vResp.Body, true)
		if err != nil || vt != m3u8.MEDIA {
			return fmt.Errorf("failed to decode media playlist: %w", err)
		}
		mediaPlaylist = vp.(*m3u8.MediaPlaylist)
	} else if listType == m3u8.MEDIA {
		mediaPlaylist = p.(*m3u8.MediaPlaylist)
	} else {
		return fmt.Errorf("unsupported playlist type")
	}

	d.ensureTotalBar()

	// per episode bar

	bar := d.progress.AddBar(0, // Total will be updated as we go
		mpb.PrependDecorators(
			decor.Name(message+" ", decor.WC{W: len(message) + 1}),
			decor.CountersKibiByte("% .2f / % .2f"),
		),
		d.downloadInfo(),
	)

	// Use a temporary .ts file for m3u8
	tsPath := outputPath
	if strings.HasSuffix(outputPath, ".mp4") {
		tsPath = strings.TrimSuffix(outputPath, ".mp4") + ".ts"
	}

	targetFile, err := os.OpenFile(tsPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer targetFile.Close()

	var totalDuration float64
	for _, seg := range mediaPlaylist.Segments {
		if seg == nil {
			break
		}
		totalDuration += seg.Duration
	}

	var downloadedBytes int64
	var downloadedDuration float64
	var currentKey []byte
	var currentIV []byte
	var lastEstimation int64

	for i, segment := range mediaPlaylist.Segments {
		if segment == nil {
			break
		}

		if segment.Key != nil {
			if segment.Key.Method == "AES-128" {
				keyURL, err := mediaPlaylistURL.Parse(segment.Key.URI)
				if err != nil {
					return err
				}

				req, err := http.NewRequestWithContext(ctx, "GET", keyURL.String(), nil)
				if err != nil {
					return err
				}
				if d.userAgent != "" {
					req.Header.Set("User-Agent", d.userAgent)
				}
				if referer != "" {
					req.Header.Set("Referer", referer)
				}

				kResp, err := d.client.Do(req)
				if err != nil {
					return err
				}
				currentKey, err = io.ReadAll(kResp.Body)
				kResp.Body.Close()
				if err != nil {
					return err
				}

				if segment.Key.IV != "" {
					ivStr := strings.TrimPrefix(segment.Key.IV, "0x")
					currentIV, err = hex.DecodeString(ivStr)
					if err != nil {
						return err
					}
				} else {
					// Use sequence number as IV if not provided
					seq := uint64(mediaPlaylist.SeqNo) + uint64(i)
					currentIV = make([]byte, 16)
					binary.BigEndian.PutUint64(currentIV[8:], seq)
				}
			} else {
				return fmt.Errorf("unsupported encryption method: %s", segment.Key.Method)
			}
		}

		segmentURL, err := mediaPlaylistURL.Parse(segment.URI)
		if err != nil {
			return err
		}

		req, err := http.NewRequestWithContext(ctx, "GET", segmentURL.String(), nil)
		if err != nil {
			return err
		}
		if d.userAgent != "" {
			req.Header.Set("User-Agent", d.userAgent)
		}
		if referer != "" {
			req.Header.Set("Referer", referer)
		}

		sResp, err := d.client.Do(req)
		if err != nil {
			return err
		}

		segmentBytes, err := io.ReadAll(sResp.Body)
		sResp.Body.Close()
		if err != nil {
			return err
		}

		if currentKey != nil {
			block, err := aes.NewCipher(currentKey)
			if err != nil {
				return err
			}
			mode := cipher.NewCBCDecrypter(block, currentIV)
			// PKCS7 padding is handled by the M3U8 standard, but we might need to unpad if it's not a multiple of block size (though it should be for AES-128 in HLS)
			decrypted := make([]byte, len(segmentBytes))
			mode.CryptBlocks(decrypted, segmentBytes)

			// Unpad PKCS7
			paddingLen := int(decrypted[len(decrypted)-1])
			if paddingLen > 0 && paddingLen <= aes.BlockSize {
				segmentBytes = decrypted[:len(decrypted)-paddingLen]
			} else {
				segmentBytes = decrypted
			}
		}

		n, err := targetFile.Write(segmentBytes)
		if err != nil {
			return err
		}
		downloadedBytes += int64(n)
		downloadedDuration += segment.Duration

		// Estimation
		estimatedTotal := int64((float64(downloadedBytes) * totalDuration) / downloadedDuration)
		bar.SetTotal(estimatedTotal, false)

		// Update total bar with the change in estimation
		diff := estimatedTotal - lastEstimation
		d.addTotalSize(diff)
		lastEstimation = estimatedTotal

		bar.SetCurrent(downloadedBytes)
		d.addTotalPos(int64(n))
	}

	bar.SetTotal(downloadedBytes, true)
	bar.SetCurrent(downloadedBytes)

	targetFile.Close()

	// Post-processing with FFmpeg
	if d.ffmpegPath != "" && tsPath != outputPath {
		slog.Debug("Remuxing with FFmpeg", "in", tsPath, "out", outputPath)
		cmd := exec.Command(d.ffmpegPath, "-y", "-i", tsPath, "-c", "copy", outputPath)
		if !d.debug {
			cmd.Stdout = nil
			cmd.Stderr = nil
		}
		if err := cmd.Run(); err == nil {
			os.Remove(tsPath)
		} else {
			slog.Warn("FFmpeg remux failed", "error", err)
		}
	}

	return nil
}

type totalWriter struct {
	d *Downloader
}

func (tw totalWriter) Write(p []byte) (int, error) {
	tw.d.addTotalPos(int64(len(p)))
	return len(p), nil
}

func (d *Downloader) Wait() {
	d.progress.Wait()
}

type rateLimitedReader struct {
	r       io.Reader
	limiter *rate.Limiter
	ctx     context.Context
}

func (r *rateLimitedReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if n > 0 {
		if err := r.limiter.WaitN(r.ctx, n); err != nil {
			return n, err
		}
	}
	return n, err
}

func slogInfo(format string, args ...interface{}) {
	slog.Info(fmt.Sprintf(format, args...))
}
