package download

import (
	"context"
	"log/slog"
	"path/filepath"
	"sync"

	"github.com/bugmaschine/gad/internal/downloaders"
)

type ManagerTask struct {
	DownloadUrl string
	Referer     string
	Language    downloaders.Language
	VideoType   downloaders.VideoType
	EpisodeInfo downloaders.EpisodeInfo
}

type DownloadManager struct {
	downloader    *Downloader
	tasks         chan ManagerTask
	maxConcurrent int
	saveDir       string
	seriesInfo    downloaders.SeriesInfo
	skipExisting  bool
}

func NewDownloadManager(d *Downloader, maxConcurrent int, saveDir string, info downloaders.SeriesInfo, skip bool) *DownloadManager {
	if maxConcurrent <= 0 {
		maxConcurrent = 1
	}
	return &DownloadManager{
		downloader:    d,
		tasks:         make(chan ManagerTask, 100),
		maxConcurrent: maxConcurrent,
		saveDir:       saveDir,
		seriesInfo:    info,
		skipExisting:  skip,
	}
}

func (m *DownloadManager) Submit(task ManagerTask) {
	m.tasks <- task
}

func (m *DownloadManager) Close() {
	close(m.tasks)
}

func (m *DownloadManager) ProgressDownloads(ctx context.Context) error {
	seriesName := PrepareSeriesNameForFile(m.seriesInfo.Title)
	cache, _ := NewDirectoryCache(m.saveDir)

	var wg sync.WaitGroup
	sem := make(chan struct{}, m.maxConcurrent)
	errChan := make(chan error, 1)

	for task := range m.tasks {
		slog.Debug("Download manager received task", "url", task.DownloadUrl, "ep", task.EpisodeInfo)
		wg.Add(1)
		go func(t ManagerTask) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			outputName := GetEpisodeName(seriesName, &t.VideoType, &t.EpisodeInfo, false)

			if m.skipExisting && cache != nil && cache.CheckIfEpisodeExists(outputName) {
				slog.Info("skipping download for file: already exists", "file", outputName)
				slog.Debug("File exists check passed", "file", outputName)
				return
			}

			dt := NewDownloadTask(filepath.Join(m.saveDir, outputName), t.DownloadUrl).
				SetSkipExisting(m.skipExisting).
				SetReferer(t.Referer)

			if err := m.downloader.DownloadToFile(ctx, dt); err != nil {
				slog.Warn("Failed download", "file", outputName, "error", err)

				select {
				case errChan <- err:
				default:
				}
			} else {
				slog.Debug("Download finished successfully", "file", outputName)
			}
		}(task)
	}

	wg.Wait()

	select {
	case err := <-errChan:
		return err
	default:
		return nil
	}

}
