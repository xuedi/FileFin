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
	b := newMetaBuilder(item.Title, item.Year, d.Description)
	release := d.Release
	if release == "" && item.Year > 0 {
		release = strconv.Itoa(item.Year)
	}
	b.add("release", release)
	if d.Runtime > 0 {
		b.add("runtime", strconv.Itoa(d.Runtime))
	}
	b.add("directedBy", strings.Join(d.Directors, ", "))
	b.add("writtenBy", strings.Join(d.Writers, ", "))
	b.add("contentRating", d.ContentRating)
	b.add("studio", d.Studio)
	for _, u := range d.UniqueIDs {
		b.add(u.Type+"Id", u.Value)
	}
	b.rate("nfo", d.Rating)
	b.m.Actors = append(b.m.Actors, d.Actors...)
	b.addGenres(d.Genres)
	return b.build()
}
