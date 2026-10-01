package main

import (
	"os"
	"path/filepath"
	"testing"

	"bugmaschine/gad/pkg/utils"
)

func TestQueueSaveDirDoesNotCreateSeriesDirectory(t *testing.T) {
	baseDir := t.TempDir()
	matcher, err := utils.NewSimilarFolderMatcher(baseDir)
	if err != nil {
		t.Fatalf("NewSimilarFolderMatcher returned error: %v", err)
	}

	saveDir, err := queueSaveDir(baseDir, "Example Series", matcher)
	if err != nil {
		t.Fatalf("queueSaveDir returned error: %v", err)
	}
	if saveDir != filepath.Join(baseDir, "Example Series") {
		t.Fatalf("unexpected save directory: %s", saveDir)
	}
	if _, err := os.Stat(saveDir); !os.IsNotExist(err) {
		t.Fatalf("expected series directory not to exist, stat error: %v", err)
	}
}
