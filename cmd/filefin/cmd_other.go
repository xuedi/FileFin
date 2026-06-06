package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/urfave/cli/v2"

	"filefin/internal/cache"
	"filefin/internal/config"
	"filefin/internal/model"
	"filefin/internal/optimize"
	"filefin/internal/scanner"
	"filefin/internal/server"
	"filefin/internal/transcode"
)

func cmdValidate(c *cli.Context) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	scan, err := scanner.Scan(cfg.DataDir)
	if err != nil {
		return err
	}
	fmt.Printf("Scanned %d categories, %d media folders.\n", len(scan.Categories), countMedia(scan))
	if len(scan.Issues) == 0 {
		fmt.Println("No issues found.")
		return nil
	}
	fmt.Printf("\n%d issue(s):\n", len(scan.Issues))
	for _, issue := range scan.Issues {
		fmt.Println(" -", issue)
	}
	return fmt.Errorf("%d validation issue(s)", len(scan.Issues))
}

func cmdRebuild(c *cli.Context) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	scan, err := scanner.Scan(cfg.DataDir)
	if err != nil {
		return err
	}
	store, err := cache.Open(cfg.CachePath)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.Rebuild(scan); err != nil {
		return err
	}
	fmt.Printf("Rebuilt cache at %s: %d categories, %d media.\n", cfg.CachePath, len(scan.Categories), countMedia(scan))
	return nil
}

func cmdServe(c *cli.Context) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	scan, err := scanner.Scan(cfg.DataDir)
	if err != nil {
		return err
	}
	store, err := cache.Open(cfg.CachePath)
	if err != nil {
		return err
	}
	defer store.Close()
	if err := store.Rebuild(scan); err != nil {
		return err
	}
	addr := fmt.Sprintf(":%d", cfg.Port)
	enc := detectEncoder(cfg)
	srv := server.New(cfg, store, enc)
	defer srv.Close()
	fmt.Printf("Serving on http://localhost%s (data: %s, %d media)\n", addr, cfg.DataDir, countMedia(scan))
	fmt.Println(gpuStatusLine(cfg, enc))
	if cfg.OptimizeEnabled {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go optimize.NewWorker(optimize.Options{
			DataDir: cfg.DataDir,
			FFmpeg:  cfg.FFmpegPath,
			FFprobe: cfg.FFprobePath,
			Encoder: enc,
			Busy:    srv.TranscodeActive,
			Log:     func(f string, a ...any) { log.Printf(f, a...) },
		}).Run(ctx)
		fmt.Println("Optimize: background worker enabled (pre-transcoding to direct-play copies)")
	}
	return http.ListenAndServe(addr, srv.Handler())
}

// cmdOptimize runs the optimize backlog once and exits - an explicit-writer entry point
// for clearing the queue without keeping the server up.
func cmdOptimize(c *cli.Context) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	enc := probeEncoder(cfg)
	if enc.Kind == "vaapi" {
		fmt.Printf("Optimizing with GPU (VAAPI, %s)\n", enc.Device)
	} else {
		fmt.Println("Optimizing with software encoder (libx264)")
	}
	return optimize.NewWorker(optimize.Options{
		DataDir: cfg.DataDir,
		FFmpeg:  cfg.FFmpegPath,
		FFprobe: cfg.FFprobePath,
		Encoder: enc,
		Log:     func(f string, a ...any) { fmt.Printf(f+"\n", a...) },
	}).RunOnce(c.Context)
}

// detectEncoder picks the video encoder for serve, probing for a GPU once at startup.
// Skipped (software default) only when neither live transcoding nor the optimizer needs it.
func detectEncoder(cfg *config.Config) transcode.Encoder {
	if !cfg.TranscodeEnabled && !cfg.OptimizeEnabled {
		return transcode.Encoder{}
	}
	return probeEncoder(cfg)
}

// probeEncoder always runs GPU detection, honoring the hwaccel config.
func probeEncoder(cfg *config.Config) transcode.Encoder {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return transcode.DetectEncoder(ctx, transcode.Options{
		FFmpegPath:    cfg.FFmpegPath,
		HWAccel:       cfg.HWAccel,
		HWAccelDevice: cfg.HWAccelDevice,
	})
}

func gpuStatusLine(cfg *config.Config, enc transcode.Encoder) string {
	switch {
	case !cfg.TranscodeEnabled:
		return "GPU acceleration: transcoding disabled"
	case enc.Kind == "vaapi":
		return fmt.Sprintf("GPU acceleration: enabled (VAAPI, %s, %s)", enc.Device, enc.Codec)
	case cfg.HWAccel == "off":
		return "GPU acceleration: disabled by config - using software encoding (libx264)"
	default:
		return "GPU acceleration: not available - using software encoding (libx264)"
	}
}

func countMedia(scan *model.Scan) int {
	n := 0
	for _, cat := range scan.Categories {
		n += len(cat.Media)
	}
	return n
}
