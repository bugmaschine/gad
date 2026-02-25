package downloaders

import (
	"context"
	"fmt"
)

type Language int

const (
	LanguageUnspecified Language = iota
	LanguageEnglish
	LanguageGerman
)

func (l Language) GetNameShort() string {
	switch l {
	case LanguageEnglish:
		return "Eng"
	case LanguageGerman:
		return "Ger"
	default:
		return "Und"
	}
}

func (l Language) GetNameLong() string {
	switch l {
	case LanguageEnglish:
		return "English"
	case LanguageGerman:
		return "German"
	default:
		return "Unspecified"
	}
}

type VideoType struct {
	Type     VideoTypeKind
	Language Language
}

type VideoTypeKind int

const (
	VideoTypeUnspecified VideoTypeKind = iota
	VideoTypeRaw
	VideoTypeDub
	VideoTypeSub
)

func (vt VideoType) String() string {
	switch vt.Type {
	case VideoTypeRaw:
		return "Raw"
	case VideoTypeDub:
		if vt.Language == LanguageUnspecified {
			return "Dub"
		}
		return fmt.Sprintf("%sDub", vt.Language.GetNameShort())
	case VideoTypeSub:
		if vt.Language == LanguageUnspecified {
			return "Sub"
		}
		return fmt.Sprintf("%sSub", vt.Language.GetNameShort())
	default:
		return ""
	}
}

type EpisodesRequest struct {
	Kind    EpisodesRequestKind
	Payload AllOrSpecific
}

type EpisodesRequestKind int

const (
	EpisodesRequestUnspecified EpisodesRequestKind = iota
	EpisodesRequestEpisodes
	EpisodesRequestSeasons
)

type AllOrSpecific struct {
	All      bool
	Specific []Range
}

type Range struct {
	Begin uint32
	End   uint32
}

type ExtractorMatch struct {
	Any  bool
	Name string
}

type SeriesInfo struct {
	Title       string
	Description string
}

type EpisodeInfo struct {
	Season      uint32
	Episode     uint32
	Title       string
	MaxEpisodes uint32
}

type DownloadSettings struct {
	DdosWaitEpisodes uint32
	DdosWaitMs       uint32
	SkipExisting     bool
	CheckIfExists    func(season, episode, maxEpisodes uint32, videoType *VideoType) bool
}

type DownloadRequest struct {
	Url                 string
	Language            VideoType
	Episodes            EpisodesRequest
	SaveDirectory       string
	SeriesTitle         string
	ExtractorPriorities []ExtractorMatch
}

type Downloader interface {
	GetSeriesInfo(ctx context.Context) (*SeriesInfo, error)
	Download(ctx context.Context, request DownloadRequest, settings DownloadSettings, sender chan<- *DownloadTaskWrapper) error
}

type DownloadTaskWrapper struct {
	Episode EpisodeInfo
	Lang    VideoType
	Url     string
	Referer string
}
