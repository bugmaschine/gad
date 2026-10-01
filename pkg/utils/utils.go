package utils

import (
	"bufio"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
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

type SimilarFolderMatcher struct {
	baseDir string
	entries []similarFolderCandidate
}

type similarFolderCandidate struct {
	norm string
	path string
}

const similarFolderThreshold = 0.4

func NewSimilarFolderMatcher(baseDir string) (*SimilarFolderMatcher, error) {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return nil, err
	}

	matcher := &SimilarFolderMatcher{
		baseDir: baseDir,
		entries: make([]similarFolderCandidate, 0, len(entries)),
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		matcher.addName(entry.Name())
	}

	return matcher, nil
}

func (m *SimilarFolderMatcher) Find(target string) (string, error) {
	targetNorm := normalize(target)
	var bestMatch string
	var highestScore float64

	for _, entry := range m.entries {
		score := calculateSimilarity(targetNorm, entry.norm)
		if score > highestScore {
			highestScore = score
			bestMatch = entry.path
		}
	}

	if highestScore > similarFolderThreshold {
		return bestMatch, nil
	}

	return "", fmt.Errorf("no sufficiently similar folder found (best score: %.2f)", highestScore)
}

func (m *SimilarFolderMatcher) Add(folderPath string) {
	name := filepath.Base(folderPath)
	candidate := similarFolderCandidate{
		norm: normalize(name),
		path: folderPath,
	}

	for i := range m.entries {
		if m.entries[i].path == folderPath {
			m.entries[i] = candidate
			return
		}
	}

	m.entries = append(m.entries, candidate)
}

func (m *SimilarFolderMatcher) addName(name string) {
	m.entries = append(m.entries, similarFolderCandidate{
		norm: normalize(name),
		path: filepath.Join(m.baseDir, name),
	})
}

func FindSimilarFolder(baseDir, target string) (string, error) {
	matcher, err := NewSimilarFolderMatcher(baseDir)
	if err != nil {
		return "", err
	}
	return matcher.Find(target)
}

var normalizeReplacer = strings.NewReplacer(
	"×", "x",
	"’", "'",
	"‘", "'",
	"“", "\"",
	"”", "\"",
	"–", "-",
	"—", "-",
)

var nonAlnumRegex = regexp.MustCompile(`[^a-z0-9 ]+`)

var stopWords = map[string]struct{}{
	"the": {}, "a": {}, "an": {}, "of": {}, "and": {}, "in": {},
	"is": {}, "to": {}, "with": {}, "for": {}, "on": {}, "at": {},
}

func normalize(s string) string {
	s = CleanSearchName(s)
	s = normalizeReplacer.Replace(s)
	s = strings.ToLower(s)
	s = nonAlnumRegex.ReplaceAllString(s, " ")
	s = strings.Join(strings.Fields(s), " ") // collapses whitespace, also trims

	return s
}

func meaningfulTokens(s string) []string {
	fields := strings.Fields(s)
	tokens := make([]string, 0, len(fields))
	for _, f := range fields {
		if _, isStop := stopWords[f]; isStop {
			continue
		}
		tokens = append(tokens, f)
	}
	return tokens
}

// calculateSimilarity combines exact token overlap and string distance,
// ignoring stop-words so generic words like "the"/"of" can't inflate the score.
func calculateSimilarity(target, candidate string) float64 {
	if target == candidate {
		return 1.0
	}

	tFields := meaningfulTokens(target)
	cFields := meaningfulTokens(candidate)

	if len(tFields) == 0 || len(cFields) == 0 {
		// nothing meaningful to compare (e.g. title was ONLY stop-words)
		return 0
	}

	// multiset intersection so duplicate tokens can't be double-counted
	remaining := make(map[string]int, len(cFields))
	for _, cf := range cFields {
		remaining[cf]++
	}

	matches := 0
	for _, tf := range tFields {
		if remaining[tf] > 0 {
			remaining[tf]--
			matches++
		}
	}

	tokenScore := float64(matches) / float64(len(tFields)+len(cFields)-matches)

	if strings.Contains(candidate, target) || strings.Contains(target, candidate) {
		tokenScore += 0.2
	}

	return tokenScore
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

// Code copied from https://gist.github.com/jerblack/d0eb182cc5a1c1d92d92a4c4fcc416c6
func IsExecutedAsAdmin() bool {
	if runtime.GOOS != "windows" {
		return os.Geteuid() == 0 // should also apply to macos, i think.
	}
	_, err := os.Open("\\\\.\\PHYSICALDRIVE0")
	if err != nil {
		return false
	}
	return true
}

func CleanQueueList(input []string) []string {
	var result []string
	seen := make(map[string]struct{})

	for _, line := range input {
		line = strings.TrimSpace(line)
		slog.Debug("Processing line from queue", "line", line)

		if line == "" || strings.HasPrefix(line, "#") {
			slog.Debug("Skipping invalid or commented line", "line", line)
			continue
		}

		if commentIndex := strings.Index(line, "#"); commentIndex >= 0 {
			line = strings.TrimSpace(line[:commentIndex])
			slog.Debug("Removed comment from line", "line", line)
		}
		if line == "" {
			continue
		}

		// sanity check for lines.
		if !strings.HasPrefix(line, "http://") && !strings.HasPrefix(line, "https://") {
			panic(fmt.Sprintf("Invalid line in queue: %s", line))
		}

		if _, ok := seen[line]; ok {
			slog.Debug("Skipping duplicate URL", "url", line)
			continue
		}

		seen[line] = struct{}{}
		result = append(result, line)
		slog.Debug("Added URL from queue", "url", line)
	}

	return result
}

func LoadQueueURLs(scanner *bufio.Scanner) ([]string, error) {
	var lines []string

	for scanner.Scan() {
		lines = append(lines, strings.TrimSpace(scanner.Text()))
	}

	lines = CleanQueueList(lines)

	return lines, scanner.Err()
}
