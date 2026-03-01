package chrome

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/bugmaschine/gad/pkg/download"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/chromedp"
)

const (
	UblockGithubAPIURL        = "https://api.github.com/repos/uBlockOrigin/uBOL-home/releases/latest"
	UblockFallbackDownloadURL = "https://github.com/uBlockOrigin/uBOL-home/releases/download/2026.215.1801/uBOLite_2026.215.1801.chromium.zip"
)

const (
	ChromiumBaseURL = "https://www.googleapis.com/download/storage/v1/b/chromium-browser-snapshots/o/%s%%2F%s%%2F%s?alt=media"
	LastChangeURL   = "https://www.googleapis.com/download/storage/v1/b/chromium-browser-snapshots/o/%s%%2FLAST_CHANGE?alt=media"
)

var (
	checkedForUblockUpdates = false
	checkedForChromeUpdates = false
)

// Downloader matches the interface needed for uBlock download.
type Downloader interface {
	DownloadToFile(ctx context.Context, task *download.DownloadTask) error
}

type ChromeManager struct {
	dataDir    string
	downloader Downloader
}

func NewManager(dataDir string, downloader Downloader) *ChromeManager {
	return &ChromeManager{
		dataDir:    dataDir,
		downloader: downloader,
	}
}

// Get initializes a chromedp context with uBlock Origin and anti-automation patches.
func (m *ChromeManager) Get(ctx context.Context, headless, debug bool) (context.Context, context.CancelFunc, error) {
	chromeExecPath, err := m.prepareChromium()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to prepare chromium: %w", err)
	}

	ublockDir := filepath.Join(m.dataDir, "uBlock")
	if err := m.prepareUblock(ctx, ublockDir); err != nil {
		slog.Warn("Failed to prepare uBlock Origin, proceeding without it", "error", err)
	}

	opts := []chromedp.ExecAllocatorOption{
		chromedp.ExecPath(chromeExecPath),
		chromedp.NoDefaultBrowserCheck,
		chromedp.NoFirstRun,
		chromedp.DisableGPU, // Safer across platforms
	}

	if headless && !debug {
		// apparently the new headless mode is good??
		opts = append(opts, chromedp.Flag("headless", "new"))
	}

	opts = append(opts,
		//chromedp.Flag("no-sandbox", true), // this is absolutely dangerous. i think sdl did this for performance reasons.
		chromedp.Flag("disable-dev-shm-usage", true),
		chromedp.Flag("disable-blink-features", "AutomationControlled"),
		chromedp.UserAgent("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/121.0.0.0 Safari/537.36"),
		chromedp.WindowSize(1920, 1080),
		chromedp.Flag("disable-infobars", true),
		chromedp.Flag("exclude-switches", "enable-automation,enable-logging"),
	)

	effectiveUblockDir, err := m.getUblockDirectory(ublockDir)
	if err == nil {
		opts = append(opts, chromedp.Flag("load-extension", effectiveUblockDir))
	} else {
		slog.Warn("Failed to add uBlock Origin extension", "error", err)
	}

	allocCtx, allocCancel := chromedp.NewExecAllocator(ctx, opts...)

	var contextOpts []chromedp.ContextOption
	if debug {
		contextOpts = append(contextOpts,
			chromedp.WithLogf(func(s string, i ...interface{}) { slog.Debug(fmt.Sprintf(s, i...)) }),
		)
	}

	// Create context
	taskCtx, taskCancel := chromedp.NewContext(allocCtx, contextOpts...)

	// Combine cancels
	combinedCancel := func() {
		taskCancel()
		allocCancel()
	}

	// Apply anti-automation patches
	err = chromedp.Run(taskCtx,
		chromedp.ActionFunc(func(ctx context.Context) error {
			script := `
				Object.defineProperty(window, "navigator", {
					value: new Proxy(navigator, {
						has: (target, key) => (key === "webdriver" ? false : key in target),
						get: (target, key) =>
						key === "webdriver"
							? false
							: typeof target[key] === "function"
							? target[key].bind(target)
							: target[key],
					}),
				});
			`
			_, err := page.AddScriptToEvaluateOnNewDocument(script).Do(ctx)
			return err
		}),
	)
	if err != nil {
		combinedCancel()
		return nil, nil, fmt.Errorf("browser failed to start or patches failed: %w", err)
	}

	return taskCtx, combinedCancel, nil
}

