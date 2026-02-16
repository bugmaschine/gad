package cli

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/bugmaschine/sdl/internal/downloaders"
	"github.com/spf13/cobra"
)

type Args struct {
	VideoType           string
	Language            string
	TypeLanguage        string
	Episodes            string
	Seasons             string
	ExtractorPriorities string
	Extractor           string
	ConcurrentDownloads int
	LimitRate           string
	Retries             int
	DdosWaitEpisodes    int
	DdosWaitMs          uint32
	SkipExisting        bool
	Debug               bool
	Browser             bool
	Url                 string
}

func (a *Args) GetVideoType() downloaders.VideoType {
	if a.TypeLanguage != "" {
		vt, err := parseShorthand(a.TypeLanguage)
		if err == nil {
			return vt
		}
	}

	lang := parseLanguage(a.Language)
	switch strings.ToLower(a.VideoType) {
	case "raw":
		return downloaders.VideoType{Type: downloaders.VideoTypeRaw}
	case "dub":
		return downloaders.VideoType{Type: downloaders.VideoTypeDub, Language: lang}
	case "sub":
		return downloaders.VideoType{Type: downloaders.VideoTypeSub, Language: lang}
	default:
		return downloaders.VideoType{Type: downloaders.VideoTypeUnspecified, Language: lang}
	}
}

func (a *Args) GetEpisodesRequest() downloaders.EpisodesRequest {
	if a.Episodes != "" {
		ranges, _ := parseRanges(a.Episodes)
		return downloaders.EpisodesRequest{
			Kind: downloaders.EpisodesRequestEpisodes,
			Payload: downloaders.AllOrSpecific{
				All:      a.Episodes == "all",
				Specific: ranges,
			},
		}
	}
	if a.Seasons != "" {
		ranges, _ := parseRanges(a.Seasons)
		return downloaders.EpisodesRequest{
			Kind: downloaders.EpisodesRequestSeasons,
			Payload: downloaders.AllOrSpecific{
				All:      a.Seasons == "all",
				Specific: ranges,
			},
		}
	}
	return downloaders.EpisodesRequest{Kind: downloaders.EpisodesRequestUnspecified}
}

func parseLanguage(s string) downloaders.Language {
	switch strings.ToLower(s) {
	case "en", "english", "eng":
		return downloaders.LanguageEnglish
	case "de", "german", "ger":
		return downloaders.LanguageGerman
	default:
		return downloaders.LanguageUnspecified
	}
}

func parseShorthand(input string) (downloaders.VideoType, error) {
	inputLower := strings.ToLower(input)
	if inputLower == "unspecified" {
		return downloaders.VideoType{Type: downloaders.VideoTypeUnspecified, Language: downloaders.LanguageUnspecified}, nil
	}
	if inputLower == "raw" {
		return downloaders.VideoType{Type: downloaders.VideoTypeRaw}, nil
	}
	if inputLower == "dub" {
		return downloaders.VideoType{Type: downloaders.VideoTypeDub, Language: downloaders.LanguageUnspecified}, nil
	}
	if inputLower == "sub" {
		return downloaders.VideoType{Type: downloaders.VideoTypeSub, Language: downloaders.LanguageUnspecified}, nil
	}

	// Suffix checks
	if strings.HasSuffix(inputLower, "dub") {
		langPart := strings.TrimSuffix(inputLower, "dub")
		lang := parseLanguage(langPart)
		if lang != downloaders.LanguageUnspecified {
			return downloaders.VideoType{Type: downloaders.VideoTypeDub, Language: lang}, nil
		}
	}
	if strings.HasSuffix(inputLower, "sub") {
		langPart := strings.TrimSuffix(inputLower, "sub")
		lang := parseLanguage(langPart)
		if lang != downloaders.LanguageUnspecified {
			return downloaders.VideoType{Type: downloaders.VideoTypeSub, Language: lang}, nil
		}
	}

	// Language only check
	lang := parseLanguage(inputLower)
	if lang != downloaders.LanguageUnspecified {
		return downloaders.VideoType{Type: downloaders.VideoTypeUnspecified, Language: lang}, nil
	}

	return downloaders.VideoType{}, fmt.Errorf("failed to parse %q as video type shorthand", input)
}

