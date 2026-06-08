package importer

import (
	"strconv"
	"strings"

	"filefin/internal/jellyfin"
)

// MetaFromJellyfin maps a Jellyfin NFO item into a Meta from its own fields. Like
// MetaFromPlex it is left unenriched on purpose: the NFO metadata is the starting
// point, and the OMDb enricher later fills any gaps additively (never overwriting these
// values). The caller's title/year are applied by the importer so the file matches its
// folder.
func MetaFromJellyfin(item jellyfin.Item) Meta {
	d := item.Details
	m := Meta{Title: item.Title, Year: item.Year, Description: strings.TrimSpace(d.Description)}

	md := map[string]string{}
	add := func(k, v string) {
		if v = strings.TrimSpace(v); v != "" {
			md[k] = v
		}
	}
	release := d.Release
	if release == "" && item.Year > 0 {
		release = strconv.Itoa(item.Year)
	}
	add("release", release)
	if d.Runtime > 0 {
		add("runtime", strconv.Itoa(d.Runtime))
	}
	add("directedBy", strings.Join(d.Directors, ", "))
	add("writtenBy", strings.Join(d.Writers, ", "))
	add("contentRating", d.ContentRating)
	add("studio", d.Studio)
	for _, u := range d.UniqueIDs {
		add(u.Type+"Id", u.Value)
	}
	if len(md) > 0 {
		m.Metadata = md
	}
	if r := strings.TrimSpace(d.Rating); r != "" {
		m.Ratings = map[string]string{"nfo": r}
	}
	m.Actors = append(m.Actors, d.Actors...)
	for _, g := range d.Genres {
		m.Tags = append(m.Tags, strings.ToLower(g))
	}
	return m
}
