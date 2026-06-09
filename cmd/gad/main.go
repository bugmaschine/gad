package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"bugmaschine/gad/internal/downloaders"
	"bugmaschine/gad/internal/extractors"
	"bugmaschine/gad/pkg/chrome"
	"bugmaschine/gad/pkg/cli"
	"bugmaschine/gad/pkg/dirs"
	"bugmaschine/gad/pkg/download"
	"bugmaschine/gad/pkg/ffmpeg"
	"bugmaschine/gad/pkg/logger"
	"bugmaschine/gad/pkg/utils"
)

func main() {
	os.Exit(run())
}

func run() int {
	args := &cli.Args{}
	rootCmd := cli.NewRootCommand(args)

	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	if args.Debug {
		fmt.Println("Debug mode enabled")
		if args.LogFile == "" {
			args.LogFile = "debug.log"
			fmt.Printf("Logging to file: %s\n", args.LogFile)
		}
	}

	// Set up logger
	logger.InitDefaultLogger(args.Debug, args.LogFile)
	defer logger.Close()

	// pull date and username for logging context
	time := time.Now().Format("2006-01-02 15:04:05")
	isRanAsAdmin := utils.IsExecutedAsAdmin()
	slog.Info("gad started at " + time + " with admin privileges: " + strconv.FormatBool(isRanAsAdmin))

	// Create data dir
	dataDir, err := dirs.GetDataDir()
	if err != nil {
		slog.Error("Failed to create data directory", "error", err)
		return 1
	}

	// Get save directory
	saveDir, err := dirs.GetSaveDirectory(args.OutputFolder)
	if err != nil {
		slog.Error("Failed to get save directory", "error", err)
		return 1
	}

	ctx, stop := interruptContext(context.Background())
	defer stop()

	// Rate limit parsing
	rateLimit, err := cli.ParseRateLimit(args.LimitRate)
	if err != nil {
		slog.Error("Failed to parse rate limit", "error", err)
		return 1
	}

	// Downloader for assets (FFmpeg, uBlock)
	assetDownloader := download.NewDownloader("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/148.0.0.0 Safari/537.36", args.Debug, rateLimit)

	// Create FFmpeg manager
	ff := ffmpeg.New(dataDir)

	// Auto-download FFmpeg
	slog.Info("Checking for FFmpeg...")
	ffmpegPath, err := ff.AutoDownload(ctx, assetDownloader)
	if err != nil {
		if isCancellation(ctx, err) {
			assetDownloader.Shutdown()
			return 130
		}
		slog.Error("Failed to manage FFmpeg", "error", err)
		assetDownloader.Shutdown()
		return 1
	}
	slog.Info("Using FFmpeg at", "path", ffmpegPath)
	assetDownloader.SetFfmpegPath(ffmpegPath)

	// Chrome management
	chromeMgr := chrome.NewManager(dataDir, assetDownloader)

	runner := &runner{
		ctx:        ctx,
		args:       args,
		downloader: assetDownloader,
		chromeMgr:  chromeMgr,
		saveDir:    saveDir,
	}
	if err := runner.Run(); err != nil {
		if isCancellation(ctx, err) {
			assetDownloader.Shutdown()
			return 130
		}
		slog.Error("gad failed", "error", err)
		assetDownloader.Shutdown()
		return 1
	}
	assetDownloader.Close()
	return 0
}

func interruptContext(parent context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)
	signals := make(chan os.Signal, 2)
	done := make(chan struct{})

	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)

	go func() {
		defer close(done)

		sig, ok := <-signals
		if !ok {
			return
		}

		slog.Warn("Shutdown requested, finishing cleanup. Press Ctrl+C again to force exit.", "signal", sig)
		cancel()

		sig, ok = <-signals
		if !ok {
			return
		}

		slog.Error("Second interrupt received, forcing exit", "signal", sig)
		os.Exit(130)
	}()

	return ctx, func() {
		signal.Stop(signals)
		close(signals)
		cancel()
		<-done
	}
}

func isCancellation(ctx context.Context, err error) bool {
	return ctx.Err() != nil ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded)
}

type runner struct {
	ctx        context.Context
	args       *cli.Args
	downloader *download.Downloader
	chromeMgr  *chrome.ChromeManager
	saveDir    string
}

