package main

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/bugmaschine/gad/internal/downloaders"
	"github.com/bugmaschine/gad/internal/extractors"
	"github.com/bugmaschine/gad/pkg/chrome"
	"github.com/bugmaschine/gad/pkg/cli"
	"github.com/bugmaschine/gad/pkg/dirs"
	"github.com/bugmaschine/gad/pkg/download"
	"github.com/bugmaschine/gad/pkg/ffmpeg"
	"github.com/bugmaschine/gad/pkg/logger"
	"github.com/bugmaschine/gad/pkg/utils"
)

func main() {
	args := &cli.Args{}
	rootCmd := cli.NewRootCommand(args)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	// Set up logger
	logger.InitDefaultLogger(args.Debug, args.LogFile)

	slog.Info("gad started")

	// Create data dir
	dataDir, err := dirs.GetDataDir()
	if err != nil {
		slog.Error("Failed to create data directory", "error", err)
		os.Exit(1)
	}

	// Get save directory
	saveDir, err := dirs.GetSaveDirectory(args.OutputFolder)
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
	assetDownloader := download.NewDownloader("gad/1.0", args.Debug, rateLimit)

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

	if args.QueueFile != "" {
		slog.Debug("Queue file specified", "file", args.QueueFile)
		queueFile, err := os.Open(args.QueueFile)
		if err != nil {
			slog.Error("Failed to open queue file", "error", err)
			os.Exit(1)
		}
		defer queueFile.Close()

		scanner := bufio.NewScanner(queueFile)
		for scanner.Scan() {
			// basically each row is an url, if it has an hashtag, we ignore it.
			line := strings.Trim(scanner.Text(), "\n")
			slog.Debug("Processing line from queue", "line", line)

			// check if line is valid
			if line == "" || strings.HasPrefix(line, "#") {
				slog.Debug("Skipping invalid line", "line", line)
				continue
			}

			if strings.Contains(line, "#") {
				// remove comments at the end exmample: "https://example.com/series/1 # this is a comment" to "https://example.com/series/1 "
				line = strings.Split(line, "#")[0]
				// remove trailing spaces exmample: "https://example.com/series/1 " to "https://example.com/series/1"
				line = strings.TrimSpace(line)
				slog.Debug("Removed comment from line", "line", line)
			}

			// as queue is meant for keeping a library up to date, skip existing is forced to be on.
			args.SkipExisting = true
			// For simplicity, we just set the URL and call the handler for each line.
			args.Url = line
			slog.Info("Processing URL from queue", "url", args.Url)
			// I know that this could be better, but realistically people are only going to use queue with a whole series.
			// and the download bar might not show all downloads, but who cares? i mean, i'll just have a cron job run it
			if err := handleSeriesDownload(ctx, args, assetDownloader, chromeMgr, saveDir); err != nil {
				slog.Error("Failed to handle series download from queue", "error", err, "url", args.Url)
			}
		}
		if err := scanner.Err(); err != nil {
			slog.Error("Error reading queue file", "error", err)
			os.Exit(1)
		}

		slog.Info("Finished processing queue file")
		os.Exit(0)
	}

	// Main work
	if args.Url != "" {
		if args.Extractor != "" {
			slog.Debug("Single download", "url", args.Url, "extractor", args.Extractor)
			if err := handleSingleDownload(ctx, args, assetDownloader, chromeMgr, saveDir); err != nil {
				slog.Error("Failed to handle single download", "error", err)
				os.Exit(1)
			}
			os.Exit(0)
		} else {
			slog.Debug("Series download", "url", args.Url)
			if err := handleSeriesDownload(ctx, args, assetDownloader, chromeMgr, saveDir); err != nil {
				slog.Error("Failed to handle series download", "error", err)
			}
		}
	} else {
		slog.Error("Please specify a URL")
		os.Exit(1)
	}
}

