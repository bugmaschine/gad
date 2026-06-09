package download

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"

	"bugmaschine/gad/internal/downloaders"
)

type ManagerTask struct {
	DownloadUrl        string
	Referer            string
	UserAgent          string
	Language           downloaders.Language
	VideoType          downloaders.VideoType
	EpisodeInfo        downloaders.EpisodeInfo
	OnDownloadStart    func(context.Context) (func(), error)
	OnDownloadComplete func(context.Context) error
}

type DownloadManager struct {
	downloader    *Downloader
	tasks         chan ManagerTask
	maxConcurrent int
	saveDir       string
	seriesInfo    downloaders.SeriesInfo
	skipExisting  bool
}

const managerTaskBuffer = 100

func NewDownloadManager(d *Downloader, maxConcurrent int, saveDir string, info downloaders.SeriesInfo, skip bool) *DownloadManager {
	if maxConcurrent <= 0 {
		maxConcurrent = 1
	}
	return &DownloadManager{
		downloader:    d,
		tasks:         make(chan ManagerTask, managerTaskBuffer),
		maxConcurrent: maxConcurrent,
		saveDir:       saveDir,
		seriesInfo:    info,
		skipExisting:  skip,
	}
}

func (m *DownloadManager) Submit(ctx context.Context, task ManagerTask) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	select {
	case m.tasks <- task:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (m *DownloadManager) Close() {
	close(m.tasks)
}

func (m *DownloadManager) ProgressDownloads(ctx context.Context) error {
	seriesName := PrepareSeriesNameForFile(m.seriesInfo.Title)
	cache, _ := NewDirectoryCache(m.saveDir)

	var wg sync.WaitGroup
	errChan := make(chan error, 1)

	for i := 0; i < m.maxConcurrent; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for task := range m.tasks {
				if err := m.downloadTask(ctx, seriesName, cache, task); err != nil {
					select {
					case errChan <- err:
					default:
					}
				}
			}
		}()
	}

	wg.Wait()

	select {
	case err := <-errChan:
		return err
	default:
		return ctx.Err()
	}
}

func (m *DownloadManager) downloadTask(ctx context.Context, seriesName string, cache *DirectoryCache, task ManagerTask) error {
	slog.Debug("Download manager received task", "url", task.DownloadUrl, "ep", task.EpisodeInfo)

	outputName := GetEpisodeName(seriesName, &task.VideoType, &task.EpisodeInfo, false)
	if m.skipExisting && cache != nil && cache.CheckIfEpisodeExists(outputName) {
		slog.Info("skipping download for file: already exists", "file", outputName)
		return nil
	}

	dt := NewDownloadTask(filepath.Join(m.saveDir, outputName), task.DownloadUrl).
		SetSkipExisting(m.skipExisting).
		SetReferer(task.Referer).
		SetUserAgent(task.UserAgent).
		SetOnStart(task.OnDownloadStart).
		SetOnComplete(task.OnDownloadComplete)

	if err := m.downloader.DownloadToFile(ctx, dt); err != nil {
		if isContextError(ctx, err) {
			return err
		}
		slog.Warn("Failed download", "file", outputName, "error", err)
		return fmt.Errorf("%s: %w", outputName, err)
	}

	slog.Debug("Download finished successfully", "file", outputName)
	return nil
}

func isContextError(ctx context.Context, err error) bool {
	return ctx.Err() != nil ||
		errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded)
}
