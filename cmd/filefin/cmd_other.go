package main

import (
	"fmt"
	"net/http"

	"github.com/urfave/cli/v2"

	"filefin/internal/cache"
	"filefin/internal/model"
	"filefin/internal/scanner"
	"filefin/internal/server"
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
	srv := server.New(cfg, store)
	defer srv.Close()
	fmt.Printf("Serving on http://localhost%s (data: %s, %d media)\n", addr, cfg.DataDir, countMedia(scan))
	return http.ListenAndServe(addr, srv.Handler())
}

func countMedia(scan *model.Scan) int {
	n := 0
	for _, cat := range scan.Categories {
		n += len(cat.Media)
	}
	return n
}
