package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"

	"filefin/internal/config"
	"filefin/internal/db"
	"filefin/internal/importer"
)

// runRenameUser renames a user account everywhere a username is a key: the config Users map
// and the per-user playback state inside every media folder's meta.json. The disposable cache
// is dropped afterwards so its username-keyed mirrors rebuild from the corrected sources.
//
// The service must be stopped while this runs: it holds the config in memory and would
// otherwise overwrite it and race meta.json writes. Matching is case-insensitive, so an
// account created before usernames were emails ("xuedi") is found by its stored key.
func runRenameUser(args []string) {
	fset := flag.NewFlagSet("rename-user", flag.ExitOnError)
	dryRun := fset.Bool("dry-run", false, "report what would change without writing anything")
	fset.Usage = func() {
		fmt.Fprint(os.Stderr, "Usage: filefin rename-user [--dry-run] <old-username> <new-username>\n"+
			"Stop the filefin service before running this.\n")
	}
	_ = fset.Parse(args)
	if fset.NArg() != 2 {
		fset.Usage()
		os.Exit(2)
	}
	if err := renameUser(fset.Arg(0), fset.Arg(1), *dryRun); err != nil {
		log.Fatalf("rename-user: %v", err)
	}
}

func renameUser(oldArg, newArg string, dryRun bool) error {
	if !config.Exists() {
		return errors.New("no config found")
	}
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	oldNorm := config.NormalizeUsername(oldArg)
	oldKey := ""
	for k := range cfg.Users {
		if config.NormalizeUsername(k) == oldNorm {
			oldKey = k
			break
		}
	}
	if oldKey == "" {
		return fmt.Errorf("no account matches %q", oldArg)
	}

	newKey := config.NormalizeUsername(newArg)
	if newKey == "" {
		return errors.New("new username is empty")
	}
	if newKey == config.NormalizeUsername(oldKey) {
		return fmt.Errorf("old and new usernames are the same (%q)", newKey)
	}
	for k := range cfg.Users {
		if k != oldKey && config.NormalizeUsername(k) == newKey {
			return fmt.Errorf("target %q collides with existing account %q", newKey, k)
		}
	}

	// Rename the state key in every media folder's meta.json that carries it. The old key's
	// value is moved verbatim (its Updated stamp preserved, so home ordering is unchanged).
	folders, err := metaFolders(cfg.DataDir)
	if err != nil {
		return fmt.Errorf("scan data dir: %w", err)
	}
	mgr := importer.NewManager()
	updated := 0
	for _, folder := range folders {
		st, err := importer.LoadState(folder)
		if err != nil {
			return fmt.Errorf("read state %s: %w", folder, err)
		}
		hitKey := ""
		for k := range st {
			if config.NormalizeUsername(k) == oldNorm {
				hitKey = k
				break
			}
		}
		if hitKey == "" {
			continue
		}
		updated++
		if dryRun {
			continue
		}
		if _, err := mgr.Update(folder, func(m importer.Meta) importer.Meta {
			us, ok := m.State[hitKey]
			if !ok {
				return m
			}
			delete(m.State, hitKey)
			m.State[newKey] = us
			return m
		}); err != nil {
			return fmt.Errorf("update %s: %w", folder, err)
		}
	}

	fmt.Printf("account %q -> %q\n", oldKey, newKey)
	if dryRun {
		fmt.Printf("meta.json playback-state entries to update: %d\n", updated)
		fmt.Println("dry run: config not written, cache not removed")
		return nil
	}
	fmt.Printf("meta.json playback-state entries updated: %d\n", updated)

	u := cfg.Users[oldKey]
	delete(cfg.Users, oldKey)
	cfg.Users[newKey] = u
	if err := config.Save(cfg); err != nil {
		return fmt.Errorf("save config: %w", err)
	}

	// Drop the disposable cache so its username-keyed mirrors (users, user_state) rebuild
	// from the corrected config + meta.json on the next cache open.
	if err := db.RemoveCache(); err != nil {
		fmt.Printf("warning: could not remove cache (rebuild it from the admin page): %v\n", err)
	}
	fmt.Println("config updated and cache dropped; start the service, then rebuild the library cache")
	return nil
}

// metaFolders returns every directory under root that contains a meta.json.
func metaFolders(root string) ([]string, error) {
	if root == "" {
		return nil, errors.New("config has no dataDir")
	}
	var out []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && d.Name() == "meta.json" {
			out = append(out, filepath.Dir(p))
		}
		return nil
	})
	return out, err
}
