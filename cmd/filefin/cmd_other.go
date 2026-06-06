package main

import (
	"context"
	"fmt"
	"net/http"
	"runtime"
	"time"

	"github.com/urfave/cli/v2"

	"filefin/internal/cache"
	"filefin/internal/config"
	"filefin/internal/logging"
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
	lg, closeLog := openLogger(cfg)
	defer closeLog()
	blog := lg.For(logging.Backend)

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
	srv := server.New(cfg, store, enc, lg)
	defer srv.Close()
	blog.Info(fmt.Sprintf("serving on http://localhost%s", addr), logging.Fields{
		"port": cfg.Port, "media": countMedia(scan), "data_dir": cfg.DataDir,
		"gpu": enc.Kind == "vaapi", "encoder": enc.Kind, "device": enc.Device,
	})
	blog.Info(gpuStatusLine(cfg, enc))
	if cfg.OptimizeEnabled {
		// Clear locks left by a crashed run before any worker starts (serve owns the
		// data dir at this point, so no live lock is removed).
		if n, err := optimize.SweepStaleLocks(cfg.DataDir); err == nil && n > 0 {
			blog.Info(fmt.Sprintf("cleared %d stale optimize lock(s)", n))
		}
		workers := optimizeWorkers(cfg)
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go optimize.NewWorker(optimize.Options{
			DataDir:    cfg.DataDir,
			FFmpeg:     cfg.FFmpegPath,
			FFprobe:    cfg.FFprobePath,
			Encoder:    enc,
			CPUEncoder: transcode.SoftwareEncoder(),
			MaxWorkers: workers,
			TargetLoad: cfg.OptimizeTargetLoad,
			Busy:       srv.TranscodeActive,
			Logger:     lg,
		}).Run(ctx)
		blog.Info("optimize worker enabled (pre-transcoding to direct-play copies)",
			logging.Fields{"max_workers": workers, "target_load": optimizeTargetLoad(cfg)})
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
	lg, closeLog := openLogger(cfg)
	defer closeLog()
	enc := probeEncoder(cfg)
	olog := lg.For(logging.Optimizer)
	if enc.Kind == "vaapi" {
		olog.Info("optimizing with GPU (VAAPI)", logging.Fields{"device": enc.Device})
	} else {
		olog.Info("optimizing with software encoder (libx264)")
	}
	return optimize.NewWorker(optimize.Options{
		DataDir:    cfg.DataDir,
		FFmpeg:     cfg.FFmpegPath,
		FFprobe:    cfg.FFprobePath,
		Encoder:    enc,
		CPUEncoder: transcode.SoftwareEncoder(),
		MaxWorkers: optimizeWorkers(cfg),
		TargetLoad: cfg.OptimizeTargetLoad,
		Logger:     lg,
	}).RunOnce(c.Context)
}

// optimizeWorkers resolves the concurrency ceiling for the optimize pool: the configured
// value, or the CPU count when unset. The adaptive manager scales up to this cap based on
// live CPU/GPU load, so it is a ceiling, not a fixed worker count.
func optimizeWorkers(cfg *config.Config) int {
	if cfg.OptimizeMaxWorkers > 0 {
		return cfg.OptimizeMaxWorkers
	}
	return runtime.NumCPU()
}

// optimizeTargetLoad resolves the grow-threshold percentage for logging.
func optimizeTargetLoad(cfg *config.Config) int {
	if cfg.OptimizeTargetLoad > 0 {
		return cfg.OptimizeTargetLoad
	}
	return 80
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
