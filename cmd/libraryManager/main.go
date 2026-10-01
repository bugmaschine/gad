package main

import (
	"bufio"
	"bugmaschine/gad/internal/downloaders"
	"bugmaschine/gad/pkg/chrome"
	"bugmaschine/gad/pkg/dirs"
	"bugmaschine/gad/pkg/download"
	"bugmaschine/gad/pkg/logger"
	"bugmaschine/gad/pkg/utils"
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("expected a subcommand: 'scan'")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "scan":
		runScan()

	default:
		fmt.Printf("unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
}

func runScan() int {
	scanCmd := flag.NewFlagSet("scan", flag.ExitOnError)

	debug := scanCmd.Bool("debug", false, "Enable debug logging")
	queueFile := scanCmd.String("queue", "", "Path to the queue file containing URLs to check")
	folderMatchDownloadFolder := scanCmd.String("folderMatchDownloadFolder", "", "Path to the download folder to check for existing series (required if -folderMatch is set)")
	resultOutputFile := scanCmd.String("resultOutputFile", "", "Path to the output file where results will be written (optional)")
	scanCmd.Parse(os.Args[2:])

	logger.InitDefaultLogger(*debug, *resultOutputFile)
	defer logger.Close()

	if *queueFile == "" {
		fmt.Fprintln(os.Stderr, "Error: -queue is required")
		scanCmd.Usage()
		return 1
	}

	if *folderMatchDownloadFolder == "" {
		fmt.Fprintln(os.Stderr, "Error: -folderMatchDownloadFolder is required when scanning")
		scanCmd.Usage()
		return 1
	}

	if err := folderMatchMain(*queueFile, *folderMatchDownloadFolder, *debug); err != nil {
		slog.Error("Folder match failed", "error", err)
		return 1
	}
	return 0

}

func folderMatchMain(queueFile, downloadFolder string, debug bool) error {
	f, err := os.Open(queueFile)
	if err != nil {
		return err
	}
	urls, err := utils.LoadQueueURLs(bufio.NewScanner(f))
	f.Close()
	if err != nil {
		return err
	}

	downloadFolderAbs, err := filepath.Abs(downloadFolder)
	if err != nil {
		return err
	}

	var (
		foldersNotInDownloadFolder []string // queued series with no matching folder on disk
		foldersInDownloadFolder    []string // queued series that DO have a matching folder
		matchedFolders             = make(map[string]bool)
		matcher                    *utils.SimilarFolderMatcher
	)

	// we basically use the matcher to check if a folder exists in the download folder that is similar to the series name in the URL
	matcher, err = utils.NewSimilarFolderMatcher(downloadFolderAbs)
	if err != nil {
		return err
	}

	dataDir, err := dirs.GetDataDir()
	if err != nil {
		slog.Error("Failed to create data directory", "error", err)
	}

	// because we need to get info, we have to get chromium. i mean. theres worse i guess?
	assetDownloader := download.NewDownloader("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/148.0.0.0 Safari/537.36", debug, 0)
	defer assetDownloader.Shutdown()

	ctx := context.Background()

	chromeMgr := chrome.NewManager(dataDir, assetDownloader)

	slog.Info("Starting browser...")
	headless := true // todo: make this settable via flag.
	scrapeCtx, cancel, err := chromeMgr.Get(ctx, headless, debug)
	if err != nil {
		panic("Failed to start browser" + err.Error())
	}
	defer cancel()

	for _, url := range urls {
		dl, err := downloaders.GetDownloader(url)
		if err != nil {
			slog.Error("Failed to get downloader", "error", err)
			return err
		}
		if dl == nil {
			slog.Error("No downloader supports this URL. Maybe use -e to specify an extractor for a single file?")
			return fmt.Errorf("no downloader supports this URL")
		}

		info, err := dl.GetSeriesInfo(scrapeCtx)
		if err != nil {
			slog.Error("Failed to get series info", "url", url, "error", err)
			continue
		}

		seriesName := download.PrepareSeriesNameForFile(info.Title)
		if seriesName == "" {
			slog.Warn("Failed to extract series name from URL", "url", url)
			continue
		}

		match, err := matcher.Find(seriesName)
		if err != nil {
			slog.Debug("No similar folder found for series", "series", seriesName, "url", url)
			foldersNotInDownloadFolder = append(foldersNotInDownloadFolder, seriesName)
			continue
		}

		slog.Debug("stuffs", "seriesname", seriesName, "matcherResult", match)
		foldersInDownloadFolder = append(foldersInDownloadFolder, match)
		matchedFolders[filepath.Base(match)] = true

		// we probaby shouldnt ddos the site. but i'm too lazy to bring the ddos stuff over. so i just assume 100ms is enough to not get rate limited. if it is, then we can just increase this number.
		time.Sleep(100 * time.Millisecond)
	}

	// load folder names from the download folder
	entries, err := os.ReadDir(downloadFolderAbs)
	if err != nil {
		return err
	}

	var foldersInDownloadFolderNotInQueue []string
	for _, entry := range entries {
		if entry.IsDir() && !matchedFolders[entry.Name()] {
			foldersInDownloadFolderNotInQueue = append(foldersInDownloadFolderNotInQueue, entry.Name())
		}
	}

	printList("Folders in download folder", foldersInDownloadFolder)
	printList("Folders not in download folder", foldersNotInDownloadFolder)
	printList("Folders in download folder but not in queue", foldersInDownloadFolderNotInQueue)

	return nil
}

func printList(label string, items []string) {
	if len(items) == 0 {
		slog.Info(label, "count", 0)
		return
	}
	slog.Info(label, "count", len(items))
	for _, f := range items {
		slog.Info("folder", "label", label, "name", f)
	}
}
