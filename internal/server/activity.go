package server

import (
	"context"
	"database/sql"
	"net/http"
	"strconv"
	"sync"

	"filefin/internal/db"
)

// The Progress page is split along the seam the agents already have (see docs/agents.md):
// a **run** is one pass with a beginning and an end - the health sweep's walk of the library,
// or an agent draining its queue - while an **activity** item is a single unit of work that
// takes long enough to be worth watching. Keeping them apart is what stops a queue of
// sub-second tasks from strobing a list of progress bars.

// The run kinds, which double as their display labels' key on the page.
const (
	runSweep     = "sweep"
	runImport    = "import"
	runOptimize  = "optimize"
	runEnrich    = "enrich"
	runThumbnail = "thumbnail"
	runProbe     = "probe"
	runPeople    = "people"
	runTMDb      = "tmdb"
)

// runStatus is one pass in flight. Done/Total describe the whole pass, so the bar fills once
// from start to finish; Detail carries whatever distinguishes this run from another of the
// same kind (a sweep's scope, say). A kind with nothing to do is simply absent.
type runStatus struct {
	Kind   string `json:"kind"`
	Label  string `json:"label"`
	Detail string `json:"detail,omitempty"`
	Done   int    `json:"done"`
	Total  int    `json:"total"`
}

// activityItem is one piece of work actually being done. Percent is -1 when the work has no
// measurable progress, in which case State carries a word for it instead.
type activityItem struct {
	Key     string `json:"key"`
	Kind    string `json:"kind"`
	Title   string `json:"title"`
	Detail  string `json:"detail,omitempty"`
	State   string `json:"state,omitempty"`
	Percent int    `json:"percent"`
}

// activitySnapshot is the whole Progress page in one read, so every part of it describes the
// same instant. It replaced five separate per-queue polls that each rendered as they landed.
type activitySnapshot struct {
	Runs     []runStatus    `json:"runs"`
	Activity []activityItem `json:"activity"`
}

// drainTracker gives a queue the one thing a queue does not have: a denominator. A queue
// that goes from empty to N items has started a run of N; as agents drain it the run fills
// up, and a refill that adds more while it runs raises the total rather than rewinding the
// bar. The run ends when the queue empties, and the next non-empty queue starts a new one.
type drainTracker struct {
	mu   sync.Mutex
	high map[string]int
}

func newDrainTracker() *drainTracker { return &drainTracker{high: map[string]int{}} }

// observe folds a queue's current remaining count into its run and reports the run's
// progress. ok is false when the queue is empty, i.e. there is no run to show.
func (d *drainTracker) observe(kind string, remaining int) (done, total int, ok bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if remaining <= 0 {
		delete(d.high, kind)
		return 0, 0, false
	}
	if remaining > d.high[kind] {
		d.high[kind] = remaining
	}
	total = d.high[kind]
	return total - remaining, total, true
}

// handleActivity answers the Progress page: every run in flight and every piece of work
// being done, in one snapshot.
func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request) {
	pool, err := s.ensureDB(r.Context())
	if err != nil {
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return
	}
	ctx := r.Context()
	snap := activitySnapshot{Runs: []runStatus{}, Activity: []activityItem{}}
	s.addSweepRun(&snap)
	s.addImports(ctx, pool, &snap)
	s.addOptimize(ctx, pool, &snap)
	s.addEnrich(ctx, pool, &snap)
	s.addThumbnails(ctx, pool, &snap)
	s.addProbes(ctx, pool, &snap)
	s.addTMDb(ctx, pool, &snap)
	s.addPeople(ctx, pool, &snap)
	s.addPlayback(&snap)
	writeJSON(w, snap)
}

// addSweepRun reports the health sweep, whichever kind is running (see discovery.go). It is
// the one run that knows its own total up front, because it walks a list rather than
// draining a queue.
func (s *Server) addSweepRun(snap *activitySnapshot) {
	st := s.sweepJob.snapshot()
	if !st.Running {
		return
	}
	snap.Runs = append(snap.Runs, runStatus{
		Kind: runSweep, Label: "Health sweep", Detail: st.Scope, Done: st.Done, Total: st.Total,
	})
}

// addImports splits the import queue: a copy in flight is activity (it has real byte
// progress), everything still queued only feeds the run's denominator.
func (s *Server) addImports(ctx context.Context, pool *sql.DB, snap *activitySnapshot) {
	rows, err := db.ListActiveImports(ctx, pool)
	if err != nil {
		return
	}
	s.progMu.Lock()
	for i := range rows {
		if p, ok := s.progress[rows[i].ID]; ok {
			rows[i].Copied, rows[i].Total = p.copied, p.total
		}
	}
	s.progMu.Unlock()
	for _, row := range rows {
		if row.Status != db.StatusImporting {
			continue
		}
		title := row.Title
		if title == "" {
			title = row.Filename
		}
		snap.Activity = append(snap.Activity, activityItem{
			Key: "import-" + strconv.FormatInt(row.ID, 10), Kind: runImport,
			Title: title, Detail: row.Category, Percent: bytePercent(row.Copied, row.Total),
		})
	}
	s.addRun(snap, runImport, "Importing", len(rows))
}

