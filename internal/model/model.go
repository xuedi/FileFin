// Package model holds the domain types the scanner produces from the filesystem.
package model

// KV is an ordered key/value pair used for meta.md sections.
type KV struct {
	Key   string
	Value string
}

// Meta is the parsed contents of a media folder's meta.md.
type Meta struct {
	Title       string
	Description string
	Plot        string
	Metadata    []KV
	Ratings     []KV
	Technical   []KV
	Actors      []string
	Tags        []string
}

// Subtitle is one external sidecar subtitle attached to a media file. Lang is the
// normalised language tag; Path is the on-disk file.
type Subtitle struct {
	Lang string
	Path string
}

// MediaFile is a single playable file inside a media folder. Season/Episode are
// zero when the file is not part of a numbered series.
type MediaFile struct {
	Path      string
	Name      string
	Season    int
	Episode   int
	Ext       string
	Subtitles []Subtitle
}

// Media is one media folder: a single film, a film series, or a multi-episode
// show. There is deliberately no film/show distinction.
type Media struct {
	ID       string
	Category string
	Folder   string
	Path     string
	Year     int
	Title    string
	Files    []MediaFile
	Poster   string
	Meta     *Meta
}

// Category is a top-level grouping folder under the data directory.
type Category struct {
	Name  string
	Path  string
	Media []Media
}

// Scan is the full result of walking the data directory.
type Scan struct {
	Categories []Category
	Issues     []string
}
