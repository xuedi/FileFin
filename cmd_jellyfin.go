package main

import (
	"errors"
	"fmt"
	"path/filepath"

	"github.com/urfave/cli/v2"

	"filefin/internal/jellyfin"
)

func cmdJellyfin(c *cli.Context) error {
	cfg, err := loadConfig()
	if err != nil {
		return err
	}

	var category, source string
	switch c.NArg() {
	case 1:
		source = c.Args().Get(0)
	case 2:
		category, source = c.Args().Get(0), c.Args().Get(1)
	default:
		return errors.New("usage: jellyfin [category] <source-dir>")
	}
	if c.String("category") != "" {
		category = c.String("category")
	}
	if category == "" {
		if c.Bool("yes") {
			return errors.New("category is required (pass it as the first argument or --category)")
		}
		if category, err = chooseCategory(cfg.DataDir); err != nil {
			return err
		}
	}

	src, err := filepath.Abs(source)
	if err != nil {
		return err
	}
	items, err := jellyfin.Scan(src, category)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		fmt.Printf("No movies or shows found under %s\n", src)
		return nil
	}
	return planAndApply(c, items, cfg.DataDir)
}