func (m *ChromeManager) prepareChromium() (string, error) {
	// check if chromium is installed locally. it may not be the newest version, but i think the system admin will keep it up to date.
	for _, bin := range []string{"chromium", "chromium-browser", "google-chrome", "google-chrome-stable"} {
		if path, err := exec.LookPath(bin); err == nil {
			slog.Debug("Using system chromium", "path", path)
			return path, nil
		}
	}

	platform, zipName, execRelPath := getPlatformInfo()
	chromeDir := filepath.Join(m.dataDir, "chromium_bin")
	slog.Debug("Chromium directory", "dir", chromeDir)
	fullExecPath := filepath.Join(chromeDir, execRelPath)
	slog.Debug("Chromium executable path", "path", fullExecPath)
	versionFile := filepath.Join(m.dataDir, "current_chromium_version")

	if checkedForChromeUpdates {
		if _, err := os.Stat(fullExecPath); err == nil {
			return fullExecPath, nil
		}
	}

	slog.Debug("Checking for Chromium snapshot updates...")

	// ask google for the latest revision
	slog.Debug("Fetching latest Chromium revision from", "url", fmt.Sprintf(LastChangeURL, platform))
	resp, err := http.Get(fmt.Sprintf(LastChangeURL, platform))
	var latestRemoteRevision string
	if err == nil {
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		latestRemoteRevision = strings.TrimSpace(string(body))
	} else {
		slog.Warn("Failed to fetch latest Chromium revision", "error", err)
	}

	// check if we already have the latest version
	currentVersionBytes, _ := os.ReadFile(versionFile)
	currentLocalRevision := strings.TrimSpace(string(currentVersionBytes))

	// convert to int for comparison to prevent downgrades, because google cant manage to return a consistent revision number
	latestRevInt, _ := strconv.Atoi(latestRemoteRevision)
	currentRevInt, _ := strconv.Atoi(currentLocalRevision)
	slog.Debug("Chromium versions", "local", currentRevInt, "remote", latestRevInt)

	if latestRemoteRevision != "" && currentRevInt >= latestRevInt {
		if _, err := os.Stat(fullExecPath); err == nil {
			slog.Debug("Chromium up-to-date (or newer)", "local", currentRevInt, "remote", latestRevInt)
			checkedForChromeUpdates = true

			return fullExecPath, nil
		}
	}

	// If network failed but we have an existing installation, use it as fallback
	if latestRemoteRevision == "" {
		if _, err := os.Stat(fullExecPath); err == nil {
			slog.Warn("Could not check for Chromium updates, using existing installation")
			checkedForChromeUpdates = true
			return fullExecPath, nil
		}
		return "", fmt.Errorf("chromium not found and could not fetch latest revision")
	}
	slog.Info("Downloading chromium...", "revision", latestRemoteRevision)

	// download from google
	downloadURL := fmt.Sprintf(ChromiumBaseURL, platform, latestRemoteRevision, zipName)
	tmpZip := filepath.Join(m.dataDir, "chrome_temp.zip")

	task := download.NewDownloadTask(tmpZip, downloadURL).
		SetOverwriteFile(true).
		SetCustomMessage("Downloading Chromium")
	task.OutputPathHasExtension = true

	if err := m.downloader.DownloadToFile(context.Background(), task); err != nil {
		return "", err
	}
	defer os.Remove(tmpZip)

	// clean up
	_ = os.RemoveAll(chromeDir)
	if err := m.unzip(tmpZip, chromeDir); err != nil {
		return "", err
	}

	// set execute stuff
	if runtime.GOOS != "windows" {
		os.Chmod(fullExecPath, 0755)
	}

	// save revision
	_ = os.WriteFile(versionFile, []byte(latestRemoteRevision), 0644)
	checkedForChromeUpdates = true

	return fullExecPath, nil
}

// getPlatformInfo returns (PlatformSegment, ZipName, ExecutableSubPath)
func getPlatformInfo() (string, string, string) {
	switch runtime.GOOS {
	case "windows":
		return "Win_x64", "chrome-win.zip", filepath.Join("chrome-win", "chrome.exe")
	case "darwin":
		return "Mac", "chrome-mac.zip", filepath.Join("chrome-mac", "Chromium.app", "Contents", "MacOS", "Chromium")
	default: // Linux
		return "Linux_x64", "chrome-linux.zip", filepath.Join("chrome-linux", "chrome")
	}
}