func parseRanges(input string) ([]downloaders.Range, error) {
	if strings.ToLower(input) == "all" || strings.ToLower(input) == "unspecified" {
		return nil, nil
	}

	noSpace := strings.ReplaceAll(input, " ", "")
	parts := strings.Split(noSpace, ",")
	var ranges []downloaders.Range

	for _, part := range parts {
		if strings.Contains(part, "-") {
			rangeParts := strings.Split(part, "-")
			if len(rangeParts) != 2 {
				return nil, fmt.Errorf("invalid range format: %s", part)
			}
			begin, err := strconv.ParseUint(rangeParts[0], 10, 32)
			if err != nil {
				return nil, err
			}
			end, err := strconv.ParseUint(rangeParts[1], 10, 32)
			if err != nil {
				return nil, err
			}
			if begin > end {
				return nil, fmt.Errorf("range start cannot be bigger than range end: %s", part)
			}
			ranges = append(ranges, downloaders.Range{Begin: uint32(begin), End: uint32(end)})
		} else {
			num, err := strconv.ParseUint(part, 10, 32)
			if err != nil {
				return nil, err
			}
			ranges = append(ranges, downloaders.Range{Begin: uint32(num), End: uint32(num)})
		}
	}

	return mergeRanges(ranges), nil
}

func mergeRanges(ranges []downloaders.Range) []downloaders.Range {
	if len(ranges) <= 1 {
		return ranges
	}

	sort.Slice(ranges, func(i, j int) bool {
		return ranges[i].Begin < ranges[j].Begin
	})

	merged := []downloaders.Range{ranges[0]}
	for i := 1; i < len(ranges); i++ {
		last := &merged[len(merged)-1]
		current := ranges[i]

		if current.Begin <= last.End+1 {
			if current.End > last.End {
				last.End = current.End
			}
		} else {
			merged = append(merged, current)
		}
	}
	return merged
}

func ParseRateLimit(input string) (float64, error) {
	if strings.ToLower(input) == "inf" {
		return 0, nil // 0 means infinity in our context usually, or use a large number
	}

	re := regexp.MustCompile(`^([\d.]+)\s*([a-zA-Z]*)$`)
	matches := re.FindStringSubmatch(input)
	if matches == nil {
		return 0, fmt.Errorf("invalid rate limit format: %s", input)
	}

	val, err := strconv.ParseFloat(matches[1], 64)
	if err != nil {
		return 0, err
	}

	unit := strings.ToLower(matches[2])
	multiplier := 1.0
	switch unit {
	case "k", "kb":
		multiplier = 1000
	case "ki", "kib":
		multiplier = 1024
	case "m", "mb":
		multiplier = 1000 * 1000
	case "mi", "mib":
		multiplier = 1024 * 1024
	case "g", "gb":
		multiplier = 1000 * 1000 * 1000
	case "gi", "gib":
		multiplier = 1024 * 1024 * 1024
	}

	return val * multiplier, nil
}

func NewRootCommand(args *Args) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "sdl [URL]",
		Short: "Download multiple episodes from streaming sites",
		Args:  cobra.ExactArgs(1),
		Run: func(cmd *cobra.Command, cmdArgs []string) {
			args.Url = cmdArgs[0]
		},
	}

	f := cmd.Flags()
	f.StringVar(&args.VideoType, "type", "", "Only download specific video type (raw, dub, sub)")
	f.StringVar(&args.Language, "lang", "", "Only download specific language")
	f.StringVarP(&args.TypeLanguage, "type-language", "t", "", "Shorthand for language and video type")
	f.StringVarP(&args.Episodes, "episodes", "e", "", "Only download specific episodes (e.g. 1-3,5)")
	f.StringVarP(&args.Seasons, "seasons", "s", "", "Only download specific seasons")
	f.StringVarP(&args.ExtractorPriorities, "priorities", "p", "*", "Extractor priorities")
	f.StringVarP(&args.Extractor, "extractor", "u", "", "Use underlying extractors directly")
	f.IntVarP(&args.ConcurrentDownloads, "concurrent", "N", 5, "Concurrent downloads")
	f.StringVarP(&args.LimitRate, "rate", "r", "inf", "Maximum download rate")
	f.IntVarP(&args.Retries, "retries", "R", 5, "Number of download retries")
	f.IntVar(&args.DdosWaitEpisodes, "ddos-wait-episodes", 4, "Amount of requests before waiting")
	f.Uint32Var(&args.DdosWaitMs, "ddos-wait-ms", 60000, "Duration in milliseconds to wait")
	f.BoolVar(&args.SkipExisting, "skip-existing", false, "Skip existing files")
	f.BoolVar(&args.Browser, "browser", false, "Show browser window")
	f.BoolVarP(&args.Debug, "debug", "d", false, "Enable debug mode")

	return cmd
}
