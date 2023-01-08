package models

type VideoInfo struct {
	URL      string
	Title    string
	ThumbURL string

	Duration int

	Vertical bool
	Youtube  bool
}

type VideoOption struct {
	ID       string
	FormatID string
	Label    string
	Size     uint64
	Audio    bool

	Path string

	VideoInfo VideoInfo
}
