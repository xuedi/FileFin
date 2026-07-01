// Package thumbnail holds the orchestration-free ffmpeg helpers behind the background
// thumbnail agent: deriving two fixed-size WebP posters from a base poster image (a
// detail-page poster with aspect preserved, and a category/home tile cropped to 2:3),
// and, for other-media folders with no poster, extracting a cropped video frame to write
// the folder's base poster. The server package owns the queue and agent.
package thumbnail

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"filefin/internal/ffrun"
)

// Fixed pixel targets (Standard 1x). The detail poster preserves aspect at this width;
// the tile is cropped to exactly TileWidth x TileHeight (2:3). The filenames embed the
// width so the on-disk name and the pixel target can never drift.
const (
	DetailWidth = 280
	TileWidth   = 180
	TileHeight  = 270

	// frameSeek skips a likely-black frame 0 when extracting an other-media poster.
	frameSeek = "3"
)

// DetailName is the detail-page poster filename ("poster_280.webp").
func DetailName() string { return "poster_" + strconv.Itoa(DetailWidth) + ".webp" }

// TileName is the category/home tile poster filename ("poster_180.webp").
func TileName() string { return "poster_" + strconv.Itoa(TileWidth) + ".webp" }

// Detail scales the base poster src to DetailWidth, preserving aspect, and writes dst as
// WebP.
func Detail(ctx context.Context, ffmpeg, src, dst string) error {
	vf := fmt.Sprintf("scale=%d:-2", DetailWidth)
	return encode(ctx, ffmpeg, nil, src, vf, dst)
}

// Tile scales the base poster src to cover TileWidth x TileHeight and center-crops it to
// that exact 2:3 size, writing dst as WebP.
func Tile(ctx context.Context, ffmpeg, src, dst string) error {
	vf := fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=increase,crop=%d:%d",
		TileWidth, TileHeight, TileWidth, TileHeight)
	return encode(ctx, ffmpeg, nil, src, vf, dst)
}

// FramePoster grabs one frame from the video, crops it to the largest 2:3 area, and
// writes dst (the folder's base poster.webp) as WebP. It first seeks a small offset to
// skip a black opening frame; if that yields no frame (a very short clip) it retries from
// the start.
func FramePoster(ctx context.Context, ffmpeg, video, dst string) error {
	vf := "crop='min(iw,ih*2/3)':'min(ih,iw*3/2)'"
	if err := encode(ctx, ffmpeg, []string{"-ss", frameSeek}, video, vf, dst); err != nil {
		return encode(ctx, ffmpeg, nil, video, vf, dst)
	}
	return nil
}

// encode runs one ffmpeg image pipeline atomically: it encodes a single frame of src
// through the -vf filtergraph to a sibling ".tmp" file and renames it into place on
// success, so a crash never leaves a half-written WebP. preInput carries input options
// (e.g. an -ss seek) placed before -i.
func encode(ctx context.Context, ffmpeg string, preInput []string, src, vf, dst string) error {
	tmp := dst + ".tmp"
	args := append([]string{"-y", "-nostdin"}, preInput...)
	// Confine ffmpeg to local files so a crafted media file cannot pivot to a network input.
	args = append(args, "-protocol_whitelist", "file,crypto,data", "-i", src, "-vf", vf, "-frames:v", "1", "-an", "-f", "webp", tmp)

	if err := ffrun.Run(ctx, ffmpeg, args...); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename thumbnail %s: %w", dst, err)
	}
	return nil
}
