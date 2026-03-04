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
	var bestMatch string
	var highestScore float64 = 0.0

	// Minimum threshold to consider a match "good"
	const threshold = 0.4

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		entryName := e.Name()
		entryNorm := normalize(entryName)

		// Calculate similarity (0.0 to 1.0)
		score := calculateSimilarity(targetNorm, entryNorm)

		if score > highestScore {
			highestScore = score
			bestMatch = filepath.Join(baseDir, entryName)
		}
	}

	if highestScore > threshold {
		return bestMatch, nil
	}

	return "", fmt.Errorf("no sufficiently similar folder found (best score: %.2f)", highestScore)
}

// calculateSimilarity combines exact token overlap and string distance
func calculateSimilarity(target, candidate string) float64 {
	if target == candidate {
		return 1.0
	}

	tFields := strings.Fields(target)
	cFields := strings.Fields(candidate)

	// 1. Check for token intersection
	matches := 0
	for _, tf := range tFields {
		for _, cf := range cFields {
			if tf == cf {
				matches++
			}
		}
	}

	// Jaccard-ish similarity for tokens
	tokenScore := float64(matches) / float64(len(tFields)+len(cFields)-matches)

	// 2. Fallback to simple string contains (for partial word matches)
	if strings.Contains(candidate, target) || strings.Contains(target, candidate) {
		tokenScore += 0.2
	}

	return tokenScore
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