func (r *runner) Run() error {
	if r.args.QueueFile != "" {
		return r.runQueue()
	}

	if r.args.Url == "" {
		return fmt.Errorf("please specify a URL")
	}

	scrapeCtx, cancel, err := r.startBrowser()
	if err != nil {
		return err
	}
	defer cancel()

	if r.args.Extractor != "" {
		slog.Debug("Single download", "url", r.args.Url, "extractor", r.args.Extractor)
		return handleSingleDownload(r.ctx, scrapeCtx, r.args, r.downloader, r.saveDir)
	}

	slog.Debug("Series download", "url", r.args.Url)
	return handleSeriesDownload(r.ctx, r.args, r.downloader, r.saveDir, scrapeCtx)
}

func (r *runner) runQueue() error {
	return handleQueueDownloads(r.ctx, r.args, r.downloader, r.chromeMgr, r.saveDir)
}

func (r *runner) startBrowser() (context.Context, context.CancelFunc, error) {
	scrapeCtx, cancel, err := r.chromeMgr.Get(r.ctx, !r.args.Browser, r.args.Debug)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to start browser: %w", err)
	}
	return scrapeCtx, cancel, nil
}

func readQueueURLs(scanner *bufio.Scanner) ([]string, error) {
	var urls []string
	seen := make(map[string]struct{})

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		slog.Debug("Processing line from queue", "line", line)

		if line == "" || strings.HasPrefix(line, "#") {
			slog.Debug("Skipping invalid or commented line", "line", line)
			continue
		}

		if commentIndex := strings.Index(line, "#"); commentIndex >= 0 {
			line = strings.TrimSpace(line[:commentIndex])
			slog.Debug("Removed comment from line", "line", line)
		}
		if line == "" {
			continue
		}

		if _, ok := seen[line]; ok {
			slog.Debug("Skipping duplicate URL", "url", line)
			continue
		}

		seen[line] = struct{}{}
		urls = append(urls, line)
		slog.Debug("Added URL from queue", "url", line)
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return urls, nil
}

func handleQueueDownloads(ctx context.Context, args *cli.Args, d *download.Downloader, cm *chrome.ChromeManager, saveDir string) error {
	slog.Info("Queue file specified", "file", args.QueueFile)

	queueFile, err := os.Open(args.QueueFile)
	if err != nil {
		return err
	}
	defer queueFile.Close()
	// i think theres a more elegant way. but i'm too lazy to refractor this again.
	scanner := bufio.NewScanner(queueFile)
	urls, err := readQueueURLs(scanner)
	if err != nil {
		return err
	}

	scrapeCtx, cancel, err := cm.Get(ctx, !args.Browser, args.Debug)
	if err != nil {
		return fmt.Errorf("failed to start browser: %w", err)
	}
	defer cancel()

	hadError := false
	for _, url := range urls {
		if err := ctx.Err(); err != nil {
			return err
		}

		entryArgs := *args
		entryArgs.SkipExisting = true
		entryArgs.Url = url
		slog.Info("Processing URL from queue", "url", entryArgs.Url)

		if err := handleSeriesDownload(ctx, &entryArgs, d, saveDir, scrapeCtx); err != nil {
			if isCancellation(ctx, err) {
				return err
			}
			hadError = true
			slog.Error("Failed to handle series download from queue", "error", err, "url", entryArgs.Url)
		}
	}

	slog.Info("Finished processing queue file")
	if hadError {
		return fmt.Errorf("one or more queue entries failed")
	}
	return nil
}

