package server

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"filefin/internal/db"
	"filefin/internal/logging"
)

const (
	// uploadTmpPrefix names the per-session working dir under the OS temp dir. Uploaded
	// files land here, are staged as preCheck rows, and the dir is removed after import.
	uploadTmpPrefix = "filefin-upload-"
	// uploadSweepTTL is the idle age after which an unreferenced upload dir (files picked
	// but never imported, or an abandoned session) is reclaimed.
	uploadSweepTTL = time.Hour
)

// handleUploadBegin creates a fresh /tmp working dir for one upload session and returns its
// opaque token (the dir's base name). Every file of the session is written into this dir;
// the client never learns or supplies a filesystem path.
func (s *Server) handleUploadBegin(w http.ResponseWriter, r *http.Request) {
	dir, err := os.MkdirTemp("", uploadTmpPrefix)
	if err != nil {
		http.Error(w, "could not create upload folder", http.StatusInternalServerError)
		return
	}
	writeJSON(w, struct {
		Session string `json:"session"`
	}{filepath.Base(dir)})
}

// uploadSessionDir validates a session token and returns its absolute dir. The token must
// be exactly a filefin-upload-* base name (no separators, no traversal), so a client can
// never reach outside the temp dir.
func uploadSessionDir(session string) (string, bool) {
	if session == "" || session != filepath.Base(session) || !strings.HasPrefix(session, uploadTmpPrefix) {
		return "", false
	}
	dir := filepath.Join(os.TempDir(), session)
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		return "", false
	}
	return dir, true
}

// handleUploadFile stores one uploaded file into its session dir, streaming straight to disk
// so a large video never buffers in memory. The multipart body must send the `session` field
// before the `file` part (the browser FormData append order guarantees this).
func (s *Server) handleUploadFile(w http.ResponseWriter, r *http.Request) {
	mr, err := r.MultipartReader()
	if err != nil {
		http.Error(w, "expected a multipart upload", http.StatusBadRequest)
		return
	}
	session := ""
	wrote := false
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			http.Error(w, "bad upload", http.StatusBadRequest)
			return
		}
		switch part.FormName() {
		case "session":
			b, _ := io.ReadAll(io.LimitReader(part, 256))
			session = strings.TrimSpace(string(b))
		case "file":
			dir, ok := uploadSessionDir(session)
			if !ok {
				part.Close()
				http.Error(w, "unknown upload session", http.StatusBadRequest)
				return
			}
			name := filepath.Base(part.FileName())
			if name == "" || name == "." || name == string(filepath.Separator) {
				part.Close()
				http.Error(w, "bad filename", http.StatusBadRequest)
				return
			}
			dst := filepath.Join(dir, name)
			f, err := os.Create(dst)
			if err != nil {
				part.Close()
				http.Error(w, "could not save the file", http.StatusInternalServerError)
				return
			}
			_, copyErr := io.Copy(f, part)
			closeErr := f.Close()
			if copyErr != nil || closeErr != nil {
				os.Remove(dst)
				part.Close()
				http.Error(w, "upload failed", http.StatusInternalServerError)
				return
			}
			wrote = true
		}
		part.Close()
	}
	if !wrote {
		http.Error(w, "no file in the upload", http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleUploadAssess stages the uploaded files of a session as preCheck rows for a category,
// exactly like the folder assessment but rooted at the session's /tmp dir and with
// delete_after forced on so the working files are cleaned up after import.
func (s *Server) handleUploadAssess(w http.ResponseWriter, r *http.Request) {
	req, err := decodeJSON[struct {
		Session    string `json:"session"`
		CategoryID int64  `json:"categoryId"`
	}](w, r)
	if err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	dir, ok := uploadSessionDir(req.Session)
	if !ok {
		http.Error(w, "unknown upload session", http.StatusBadRequest)
		return
	}
	cat, ok := s.categoryByID(req.CategoryID)
	if !ok {
		http.Error(w, "unknown category", http.StatusBadRequest)
		return
	}
	pool, err := s.ensureDB(r.Context())
	if err != nil {
		http.Error(w, "cache unavailable", http.StatusServiceUnavailable)
		return
	}
	rows, err := s.stageFolder(r.Context(), pool, cat, dir, true, db.OriginUpload)
	if err != nil {
		http.Error(w, "could not stage the uploaded files", http.StatusInternalServerError)
		return
	}
	s.markDuplicates(r.Context(), pool, rows)
	writeJSON(w, rows)
}

// isUploadDir reports whether dir is a session working dir directly under the temp dir.
func isUploadDir(dir string) bool {
	return strings.HasPrefix(filepath.Base(dir), uploadTmpPrefix) &&
		filepath.Clean(filepath.Dir(dir)) == filepath.Clean(os.TempDir())
}

// cleanupUploadDir removes an upload session's working dir once no unfinished import row
// still points into it. It is best-effort and a no-op for non-upload sources.
func (s *Server) cleanupUploadDir(ctx context.Context, pool *sql.DB, sourcePath string) {
	dir := filepath.Dir(sourcePath)
	if !isUploadDir(dir) {
		return
	}
	refs, err := db.CountUnfinishedImportsUnder(ctx, pool, dir+string(filepath.Separator))
	if err != nil || refs > 0 {
		return
	}
	_ = os.RemoveAll(dir)
}

// sweepUploadDirs reclaims upload working dirs that have gone idle past the TTL and are
// referenced by no unfinished import row: files uploaded but never imported, or sessions
// abandoned before assessment. A dir whose preCheck rows still exist is kept regardless of
// age, so an in-progress assessment is never pulled out from under the admin.
func (s *Server) sweepUploadDirs(ctx context.Context, pool *sql.DB) {
	entries, err := os.ReadDir(os.TempDir())
	if err != nil {
		return
	}
	removed := 0
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), uploadTmpPrefix) {
			continue
		}
		info, err := e.Info()
		if err != nil || time.Since(info.ModTime()) < uploadSweepTTL {
			continue
		}
		dir := filepath.Join(os.TempDir(), e.Name())
		refs, err := db.CountUnfinishedImportsUnder(ctx, pool, dir+string(filepath.Separator))
		if err != nil || refs > 0 {
			continue
		}
		if os.RemoveAll(dir) == nil {
			removed++
		}
	}
	if removed > 0 {
		s.logger().For(logging.Import).Info(fmt.Sprintf("cleared %d abandoned upload folder(s)", removed))
	}
}
