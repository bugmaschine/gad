package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"time"

	"github.com/bugmaschine/sdl/internal/downloaders"
	"github.com/bugmaschine/sdl/internal/extractors"
	"github.com/bugmaschine/sdl/pkg/chrome"
	"github.com/bugmaschine/sdl/pkg/cli"
	"github.com/bugmaschine/sdl/pkg/dirs"
	"github.com/bugmaschine/sdl/pkg/download"
	"github.com/bugmaschine/sdl/pkg/ffmpeg"
	"github.com/bugmaschine/sdl/pkg/logger"
)

func main() {
	args := &cli.Args{}
	rootCmd := cli.NewRootCommand(args)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Set up logger
	logger.InitDefaultLogger(args.Debug)

	// Create data dir
	dataDir, err := dirs.GetDataDir()
	if err != nil {
		slog.Error("Failed to create data directory", "error", err)
		os.Exit(1)
	}

	// Get save directory
	saveDir, err := dirs.GetSaveDirectory("") // Change to args if added
	if err != nil {
		slog.Error("Failed to get save directory", "error", err)
		os.Exit(1)
	}

	// Context with signal handling
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	// Rate limit parsing
	rateLimit, err := cli.ParseRateLimit(args.LimitRate)
	if err != nil {
		slog.Error("Failed to parse rate limit", "error", err)
		os.Exit(1)
	}

	// Downloader for assets (FFmpeg, uBlock)
	assetDownloader := download.NewDownloader("SDL/1.0", args.Debug, rateLimit)

	// Create FFmpeg manager
	ff := ffmpeg.New(dataDir)

	// Auto-download FFmpeg
	slog.Info("Checking for FFmpeg...")
	ffmpegPath, err := ff.AutoDownload(ctx, assetDownloader)
	if err != nil {
		slog.Error("Failed to manage FFmpeg", "error", err)
		os.Exit(1)
	}
	slog.Info("Using FFmpeg at", "path", ffmpegPath)
	assetDownloader.SetFfmpegPath(ffmpegPath)

	// Chrome management
	chromeMgr := chrome.NewManager(dataDir, assetDownloader)

	// Main work
	if args.Url != "" {
		if args.Extractor != "" {
			handleSingleDownload(ctx, args, assetDownloader, chromeMgr, saveDir)
		} else {
			handleSeriesDownload(ctx, args, assetDownloader, chromeMgr, saveDir)
		}
	} else {
		slog.Error("Please specify a URL with -u")
		os.Exit(1)
	}
}

func handleSeriesDownload(ctx context.Context, args *cli.Args, d *download.Downloader, cm *chrome.ChromeManager, saveDir string) {
	dl, err := downloaders.GetDownloader(args.Url)
	if err != nil {
		slog.Error("Failed to get downloader", "error", err)
		return
	}
	if dl == nil {
		slog.Error("No downloader supports this URL. Maybe use -e to specify an extractor for a single file?")
		return
	}

	// Browser session for scraping
	scrapeCtx, cancel, err := cm.Get(ctx, !args.Browser, args.Debug)
	if err != nil {
		slog.Error("Failed to start browser", "error", err)
		return
	}
	defer cancel()

	slog.Info("Fetching series info...")
	info, err := dl.GetSeriesInfo(scrapeCtx)
	if err != nil {
		slog.Error("Failed to get series info", "error", err)
		return
	}
	slog.Info("Series", "title", info.Title)

	manager := download.NewDownloadManager(d, args.ConcurrentDownloads, saveDir, *info, args.SkipExisting)
	taskChan := make(chan *downloaders.DownloadTaskWrapper, 50)

	// Start manager in background
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		manager.ProgressDownloads(ctx)
	}()

	// Feed tasks from downloader to manager
	go func() {
		for tw := range taskChan {
			manager.Submit(download.ManagerTask{
				DownloadUrl: tw.Url,
				Referer:     tw.Referer,
				VideoType:   tw.Lang,
				EpisodeInfo: tw.Episode,
			})
		}
		manager.Close()
	}()

	settings := downloaders.DownloadSettings{
		SkipExisting: args.SkipExisting,
	}

	req := downloaders.DownloadRequest{
		Url:           args.Url,
		SaveDirectory: saveDir,
		SeriesTitle:   info.Title,
		// Other fields like language selection could be added to CLI args
	}

	slog.Info("Starting scrape...")
	if err := dl.Download(scrapeCtx, req, settings, taskChan); err != nil {
		slog.Error("Scrape failed", "error", err)
	}

	close(taskChan)
	wg.Wait()
	slog.Info("Done!")
}

func handleSingleDownload(ctx context.Context, args *cli.Args, d *download.Downloader, cm *chrome.ChromeManager, saveDir string) {
	slog.Info("Extracting video URL...", "url", args.Url)

	// If it needs chrome (complex extractors), we would handle that here.
	// For simple extractors like Vidoza:
	ext, err := extractors.ExtractVideoUrl(ctx, args.Url, "", "")
	if err != nil {
		slog.Error("Failed to extract video URL", "error", err)
		return
	}
	if ext == nil {
		slog.Error("No extractor supported this URL")
		return
	}

	timestamp := time.Now().Format("2006-01-02_15-04-05.000")
	outputPath := filepath.Join(saveDir, timestamp)

	task := download.NewDownloadTask(outputPath, ext.Url).
		SetSkipExisting(args.SkipExisting).
		SetReferer(ext.Referer)

	slog.Info("Starting download...", "url", ext.Url)
	if err := d.DownloadToFile(ctx, task); err != nil {
		slog.Error("Download failed", "error", err)
	}

	d.Wait()
}