func handleSeriesDownload(ctx context.Context, args *cli.Args, d *download.Downloader, saveDir string, scrapeCtx context.Context) (err error) {
	dl, err := downloaders.GetDownloader(args.Url)
	if err != nil {
		slog.Error("Failed to get downloader", "error", err)
		return err
	}
	if dl == nil {
		slog.Error("No downloader supports this URL. Maybe use -e to specify an extractor for a single file?")
		return fmt.Errorf("no downloader supports this URL")
	}

	slog.Info("Fetching series info...")
	info, err := dl.GetSeriesInfo(scrapeCtx)
	if err != nil {
		if isCancellation(ctx, err) {
			return err
		}
		slog.Error("Failed to get series info", "error", err)
		return err
	}
	slog.Info("Series", "title", info.Title)

	if args.QueueFile != "" {
		saveDir, err = queueSaveDir(saveDir, info.Title)
		if err != nil {
			return err
		}
	}

	manager := download.NewDownloadManager(d, args.ConcurrentDownloads, saveDir, *info, args.SkipExisting)
	taskChan := make(chan *downloaders.DownloadTaskWrapper, 50)

	managerErrCh := make(chan error, 1)
	go func() {
		managerErrCh <- manager.ProgressDownloads(ctx)
	}()

	feederErrCh := make(chan error, 1)
	go func() {
		var feederErr error
		defer func() {
			manager.Close()
			feederErrCh <- feederErr
		}()
		for tw := range taskChan {
			err := manager.Submit(ctx, download.ManagerTask{
				DownloadUrl:        tw.Url,
				Referer:            tw.Referer,
				UserAgent:          tw.UserAgent,
				VideoType:          tw.Lang,
				EpisodeInfo:        tw.Episode,
				OnDownloadStart:    tw.OnDownloadStart,
				OnDownloadComplete: tw.OnDownloadComplete,
			})
			if err != nil {
				feederErr = err
				for range taskChan {
				}
				return
			}
		}
	}()

	waitForDownloads := func() error {
		close(taskChan)
		feederErr := <-feederErrCh
		managerErr := <-managerErrCh
		if feederErr != nil {
			return feederErr
		}
		return managerErr
	}

	seriesNameForCache := download.PrepareSeriesNameForFile(info.Title)
	cache, _ := download.NewDirectoryCache(saveDir)

	settings := downloaders.DownloadSettings{
		DdosWaitEpisodes: nonNegativeUint32(args.DdosWaitEpisodes),
		DdosWaitMs:       args.DdosWaitMs,
		SkipExisting:     args.SkipExisting,
		CheckIfExists: func(season, episode, maxEpisodes uint32, videoType *downloaders.VideoType) bool {
			if !args.SkipExisting || cache == nil {
				return false
			}

			if videoType == nil {
				epInfo := downloaders.EpisodeInfo{Season: season, Episode: episode, MaxEpisodes: maxEpisodes}
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
		Language:      args.GetVideoType(),
		Episodes:      args.GetEpisodesRequest(),
	}

	slog.Info("Starting scrape...")
	scrapeErr := dl.Download(scrapeCtx, req, settings, taskChan)
	managerErr := waitForDownloads()

	if scrapeErr != nil {
		if isCancellation(ctx, scrapeErr) {
			return scrapeErr
		}
		slog.Error("Scrape failed", "error", scrapeErr)
		return scrapeErr
	}
	if managerErr != nil {
		if isCancellation(ctx, managerErr) {
			return managerErr
		}
		return managerErr
	}

	slog.Info("Done!")

	return nil
}

func nonNegativeUint32(v int) uint32 {
	if v < 0 {
		return 0
	}
	return uint32(v)
}

func queueSaveDir(baseDir, seriesTitle string) (string, error) {
	slog.Debug("Downloading in Queue file mode")

	if err := os.MkdirAll(baseDir, 0755); err != nil {
		slog.Error("Failed to create save directory", "error", err, "path", baseDir)
		return "", err
	}

	folderName := utils.CleanFolderName(seriesTitle)
	similarFolder, err := utils.FindSimilarFolder(baseDir, folderName)
	if err != nil {
		slog.Warn("No similar folder found, will create new one", "folder", folderName, "error", err)
	} else {
		slog.Debug("found similar folder", "similarFolder", similarFolder)
	}

	if similarFolder != "" {
		slog.Info("Found similar folder, using it", "folder", similarFolder)
		slog.Info("Saving to", "directory", similarFolder)
		return similarFolder, nil
	}

	saveDir := filepath.Join(baseDir, folderName)
	slog.Info("No similar folder found, will create new one", "folder", folderName)
	slog.Info("Saving to", "directory", saveDir)

	if err := os.MkdirAll(saveDir, 0755); err != nil {
		slog.Error("Failed to create save directory", "error", err, "path", saveDir)
		return "", err
	}
	return saveDir, nil
}

func handleSingleDownload(ctx, extractCtx context.Context, args *cli.Args, d *download.Downloader, saveDir string) error {
	slog.Info("Extracting video URL...", "url", args.Url)

	var ext *extractors.ExtractedVideo
	var err error
	if args.Extractor != "" {
		ext, err = extractors.ExtractVideoUrlWithExtractor(extractCtx, args.Url, args.Extractor, "", "")
	} else {
		ext, err = extractors.ExtractVideoUrl(extractCtx, args.Url, "", "")
	}
	if err != nil {
		if isCancellation(ctx, err) {
			return err
		}
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
		SetReferer(ext.Referer).
		SetUserAgent(ext.UserAgent).
		SetOnStart(ext.OnDownloadStart).
		SetOnComplete(ext.OnDownloadComplete)

	slog.Info("Starting download...", "url", ext.Url)
	if err := d.DownloadToFile(ctx, task); err != nil {
		if isCancellation(ctx, err) {
			return err
		}
		slog.Error("Download failed", "error", err)
		return err
	}

	return nil
}
