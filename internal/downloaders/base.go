package downloaders

var downloaders []DownloaderFactory

type DownloaderFactory func(url string) (Downloader, error)

func Register(f DownloaderFactory) {
	downloaders = append(downloaders, f)
}

func GetDownloader(url string) (Downloader, error) {
	for _, f := range downloaders {
		d, err := f(url)
		if err == nil && d != nil {
			return d, nil
		}
	}
	return nil, nil
}
