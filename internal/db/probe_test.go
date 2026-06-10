package db

import (
	"path/filepath"
	"testing"
)

func TestProbeFormatAndQueue(t *testing.T) {
	ctx, pool := mediaTestPool(t)

	dir := "/data/Movies/(1966) Django"
	if err := InsertMedia(ctx, pool, Media{ID: "m1", Path: dir, Year: 1966, Title: "Django"}); err != nil {
		t.Fatal(err)
	}
	// One file inserted without a probed format (the rebuild/reconcile shape).
	if err := InsertMediaFile(ctx, pool, MediaFile{
		MediaID: "m1", Idx: 0, Path: filepath.Join(dir, "(1966) Django.avi"),
		Name: "(1966) Django.avi", Ext: ".avi",
	}); err != nil {
		t.Fatal(err)
	}

	// A file with empty format columns is a missing-format candidate.
	missing, err := MediaMissingFormat(ctx, pool)
	if err != nil {
		t.Fatal(err)
	}
	if len(missing) != 1 || missing[0] != "m1" {
		t.Fatalf("MediaMissingFormat = %v, want [m1]", missing)
	}

	// The probe agent fills the true format; the row then reads it back.
	if err := SetMediaFileFormat(ctx, pool, "m1", 0, "mov,mp4,m4a,3gp,3g2,mj2", "h264", "aac"); err != nil {
		t.Fatal(err)
	}
	f, ok, err := FileAt(ctx, pool, "m1", 0)
	if err != nil || !ok {
		t.Fatalf("FileAt: ok=%v %v", ok, err)
	}
	if f.Container != "mov,mp4,m4a,3gp,3g2,mj2" || f.VideoCodec != "h264" || f.AudioCodec != "aac" {
		t.Fatalf("format not stored: %+v", f)
	}
	// No longer missing once filled.
	if missing, _ := MediaMissingFormat(ctx, pool); len(missing) != 0 {
		t.Fatalf("MediaMissingFormat after fill = %v, want []", missing)
	}

	// Queue lifecycle: upsert -> claim -> finish.
	if err := UpsertPendingProbe(ctx, pool, "m1"); err != nil {
		t.Fatal(err)
	}
	if n, _ := CountPendingProbe(ctx, pool); n != 1 {
		t.Fatalf("pending = %d, want 1", n)
	}
	task, ok, err := ClaimNextProbe(ctx, pool, "PROBE")
	if err != nil || !ok || task.MediaID != "m1" {
		t.Fatalf("claim: ok=%v task=%+v %v", ok, task, err)
	}
	active, err := ListActiveProbe(ctx, pool)
	if err != nil || len(active) != 1 || active[0].Title != "Django" {
		t.Fatalf("active = %+v %v", active, err)
	}
	if err := FinishProbe(ctx, pool, task.ID); err != nil {
		t.Fatal(err)
	}
	if n, _ := CountPendingProbe(ctx, pool); n != 0 {
		t.Fatalf("pending after finish = %d, want 0", n)
	}
}