func handleSeriesDownload(ctx context.Context, args *cli.Args, d *download.Downloader, cm *chrome.ChromeManager, saveDir string) (err error) {
	dl, err := downloaders.GetDownloader(args.Url)
	if err != nil {
		slog.Error("Failed to get downloader", "error", err)
		return err
	}
	if dl == nil {
		slog.Error("No downloader supports this URL. Maybe use -e to specify an extractor for a single file?")
		return fmt.Errorf("no downloader supports this URL")
	}

	// Browser session for scraping
	scrapeCtx, cancel, err := cm.Get(ctx, !args.Browser, args.Debug)
	if err != nil {
		slog.Error("Failed to start browser", "error", err)
		return err
	}
	defer cancel()

	slog.Info("Fetching series info...")
	info, err := dl.GetSeriesInfo(scrapeCtx)
	if err != nil {
		slog.Error("Failed to get series info", "error", err)
		return err
	}
	slog.Info("Series", "title", info.Title)

	// maybe make this an option, idk.
	if args.QueueFile != "" {
		slog.Debug("Queue file there, doing special stuff")
		folderName := utils.CleanFolderName(info.Title)
		saveDir = filepath.Join(saveDir, folderName)
		slog.Info("Saving to", "directory", saveDir)

		if err := os.MkdirAll(saveDir, 0755); err != nil {
			slog.Error("Failed to create save directory", "error", err, "path", saveDir)
			return err
		}

	}

	manager := download.NewDownloadManager(d, args.ConcurrentDownloads, saveDir, *info, args.SkipExisting)
	taskChan := make(chan *downloaders.DownloadTaskWrapper, 50)

	// Start manager in background
	var wg sync.WaitGroup
	wg.Add(1)
	var managerErr error

	go func() {
		defer wg.Done()
		managerErr = manager.ProgressDownloads(ctx)
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

	defer func() {
		close(taskChan)
		wg.Wait()
	}()

	seriesNameForCache := download.PrepareSeriesNameForFile(info.Title)
	cache, _ := download.NewDirectoryCache(saveDir)

	settings := downloaders.DownloadSettings{
		SkipExisting: args.SkipExisting,
		CheckIfExists: func(season, episode, maxEpisodes uint32, videoType *downloaders.VideoType) bool {
			if !args.SkipExisting || cache == nil {
				return false
			}

			// If videoType is nil, check by prefix using a dummy videoType and trimming it
			if videoType == nil {
				epInfo := downloaders.EpisodeInfo{Season: season, Episode: episode, MaxEpisodes: maxEpisodes}
				// We build the name with no videoType and no title for a clean prefix
				prefix := download.GetEpisodeName(seriesNameForCache, nil, &epInfo, false)
				return cache.HasPrefix(prefix)
			}

			epInfo := downloaders.EpisodeInfo{Season: season, Episode: episode, MaxEpisodes: maxEpisodes}
			outputName := download.GetEpisodeName(seriesNameForCache, videoType, &epInfo, false)
			return cache.CheckIfEpisodeExists(outputName)
		},
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
		return err
	}

	slog.Info("Done!")

	return managerErr
}

func handleSingleDownload(ctx context.Context, args *cli.Args, d *download.Downloader, cm *chrome.ChromeManager, saveDir string) error {
	slog.Info("Extracting video URL...", "url", args.Url)

	// If it needs chrome (complex extractors), we would handle that here.
	// For simple extractors like Vidoza:
	ext, err := extractors.ExtractVideoUrl(ctx, args.Url, "", "")
	if err != nil {
		slog.Error("Failed to extract video URL", "error", err)
		return err
	}
	if ext == nil {
		slog.Error("No extractor supported this URL")
		return fmt.Errorf("no extractor supported this URL")
	}

	timestamp := time.Now().Format("2006-01-02_15-04-05.000")
	outputPath := filepath.Join(saveDir, timestamp)

	task := download.NewDownloadTask(outputPath, ext.Url).
		SetSkipExisting(args.SkipExisting).
		SetReferer(ext.Referer)

	slog.Info("Starting download...", "url", ext.Url)
	if err := d.DownloadToFile(ctx, task); err != nil {
		slog.Error("Download failed", "error", err)
		return err
	}

	d.Wait()
	return nil
}
