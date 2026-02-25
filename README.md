# Go Aniworld Downloader (gad)

This is a fork of a pull request which I rewrote in go, as I don't use rust that often. It's mostly meant for my own use, as i need some custom features.

## Supported sites
### German
* [AniWorld](https://aniworld.to)
* ~~[S.to](https://s.to)~~ — I do not support s.to because I don't use it. The original [sdl](https://github.com/Funami580/sdl) does support it though.

## Supported extractors
* Doodstream
* Filemoon
* LoadX
* Speedfiles
* Streamtape
* Vidmoly
* Vidoza
* Voe

## Usage

### Downloading from a queue file
```bash
gad -q queue.txt
```

Queue file contents (you can comment out lines and it will be ignored):
```
https://aniworld.to/anime/stream/you-and-i-are-polar-opposites
#https://aniworld.to/anime/stream/spy-x-family
https://aniworld.to/anime/stream/yuruyuri-happy-go-lily
```

### Downloading a single episode
By URL:
```bash
gad 'https://aniworld.to/anime/stream/yuruyuri-happy-go-lily/staffel-1/episode-1'
```
By specifying it explicitly:
```bash
gad -e 11 'https://aniworld.to/anime/stream/yuruyuri-happy-go-lily/staffel-2'
```

### Downloading an entire season
By URL:
```bash
gad 'https://aniworld.to/anime/stream/yuruyuri-happy-go-lily/staffel-2'
gad 'https://aniworld.to/anime/stream/yuruyuri-happy-go-lily/filme'
```
By specifying it explicitly:
```bash
gad -s 2 'https://aniworld.to/anime/stream/yuruyuri-happy-go-lily'
gad -s 0 'https://aniworld.to/anime/stream/yuruyuri-happy-go-lily'
```

### Downloading multiple episodes
```bash
gad -e 1,2-6,9 'https://aniworld.to/anime/stream/yuruyuri-happy-go-lily/staffel-2'
```

### Downloading multiple seasons
```bash
gad -s 1-2,4 'https://aniworld.to/anime/stream/yuruyuri-happy-go-lily'
```

### Downloading all seasons
```bash
gad 'https://aniworld.to/anime/stream/yuruyuri-happy-go-lily'
```

### Downloading in other languages
```bash
gad -t gersub 'https://aniworld.to/anime/stream/yuruyuri-happy-go-lily/staffel-1/episode-1'
```
Either dub or sub:
```bash
gad -t ger 'https://aniworld.to/anime/stream/higurashi-no-naku-koro-ni/staffel-1/episode-1'
gad -t german 'https://aniworld.to/anime/stream/higurashi-no-naku-koro-ni/staffel-1/episode-1'
```
If an episode has multiple languages, the general language preference is as follows:
* English Anime Website: EngSub > EngDub
* German Anime Website: GerDub > GerSub > EngSub > EngDub
* German non-Anime Website: GerDub > GerSub > EngDub > EngSub

### Prioritize specific extractors
First try Filemoon, then Voe, and finally try every other possible extractor using the `*` fallback:
```bash
gad -p filemoon,voe,* 'https://aniworld.to/anime/stream/yuruyuri-happy-go-lily/staffel-1/episode-1'
```

### Downloading with extractor directly
```bash
gad -u 'https://streamtape.com/e/DXYPVBeKrpCkMwD'
gad -u=voe 'https://prefulfilloverdoor.com/e/8cu8qkojpsx9'
```

### Help output
```
Usage:
  gad [URL] [flags]

Flags:
      --browser                  Show browser window
  -N, --concurrent int           Concurrent downloads (default 5)
      --ddos-wait-episodes int   Amount of requests before waiting (default 4)
      --ddos-wait-ms uint32      Duration in milliseconds to wait (default 60000)
  -d, --debug                    Enable debug mode
  -e, --episodes string          Only download specific episodes (e.g. 1-3,5)
  -u, --extractor string         Use underlying extractors directly
  -h, --help                     help for gad
      --lang string              Only download specific language
  -p, --priorities string        Extractor priorities (default "*")
  -q, --queue-file string        Path to the file containing URLs to download
  -r, --rate string              Maximum download rate (default "inf")
  -R, --retries int              Number of download retries (default 5)
  -s, --seasons string           Only download specific seasons
      --skip-existing            Skip existing files
      --type string              Only download specific video type (raw, dub, sub)
  -t, --type-language string     Shorthand for language and video type
```
## Scripting

You can use `gad` in scripts to keep your library up to date. `gad` will return code 0 if everything went without a problem.
## Notes
If FFmpeg and ChromeDriver are not found in the `PATH`, they will be downloaded automatically.

## Build from source
Currently, Go 1.24 or newer is required.
```
go build -o gad ./cmd/gad/main.go
```
The resulting executable is found at `gad`.

## Pre-built binaries
Download from [releases](https://github.com/bugmaschine/gad/releases)

## Thanks
* [aniworld_scraper](https://github.com/wolfswolke/aniworld_scraper) for the inspiration and showing how it could be done
* [sdl](https://github.com/Funami580/sdl) for providing the original rust codebase and making this fork possible