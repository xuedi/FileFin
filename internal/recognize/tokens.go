package recognize

import "regexp"

// The vocabulary of tokens that are never part of a title: quality, source, codec, audio and
// packaging markers left behind by whoever released the file. They are matched on the *raw*
// name, before dots and underscores become spaces, so a token written "H.264", "H_264" or
// "H 264" is one rule rather than three.
//
// Short tokens are deliberately absent. "NF", "IQ", "DV", "HC", "SBS", "V2" and a bare "WEB"
// all appear in real names here, but each of them is also a plausible word in a real title,
// and in every observed name an earlier marker (the year, or a season marker) already ends the
// title before they are reached. Precision is worth more than the redundancy.
const (
	sep = `[\s._]`         // separator inside a name: space, dot, underscore
	bnd = `[\s._\-\[\]()]` // token boundary: a separator, a dash, or a bracket edge

	junkResolution = `2160p|1440p|1080[pi]|720p|576p|480p|360p|4k|uhd|hd080p`
	junkSource     = `web-?dl|web-?rip|blu-?ray|bdrip|brrip|dvdrip|dvdscr|hdrip|fhdrip|hdtv|` +
		`remux|amzn|dsnp|viki|iqiyi|tving|mubi|itunes|netflix`
	junkCodec = `x26[45]|h` + sep + `?26[45]|avc|hevc|xvid|divx|hi10|10-?bit|8-?bit|vp9|av1`
	junkAudio = `aac[\d.]*|ac3|e-?ac-?3|dts(?:-hd)?|ddp?[\d.]*|truehd|atmos|flac|mp3|opus|` +
		`dual` + sep + `audio|\d+audios?`
	junkPackaging = `complete|collection|criterion|remastered|proper|repack|extended|unrated|` +
		`imax|multi|nondrm`
)

var (
	// reJunk finds the first packaging token in a raw name. Its boundary class includes
	// brackets so "(Dual Audio 5.1 FLAC)" and "[1080p]" are recognised too.
	reJunk = regexp.MustCompile(`(?i)(?:^|` + bnd + `)(?:` +
		junkResolution + `|` + junkSource + `|` + junkCodec + `|` + junkAudio + `|` + junkPackaging +
		`)(?:` + bnd + `|$)`)

	// reGroupSuffix matches a release-group credit at the very end of a name: "-AppleTor",
	// "@CHDWEB", "-unco@AvistaZ".
	reGroupSuffix = regexp.MustCompile(`[-@][A-Za-z0-9]+(?:@[A-Za-z0-9]+)?$`)

	// reCRC matches the bracketed hash fansub groups append: "[8E3F476A]", "(163C0F1F)".
	reCRC = regexp.MustCompile(`[\[(][0-9A-Fa-f]{8}[\])]`)

	// reEdgeTagHead and reEdgeTagTail match a bracketed decoration at either end of a name:
	// the group tag "[HorribleSubs]", "(Hi10)", and trailing "[720p]", "[c]", "[BD9CCF15]".
	reEdgeTagHead = regexp.MustCompile(`^\s*[\[(][^\[\]()]*[\])]\s*`)
	reEdgeTagTail = regexp.MustCompile(`\s*[\[(][^\[\]()]*[\])]\s*$`)

	// reSkipFile matches a file that is never the media itself, wherever it sits.
	reSkipFile = regexp.MustCompile(`(?i)(?:^|` + bnd + `)(?:teaser|trailer|sample|preview)(?:` + bnd + `|$)`)

	// reSkipDir matches a subfolder that holds extras rather than the media.
	reSkipDir = regexp.MustCompile(`(?i)^(?:samples?|extras?|featurettes?|bonus|trailers?|` +
		`behind[\s._-]the[\s._-]scenes|full[\s._-]cover)$`)
)

// SkipFile reports whether a file name marks itself as something other than the media: a
// teaser, trailer, sample or preview. Such files are never counted towards a media and never
// imported.
func SkipFile(name string) bool { return reSkipFile.MatchString(name) }

// SkipDir reports whether a directory holds extras rather than the media itself.
func SkipDir(name string) bool { return reSkipDir.MatchString(name) }
