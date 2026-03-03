package utils

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// RemoveFileIgnoreNotExists removes a file, ignoring the error if it doesn't exist.
func RemoveFileIgnoreNotExists(path string) error {
	err := os.Remove(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func FindSimilarFolder(baseDir, target string) (string, error) {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return "", err
	}

	targetNorm := normalize(target)
	targetTokens := strings.Fields(targetNorm)

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		entryNorm := normalize(e.Name())
		entryTokens := strings.Fields(entryNorm)

		if tokensOverlap(entryTokens, targetTokens) {
			return filepath.Join(baseDir, e.Name()), nil
		}
	}

	return "", fmt.Errorf("Not found")
}

func tokensOverlap(a, b []string) bool {
	set := map[string]struct{}{}
	for _, x := range a {
		set[x] = struct{}{}
	}
	for _, x := range b {
		if _, ok := set[x]; ok {
			return true
		}
	}
	return false
}

func normalize(s string) string {
	s = CleanSearchName(s)
	s = strings.ToLower(s)
	s = strings.TrimSpace(s)
	return s
}

// RemoveDirAllIgnoreNotExists removes a directory and all its contents, ignoring the error if it doesn't exist.
func RemoveDirAllIgnoreNotExists(path string) error {
	err := os.RemoveAll(path)
	// os.RemoveAll handles non-existence gracefully and returns nil, but we check for clarity.
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func CleanFolderName(rawName string) string {
	// i had a script that used sdl to download stuff (basically the queue feature, but more manual), and to make it backwards compatible to that script, i made it clean the titles in a similar way.
	name := strings.TrimSpace(rawName)

	dotReplace := regexp.MustCompile(`[:.]`)
	name = dotReplace.ReplaceAllString(name, " - ")

	illegalChars := regexp.MustCompile(`[\\/*?"<>|]`)
	name = illegalChars.ReplaceAllString(name, "")

	multiSpace := regexp.MustCompile(`\s+`)
	name = multiSpace.ReplaceAllString(name, " ")

	//name = strings.Trim(name, ". ")

	return name
}

func CleanSearchName(rawName string) string {
	// i had a script that used sdl to download stuff (basically the queue feature, but more manual), and to make it backwards compatible to that script, i made it clean the titles in a similar way.
	name := strings.TrimSpace(rawName)

	dotReplace := regexp.MustCompile(`[:.]`)
	name = dotReplace.ReplaceAllString(name, " ")

	illegalChars := regexp.MustCompile(`[\\/*?#"<>|]`)
	name = illegalChars.ReplaceAllString(name, " ")

	multiSpace := regexp.MustCompile(`\s+`)
	name = multiSpace.ReplaceAllString(name, " ")

	return name
}
