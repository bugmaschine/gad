package download

import (
	"context"
	"errors"
	"testing"

	"bugmaschine/gad/internal/downloaders"
)

func TestDownloadManagerSubmitReturnsContextErrorWhenQueueBlocked(t *testing.T) {
	manager := NewDownloadManager(nil, 1, t.TempDir(), downloaders.SeriesInfo{}, false)
	manager.tasks = make(chan ManagerTask)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := manager.Submit(ctx, ManagerTask{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled, got %v", err)
	}
}
