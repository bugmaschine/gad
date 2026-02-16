package download

import (
	"path/filepath"
)

type DownloadTask struct {
	Url                    string
	OutputPath             string
	OutputPathHasExtension bool
	OverwriteFile          bool
	SkipExisting           bool
	CustomMessage          string
	Referer                string
}

func NewDownloadTask(outputPath, url string) *DownloadTask {
	return &DownloadTask{
		Url:        url,
		OutputPath: outputPath,
	}
}

func (t *DownloadTask) SetOverwriteFile(overwrite bool) *DownloadTask {
	t.OverwriteFile = overwrite
	return t
}

func (t *DownloadTask) SetSkipExisting(skip bool) *DownloadTask {
	t.SkipExisting = skip
	return t
}

func (t *DownloadTask) SetCustomMessage(message string) *DownloadTask {
	t.CustomMessage = message
	return t
}

func (t *DownloadTask) SetReferer(referer string) *DownloadTask {
	t.Referer = referer
	return t
}

func (t *DownloadTask) Filename() string {
	return filepath.Base(t.OutputPath)
}
