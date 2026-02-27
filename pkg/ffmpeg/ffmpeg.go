package ffmpeg

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/bugmaschine/gad/pkg/download"
)

// Downloader is an interface that matches the required functionality for ffmpeg auto-download.
type Downloader interface {
	DownloadToFile(ctx context.Context, task *download.DownloadTask) error
}

type Ffmpeg struct {
	dataDir string
}

func New(dataDir string) *Ffmpeg {
	return &Ffmpeg{dataDir: dataDir}
}

func (f *Ffmpeg) AutoDownload(ctx context.Context, downloader Downloader) (string, error) {
	if path, err := f.GetFfmpegPath(); err == nil && path != "" {
		return path, nil
	}

	url, err := ffmpegDownloadUrl()
	if err != nil {
		return "", err
	}

	gzipPath := f.getFfmpegDataPath(true)
	task := download.NewDownloadTask(gzipPath, url).
		SetOverwriteFile(true).
		SetCustomMessage("Downloading FFmpeg")

	task.OutputPathHasExtension = true

	err = downloader.DownloadToFile(ctx, task)
	if err != nil {
		return "", fmt.Errorf("failed to download ffmpeg: %w", err)
	}

	ffmpegPath := f.getFfmpegDataPath(false)

	err = f.decompressGzip(gzipPath, ffmpegPath)
	if err != nil {
		return "", err
	}

	_ = os.Remove(gzipPath)

	return ffmpegPath, nil
}

func (f *Ffmpeg) GetFfmpegPath() (string, error) {
	exeName := ffmpegExecutableName()
	path, err := exec.LookPath(exeName)
	if err == nil {
		return path, nil
	}

	dataPath := f.getFfmpegDataPath(false)
	if _, err := os.Stat(dataPath); err == nil {
		return dataPath, nil
	}

	return "", fmt.Errorf("ffmpeg not found")
}

func (f *Ffmpeg) getFfmpegDataPath(gzip bool) string {
	name := ffmpegExecutableName()
	if gzip {
		name = "ffmpeg.gz"
	}
	return filepath.Join(f.dataDir, name)
}

func (f *Ffmpeg) decompressGzip(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("failed to open gzip file: %w", err)
	}
	defer srcFile.Close()

	gzReader, err := gzip.NewReader(srcFile)
	if err != nil {
		return fmt.Errorf("failed to create gzip reader: %w", err)
	}
	defer gzReader.Close()

	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0755)
	if err != nil {
		return fmt.Errorf("failed to create output file: %w", err)
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, gzReader)
	if err != nil {
		return fmt.Errorf("failed to decompress gzip content: %w", err)
	}

	return nil
}

func ffmpegExecutableName() string {
	if runtime.GOOS == "windows" {
		return "ffmpeg.exe"
	}
	return "ffmpeg"
}

func ffmpegDownloadUrl() (string, error) {
	var platform string
	switch runtime.GOOS {
	case "linux":
		platform = "linux"
	case "windows":
		platform = "win32"
	case "darwin":
		platform = "darwin"
	case "freebsd":
		platform = "freebsd"
	default:
		return "", fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}

	var arch string
	switch runtime.GOARCH {
	case "amd64":
		arch = "x64"
	case "386":
		arch = "ia32"
	case "arm64":
		arch = "arm64"
	case "arm":
		arch = "arm"
	default:
		return "", fmt.Errorf("unsupported architecture: %s", runtime.GOARCH)
	}

	// Validation based on Rust code
	supported := true
	if runtime.GOOS == "windows" && (runtime.GOARCH == "arm64" || runtime.GOARCH == "arm") {
		supported = false
	}
	if runtime.GOOS == "darwin" && (runtime.GOARCH == "386" || runtime.GOARCH == "arm") {
		supported = false
	}
	if runtime.GOOS == "freebsd" && (runtime.GOARCH != "amd64") {
		supported = false
	}

	if !supported {
		return "", fmt.Errorf("unsupported platform architecture combination: %s %s", runtime.GOOS, runtime.GOARCH)
	}

	return fmt.Sprintf("https://github.com/eugeneware/ffmpeg-static/releases/latest/download/ffmpeg-%s-%s.gz", platform, arch), nil
}