// addOptimize reports encodes only. A claimed task is not yet work: the agent probes it
// first and a source live HLS can stream-copy finishes without encoding at all, so listing
// claimed rows strobed the page with bars that appeared and vanished in one poll. A live
// percent means ffmpeg is running, which is exactly the rule.
func (s *Server) addOptimize(ctx context.Context, pool *sql.DB, snap *activitySnapshot) {
	active, err := db.ListActiveTasks(ctx, pool)
	if err != nil {
		return
	}
	s.optMu.Lock()
	for _, t := range active {
		pct, encoding := s.optPercent[t.ID]
		if !encoding {
			continue
		}
		snap.Activity = append(snap.Activity, activityItem{
			Key: "optimize-" + strconv.FormatInt(t.ID, 10), Kind: runOptimize,
			Title: t.Title, Detail: t.File + " - " + t.Agent, Percent: pct,
		})
	}
	s.optMu.Unlock()
	pending, err := db.CountPending(ctx, pool)
	if err != nil {
		return
	}
	s.addRun(snap, runOptimize, "Optimizing", pending+len(active))
}

func (s *Server) addEnrich(ctx context.Context, pool *sql.DB, snap *activitySnapshot) {
	active, err := db.ListActiveEnrich(ctx, pool)
	if err != nil {
		return
	}
	for _, t := range active {
		snap.Activity = append(snap.Activity, activityItem{
			Key: "enrich-" + strconv.FormatInt(t.ID, 10), Kind: runEnrich,
			Title: t.Title, Detail: t.Agent, State: "looking up", Percent: -1,
		})
	}
	pending, err := db.CountPendingEnrich(ctx, pool)
	if err != nil {
		return
	}
	s.addRun(snap, runEnrich, "Enriching", pending+len(active))
}

func (s *Server) addThumbnails(ctx context.Context, pool *sql.DB, snap *activitySnapshot) {
	active, err := db.ListActiveThumbnail(ctx, pool)
	if err != nil {
		return
	}
	for _, t := range active {
		snap.Activity = append(snap.Activity, activityItem{
			Key: "thumbnail-" + strconv.FormatInt(t.ID, 10), Kind: runThumbnail,
			Title: t.Title, Detail: t.Agent, State: "generating", Percent: -1,
		})
	}
	pending, err := db.CountPendingThumbnail(ctx, pool)
	if err != nil {
		return
	}
	s.addRun(snap, runThumbnail, "Thumbnails", pending+len(active))
}

func (s *Server) addProbes(ctx context.Context, pool *sql.DB, snap *activitySnapshot) {
	active, err := db.ListActiveProbe(ctx, pool)
	if err != nil {
		return
	}
	for _, t := range active {
		snap.Activity = append(snap.Activity, activityItem{
			Key: "probe-" + strconv.FormatInt(t.ID, 10), Kind: runProbe,
			Title: t.Title, Detail: t.Agent, State: "probing", Percent: -1,
		})
	}
	pending, err := db.CountPendingProbe(ctx, pool)
	if err != nil {
		return
	}
	s.addRun(snap, runProbe, "Probing", pending+len(active))
}

func (s *Server) addTMDb(ctx context.Context, pool *sql.DB, snap *activitySnapshot) {
	active, err := db.ListActiveTMDb(ctx, pool)
	if err != nil {
		return
	}
	for _, t := range active {
		snap.Activity = append(snap.Activity, activityItem{
			Key: "tmdb-" + strconv.FormatInt(t.ID, 10), Kind: runTMDb,
			Title: t.Title, Detail: t.Agent, State: "looking up", Percent: -1,
		})
	}
	pending, err := db.CountPendingTMDb(ctx, pool)
	if err != nil {
		return
	}
	s.addRun(snap, runTMDb, "Matching on TMDb", pending+len(active))
}

func (s *Server) addPeople(ctx context.Context, pool *sql.DB, snap *activitySnapshot) {
	active, err := db.ListActivePeople(ctx, pool)
	if err != nil {
		return
	}
	for _, t := range active {
		snap.Activity = append(snap.Activity, activityItem{
			Key: "people-" + strconv.FormatInt(t.ID, 10), Kind: runPeople,
			Title: t.Title, Detail: t.Agent, State: "fetching cast", Percent: -1,
		})
	}
	pending, err := db.CountPendingPeople(ctx, pool)
	if err != nil {
		return
	}
	s.addRun(snap, runPeople, "Cast photos", pending+len(active))
}

// addPlayback lists live transcode sessions. It reads the manager field directly rather than
// through hlsManager(), which would build one (detecting the encoder) just to be told nobody
// is watching. Only transcoded playback is visible: a direct-played file is served as byte
// ranges and has no session at all.
func (s *Server) addPlayback(snap *activitySnapshot) {
	s.mu.RLock()
	m := s.hls
	s.mu.RUnlock()
	if m == nil {
		return
	}
	for i, sess := range m.ListSessions() {
		state := "transcoding"
		if sess.Remux {
			state = "stream-copying"
		}
		snap.Activity = append(snap.Activity, activityItem{
			Key: "playback-" + strconv.Itoa(i), Kind: "playback",
			Title: sess.Title, Detail: "live playback", State: state, Percent: -1,
		})
	}
}

// addRun folds a queue's remaining count into its drain run and appends it when one is in
// flight.
func (s *Server) addRun(snap *activitySnapshot, kind, label string, remaining int) {
	if done, total, ok := s.drains.observe(kind, remaining); ok {
		snap.Runs = append(snap.Runs, runStatus{Kind: kind, Label: label, Done: done, Total: total})
	}
}

// bytePercent renders a copy's progress, guarding the empty-total case.
func bytePercent(copied, total int64) int {
	if total <= 0 {
		return 0
	}
	if p := int(copied * 100 / total); p < 100 {
		return p
	}
	return 100
}
