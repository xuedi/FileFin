// Package sources merges what several metadata databases say about one item. Each database's
// answer is kept as its own snapshot in meta.json (see importer.Snapshot); the top-level
// fields are the merged view everything else reads. The merge fills a field when the sources
// agree or only one has it, and reports a conflict when they disagree, leaving the value in
// place until an admin picks a side, per item or as a rule for the whole library.
package sources

import (
	"strconv"
	"strings"

	"filefin/internal/importer"
)

// The sources the merge knows, in their default order of preference: OMDb first, because it
// is the incumbent every existing item was matched with.
const (
	OMDb = "omdb"
	TMDb = "tmdb"
)

// Order is the default preference between sources.
var Order = []string{OMDb, TMDb}

// Choice values that are not a source.
const (
	ChoiceUnion  = "union"  // a list field takes every source's entries
	ChoiceManual = "manual" // the field keeps the value it has (typed by hand, or kept on purpose)
)

// Label is a source's display name.
func Label(src string) string {
	switch src {
	case OMDb:
		return "OMDb"
	case TMDb:
		return "TMDb"
	}
	return src
}

// Policy is how a field's values from different sources are compared and combined.
type Policy string

const (
	PolicyTitle     Policy = "title"     // agree when a title or alternative title folds the same
	PolicyYear      Policy = "year"      // one year apart is a field conflict, more an identity one
	PolicyKind      Policy = "kind"      // movie vs series: a mismatch is an identity conflict
	PolicyID        Policy = "id"        // a differing id is an identity conflict; fill only
	PolicyRuntime   Policy = "runtime"   // within 10% (at least 3 minutes)
	PolicyRelease   Policy = "release"   // the same year; the day differs by country
	PolicyDirectors Policy = "directors" // one list of names contains the other
	PolicyWriters   Policy = "writers"   // the lists share a name; writing credits vary a lot
	PolicyExact     Policy = "exact"     // equal after folding case and spacing
	PolicyUnion     Policy = "union"     // never conflicts; the sources' entries are combined
	PolicyProse     Policy = "prose"     // never conflicts; prose always differs, one is chosen
	PolicyFill      Policy = "fill"      // never conflicts; fills a field only one source has
)

// Field is one mergeable field.
type Field struct {
	Key    string
	Label  string
	Group  string
	Policy Policy
	// List marks a multivalued field; Joined ones are stored as one comma-separated string.
	List, Joined bool
	// Pickable fields can be pinned to a source; ids and the kind are fixed by a re-match.
	Pickable bool
	// Virtual fields exist only in the snapshots (the kind), never at the top level.
	Virtual bool
}

// Fields is every mergeable field, in the order the merge view shows them.
var Fields = []Field{
	{Key: "title", Label: "Title", Group: "Identity", Policy: PolicyTitle, Pickable: true},
	{Key: "year", Label: "Year", Group: "Identity", Policy: PolicyYear, Pickable: true},
	{Key: "kind", Label: "Type", Group: "Identity", Policy: PolicyKind, Virtual: true},
	{Key: "imdbID", Label: "IMDb ID", Group: "Identity", Policy: PolicyID},
	{Key: "tmdbID", Label: "TMDb ID", Group: "Identity", Policy: PolicyFill},
	{Key: "description", Label: "Plot", Group: "About", Policy: PolicyProse, Pickable: true},
	{Key: "tagline", Label: "Tagline", Group: "About", Policy: PolicyFill, Pickable: true},
	{Key: "originalTitle", Label: "Original title", Group: "About", Policy: PolicyFill, Pickable: true},
	{Key: "release", Label: "Released", Group: "About", Policy: PolicyRelease, Pickable: true},
	{Key: "runtime", Label: "Runtime", Group: "About", Policy: PolicyRuntime, Pickable: true},
	{Key: "directedBy", Label: "Directed by", Group: "People", Policy: PolicyDirectors, List: true, Joined: true, Pickable: true},
	{Key: "writtenBy", Label: "Written by", Group: "People", Policy: PolicyWriters, List: true, Joined: true, Pickable: true},
	{Key: "actors", Label: "Cast", Group: "People", Policy: PolicyFill, List: true, Pickable: true},
	{Key: "genres", Label: "Genres", Group: "Classification", Policy: PolicyUnion, List: true, Pickable: true},
	{Key: "language", Label: "Language", Group: "Classification", Policy: PolicyUnion, List: true, Joined: true, Pickable: true},
	{Key: "origin", Label: "Country", Group: "Classification", Policy: PolicyUnion, List: true, Joined: true, Pickable: true},
	{Key: "contentRating", Label: "Rated", Group: "Classification", Policy: PolicyExact, Pickable: true},
	{Key: "awards", Label: "Awards", Group: "Other", Policy: PolicyFill, Pickable: true},
	{Key: "boxOffice", Label: "Box office", Group: "Other", Policy: PolicyFill, Pickable: true},
}

// FieldByKey looks a field up; ok is false for an unknown key.
func FieldByKey(key string) (Field, bool) {
	for _, f := range Fields {
		if f.Key == key {
			return f, true
		}
	}
	return Field{}, false
}

// value reads a field from a Meta as a list (a scalar field is a list of at most one).
func value(m importer.Meta, f Field) []string {
	var raw string
	switch f.Key {
	case "title":
		raw = m.Title
	case "year":
		if m.Year > 0 {
			raw = strconv.Itoa(m.Year)
		}
	case "description":
		raw = m.Description
	case "actors":
		return clean(m.Actors)
	case "genres":
		return clean(m.Genres)
	default:
		raw = m.Metadata[f.Key]
	}
	if f.Joined {
		return clean(strings.Split(raw, ","))
	}
	return clean([]string{raw})
}

// setValue writes a field onto a Meta.
func setValue(m *importer.Meta, f Field, v []string) {
	joined := strings.Join(v, ", ")
	switch f.Key {
	case "title":
		m.Title = joined
	case "year":
		m.Year, _ = strconv.Atoi(joined)
	case "description":
		m.Description = joined
	case "actors":
		m.Actors = append([]string(nil), v...)
	case "genres":
		m.Genres = append([]string(nil), v...)
	default:
		if joined == "" {
			delete(m.Metadata, f.Key)
			return
		}
		if m.Metadata == nil {
			m.Metadata = map[string]string{}
		}
		m.Metadata[f.Key] = joined
	}
}

// snapshotValue reads a field from a snapshot.
func snapshotValue(s *importer.Snapshot, f Field) []string {
	if f.Key == "kind" {
		return clean([]string{s.Kind})
	}
	return value(importer.Meta{
		Title: s.Title, Year: s.Year, Description: s.Description,
		Metadata: s.Metadata, Actors: s.Actors, Genres: s.Genres,
	}, f)
}

func clean(in []string) []string {
	var out []string
	for _, v := range in {
		if v = strings.TrimSpace(v); v != "" && !strings.EqualFold(v, "N/A") {
			out = append(out, v)
		}
	}
	return out
}

// PinEdits marks every pickable field an edit changed as manual, so no merge ever overwrites
// what an admin typed. It returns the edited Meta's choices.
func PinEdits(before, after importer.Meta) map[string]string {
	out := make(map[string]string, len(after.Choices))
	for k, v := range after.Choices {
		out[k] = v
	}
	for _, f := range Fields {
		if f.Pickable && !sameValues(value(before, f), value(after, f), f.List) {
			out[f.Key] = ChoiceManual
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
