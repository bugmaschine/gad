package download

import (
	"fmt"
	"math"
	"regexp"
	"strings"
	"unicode"

	"github.com/bugmaschine/sdl/internal/downloaders"
)

func PrepareSeriesNameForFile(name string) string {
	const NameLimit = 160

	// Remove control characters
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name)

	// Replace whitespace with space
	name = strings.Join(strings.Fields(name), " ")

	// Remove quotes
	name = strings.ReplaceAll(name, "\"", "")

	// Colon regexes
	colon1 := regexp.MustCompile(`([\p{L}\d]): +([\p{L}\d])`)
	name = colon1.ReplaceAllString(name, "${1} - ${2}")
	colon2 := regexp.MustCompile(`([\p{L}\d]):([\p{L}\d])`)
	name = colon2.ReplaceAllString(name, "${1} ${2}")
	name = strings.ReplaceAll(name, ":", "")

	// Question marks
	question := regexp.MustCompile(`([\p{L}\d])\?+ +([\p{L}\d])`)
	name = question.ReplaceAllString(name, "${1} - ${2}")
	name = strings.ReplaceAll(name, "?", "")

	// Slashes
	slash1 := regexp.MustCompile(`\b([\p{L}\d])/+([\p{L}\d])\b`)
	name = slash1.ReplaceAllString(name, "${1}${2}")
	slash2 := regexp.MustCompile(`([\p{L}\d])/+([\p{L}\d])`)
	name = slash2.ReplaceAllString(name, "${1} ${2}")
	name = strings.ReplaceAll(name, "/", "")

	// Other special chars
	chars := []string{"\\", "*", "<", ">", "|"}
	for _, c := range chars {
		name = strings.ReplaceAll(name, c, "")
	}

	// Multiple spaces
	multipleSpaces := regexp.MustCompile(` {2,}`)
	name = multipleSpaces.ReplaceAllString(name, " ")

	// Trim space and dot
	name = strings.Trim(name, " .")

	if len(name) > NameLimit {
		// Truncate safely at rune boundary
		runes := []rune(name)
		totalBytes := 0
		var truncated []rune
		for _, r := range runes {
			totalBytes += len(string(r))
			if totalBytes > NameLimit {
				break
			}
			truncated = append(truncated, r)
		}
		name = string(truncated)
	}

	return name
}

func GetEpisodeName(animeName string, videoType *downloaders.VideoType, epInfo *downloaders.EpisodeInfo, includeTitle bool) string {
	var sb strings.Builder

	if animeName != "" {
		sb.WriteString(animeName)
		sb.WriteString(" - ")
	}

	if epInfo.Season > 0 {
		sb.WriteString(fmt.Sprintf("S%02d", epInfo.Season))
	}

	alignment := 2
	if epInfo.MaxEpisodes > 0 {
		// Calculate alignment like Rust: (log10(max) + 1)
		alignment = int(math.Log10(float64(epInfo.MaxEpisodes))) + 1
		if alignment < 2 {
			alignment = 2
		}
	}

	sb.WriteString("E")
	sb.WriteString(fmt.Sprintf("%0*d", alignment, epInfo.Episode))

	if videoType != nil {
		vtStr := videoType.String()
		if vtStr != "" {
			sb.WriteString(" - ")
			sb.WriteString(vtStr)
		}
	}

	if includeTitle && epInfo.Title != "" {
		sb.WriteString(" - ")
		sb.WriteString(epInfo.Title)
	}

	return sb.String()
}

func formatEpisodeNumber(num uint32, alignment int) string {
	if alignment <= 0 {
		return fmt.Sprintf("%d", num)
	}
	tpl := fmt.Sprintf("%%0%dd", alignment)
	return fmt.Sprintf(tpl, num)
}

func ilog10(n uint32) int {
	if n == 0 {
		return 0
	}
	return int(math.Log10(float64(n)))
}
