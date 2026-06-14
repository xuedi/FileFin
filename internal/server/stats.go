package server

import (
	"net/http"
	"sort"
	"strings"

	"filefin/internal/db"
	"filefin/internal/transcode"
)

// Playability buckets, the single vocabulary the dashboard coverage figure and the
// statistics page both report. A file lands in exactly one, decided by classifyFile.
const (
	bucketDirectPlay    = "Direct play"    // served as-is, no copy needed
	bucketRemux         = "Remux"          // HLS stream-copy serves it; optimizer makes no copy
	bucketOptimized     = "Optimized"      // needs a copy and a fresh .optimized.mp4 exists
	bucketNeedsOptimize = "Needs optimize" // needs a copy, none yet (the optimizer backlog)
	bucketUnprobed      = "Unprobed"       // empty format columns; the probe agent has not reached it
)

// classifyFile decides a file's playability bucket from its probed format, exactly as the
// optimizer's candidacy does: a probed file is judged by content (DirectPlayable / then
// RemuxEligible), an unprobed one falls back to its extension. needsCopy reports whether
// the optimizer would actually produce an .optimized.mp4 for it (so the caller knows when a
// disk check for a fresh sibling is warranted). The Optimized vs Needs-optimize split is
// not made here because it requires that disk stat; classifyFile returns bucketNeedsOptimize
// for any file that needs a copy and lets optimizeCoverage refine it.
func classifyFile(f db.MediaFile) (bucket string, needsCopy bool) {
	if f.Container == "" || f.VideoCodec == "" {
		// Unprobed: only the extension is known. A browser-native extension is direct
		// play; anything else is held as unprobed (remux-vs-copy needs the codecs).
		if transcode.NeedsTranscode(f.Ext) {
			return bucketUnprobed, false
		}
		return bucketDirectPlay, false
	}
	if transcode.DirectPlayable(f.Container, f.VideoCodec, f.AudioCodec) {
		return bucketDirectPlay, false
	}
	if transcode.RemuxEligible(transcode.Streams{VideoCodec: strings.ToLower(f.VideoCodec), AudioCodec: strings.ToLower(f.AudioCodec)}) {
		return bucketRemux, false
	}
	return bucketNeedsOptimize, true
}

// optimizeCoverage splits the files that truly need a direct-play copy into those that
// already have a fresh .optimized.mp4 sibling and those still pending. It stats the disk
// only for the needs-copy subset, so the cost is bounded by the non-native files. The
// dashboard percentage is optimized / (optimized + pending).
func optimizeCoverage(files []db.MediaFile) (optimized, pending int) {
	for _, f := range files {
		if _, needsCopy := classifyFile(f); !needsCopy {
			continue
		}
		if _, fresh := transcode.OptimizedSibling(f.Path); fresh {
			optimized++
		} else {
			pending++
		}
	}
	return optimized, pending
}

// coveragePercent is optimized as a whole-number percentage of the files that need a copy,
// or 0 when none need one (avoids a divide-by-zero and reads as "nothing to do").
func coveragePercent(optimized, pending int) int {
	total := optimized + pending
	if total == 0 {
		return 0
	}
	return optimized * 100 / total
}

// labelCount is one slice of a distribution: a human label and how many files carry it.
type labelCount struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

// coverageStat is the optimize-coverage block shared by the dashboard and the stats page.
type coverageStat struct {
	Optimized  int `json:"optimized"`
	Pending    int `json:"pending"`
	Percent    int `json:"percent"`
	TotalFiles int `json:"totalFiles"`
	TotalMedia int `json:"totalMedia"`
}

// statsView is the statistics page payload: per-dimension distributions (each sorted
// descending by count) plus the coverage block.
type statsView struct {
	Containers  []labelCount `json:"containers"`
	VideoCodecs []labelCount `json:"videoCodecs"`
	AudioCodecs []labelCount `json:"audioCodecs"`
	Playability []labelCount `json:"playability"`
	Coverage    coverageStat `json:"coverage"`
}

// handleStats walks every cached media file once and reports the library's container and
// codec distributions, the playability breakdown, and the optimize coverage, for the admin
// Statistics page.
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	pool, err := s.ensureDB(ctx)
	if err != nil {
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return
	}
	files, err := db.AllFiles(ctx, pool)
	if err != nil {
		http.Error(w, "could not read media files", http.StatusInternalServerError)
		return
	}
	media, err := db.CountMedia(ctx, pool)
	if err != nil {
		http.Error(w, "could not read library stats", http.StatusInternalServerError)
		return
	}

	containers := map[string]int{}
	video := map[string]int{}
	audio := map[string]int{}
	play := map[string]int{}
	optimized, pending := 0, 0

	for _, f := range files {
		containers[containerLabel(f.Container)]++
		video[codecLabel(f.VideoCodec)]++
		audio[audioLabel(f.AudioCodec)]++
		bucket, needsCopy := classifyFile(f)
		if needsCopy {
			if _, fresh := transcode.OptimizedSibling(f.Path); fresh {
				optimized++
				bucket = bucketOptimized
			} else {
				pending++
			}
		}
		play[bucket]++
	}

	writeJSON(w, statsView{
		Containers:  sortedCounts(containers),
		VideoCodecs: sortedCounts(video),
		AudioCodecs: sortedCounts(audio),
		Playability: sortedCounts(play),
		Coverage: coverageStat{
			Optimized:  optimized,
			Pending:    pending,
			Percent:    coveragePercent(optimized, pending),
			TotalFiles: len(files),
			TotalMedia: media,
		},
	})
}

// containerLabel maps an ffprobe format_name (a comma-listed token set such as
// "mov,mp4,m4a,3gp,3g2,mj2" or "matroska,webm") onto a friendly bucket: the two
// browser-native families collapse to readable names, an empty value is "Unprobed", and
// anything else shows its first token uppercased.
func containerLabel(container string) string {
	if strings.TrimSpace(container) == "" {
		return "Unprobed"
	}
	switch {
	case containerInFamily(container, "mov", "mp4", "m4a", "m4v", "3gp", "3g2", "mj2"):
		return "MP4"
	case containerInFamily(container, "matroska", "webm"):
		return "Matroska/WebM"
	}
	first := strings.TrimSpace(strings.SplitN(container, ",", 2)[0])
	return strings.ToUpper(first)
}

// containerInFamily reports whether any comma-separated token of an ffprobe format_name is
// one of the given family tokens.
func containerInFamily(container string, family ...string) bool {
	set := make(map[string]bool, len(family))
	for _, f := range family {
		set[f] = true
	}
	for _, tok := range strings.Split(strings.ToLower(container), ",") {
		if set[strings.TrimSpace(tok)] {
			return true
		}
	}
	return false
}

// codecLabel normalizes a video codec name for display: lowercased, with empty rendered as
// "unprobed".
func codecLabel(codec string) string {
	c := strings.ToLower(strings.TrimSpace(codec))
	if c == "" {
		return "unprobed"
	}
	return c
}

// audioLabel is codecLabel for audio, but an empty value is a real state (no audio track),
// shown as "none" rather than "unprobed".
func audioLabel(codec string) string {
	c := strings.ToLower(strings.TrimSpace(codec))
	if c == "" {
		return "none"
	}
	return c
}

// sortedCounts turns a label->count map into a slice ordered by count descending, ties
// broken by label, so the charts and tables render deterministically.
func sortedCounts(m map[string]int) []labelCount {
	out := make([]labelCount, 0, len(m))
	for label, count := range m {
		out = append(out, labelCount{Label: label, Count: count})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Label < out[j].Label
	})
	return out
}
