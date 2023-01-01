package models

type VideoInfo struct {
	Title    string
	ThumbURL string
	Path     string
	Audio    bool
}

type VideoOption struct {
	ID       string
	FormatID string
	Label    string
	Size     int64
	Audio    bool

	VideoInfo VideoInfo
}

type CachedVideoOption struct {
	FormatID string
	URL      string
	Audio    bool

	VideoInfo VideoInfo
}
