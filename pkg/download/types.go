package download

import (
	"context"
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
	UserAgent              string
	OnStart                func(context.Context) (func(), error)
	OnComplete             func(context.Context) error
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

func (t *DownloadTask) SetUserAgent(userAgent string) *DownloadTask {
	t.UserAgent = userAgent
	return t
}

func (t *DownloadTask) SetOnStart(onStart func(context.Context) (func(), error)) *DownloadTask {
	t.OnStart = onStart
	return t
}

func (t *DownloadTask) SetOnComplete(onComplete func(context.Context) error) *DownloadTask {
	t.OnComplete = onComplete
	return t
}

func (t *DownloadTask) Filename() string {
	return filepath.Base(t.OutputPath)
}