func (m *ChromeManager) prepareUblock(ctx context.Context, ublockDir string) error {
	versionFile := filepath.Join(m.dataDir, "current_ublock_version")

	currentVersionBytes, _ := os.ReadFile(versionFile)
	currentVersion := strings.TrimSpace(string(currentVersionBytes))
	slog.Debug("local uBlock version", "version", currentVersion)

	// if we have a version locally, and already checked for updates, we can skip the check. (i had the issue of the github api blocking me)
	if currentVersion != "" && checkedForUblockUpdates == true {
		return nil
	}

	latestTag, downloadURL, err := m.fetchLatestUblockInfo()
	if err != nil {
		slog.Warn("Failed to fetch latest uBlock info from GitHub, using fallback", "error", err)
		latestTag = "fallback"
		downloadURL = UblockFallbackDownloadURL
	}

	if currentVersion != "" && currentVersion == latestTag && latestTag != "fallback" {
		if _, err := os.Stat(ublockDir); err == nil {
			slog.Debug("uBlock Origin up-to-date", "version", latestTag)
			checkedForUblockUpdates = true
			return nil
		}
	}

	slog.Info("Installing uBlock Origin", "version", latestTag)

	if err := os.MkdirAll(m.dataDir, 0755); err != nil {
		return err
	}

	zipPath := filepath.Join(m.dataDir, "uBlock.zip")

	resp, err := http.Get(downloadURL)
	if err != nil {
		return fmt.Errorf("failed to download uBlock: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status from github: %s", resp.Status)
	}

	out, err := os.Create(zipPath)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, resp.Body)
	out.Close()
	if err != nil {
		return err
	}
	defer os.Remove(zipPath)

	_ = os.RemoveAll(ublockDir)
	if err := os.MkdirAll(ublockDir, 0755); err != nil {
		return err
	}

	if err := m.unzip(zipPath, ublockDir); err != nil {
		return fmt.Errorf("failed to unzip uBlock: %w", err)
	}

	_ = os.WriteFile(versionFile, []byte(latestTag), 0644)
	checkedForUblockUpdates = true
	return nil
}

func (m *ChromeManager) fetchLatestUblockInfo() (string, string, error) {
	resp, err := http.Get(UblockGithubAPIURL)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	var release struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", "", err
	}

	var downloadURL string
	for _, asset := range release.Assets {
		if strings.Contains(asset.Name, "chromium") {
			downloadURL = asset.BrowserDownloadURL
			break
		}
	}

	if release.TagName == "" || downloadURL == "" {
		return "", "", fmt.Errorf("missing tag or download url in github response")
	}

	return release.TagName, downloadURL, nil
}

func (m *ChromeManager) unzip(src, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer r.Close()

	for _, f := range r.File {
		fpath := filepath.Join(dest, f.Name)
		if f.FileInfo().IsDir() {
			os.MkdirAll(fpath, os.ModePerm)
			continue
		}

		if err := os.MkdirAll(filepath.Dir(fpath), os.ModePerm); err != nil {
			return err
		}

		outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			return err
		}

		rc, err := f.Open()
		if err != nil {
			outFile.Close()
			return err
		}

		_, err = io.Copy(outFile, rc)
		outFile.Close()
		rc.Close()

		if err != nil {
			return err
		}
	}
	return nil
}

func (m *ChromeManager) getUblockDirectory(ublockDir string) (string, error) {
	entries, err := os.ReadDir(ublockDir)
	if err != nil {
		return "", err
	}

	if len(entries) == 0 {
		return "", fmt.Errorf("ublock directory is empty")
	}

	if len(entries) == 1 && entries[0].IsDir() {
		return filepath.Join(ublockDir, entries[0].Name()), nil
	}

	return ublockDir, nil
}

// GetUserAgent returns the user agent string of the current browser.
func GetUserAgent(ctx context.Context) (string, error) {
	var ua string
	err := chromedp.Run(ctx,
		chromedp.Evaluate("navigator.userAgent", &ua),
	)
	return ua, err
}
