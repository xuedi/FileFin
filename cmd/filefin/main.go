// Command filefin is the whole appliance: a tiny subcommand dispatcher in front of the
// server. `serve` (the default) runs the HTTP server; `setup` writes a pending config and
// prints the token-gated web-install URL; `version` prints the release number.
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"filefin/internal/config"
	"filefin/internal/server"
	"filefin/internal/version"
)

func main() {
	args := os.Args[1:]
	cmd := "serve"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	switch cmd {
	case "serve":
		runServe(args)
	case "setup":
		runSetup(args)
	case "rename-user":
		runRenameUser(args)
	case "version", "--version", "-v":
		fmt.Println(version.Version)
	case "help", "--help", "-h":
		usage(os.Stdout)
	default:
		fmt.Fprintf(os.Stderr, "filefin: unknown command %q\n\n", cmd)
		usage(os.Stderr)
		os.Exit(2)
	}
}

func usage(w *os.File) {
	fmt.Fprint(w, `FileFin - a small, self-hosted media server.

Usage:
  filefin [serve] [--port N] [--bind ADDR]   run the server (default command)
  filefin setup [--port N] [--bind ADDR] [--data DIR]
                                             prepare a pending install and print the setup URL
  filefin rename-user [--dry-run] OLD NEW    rename an account across the config and every
                                             folder's playback state (stop the service first)
  filefin version                            print the release version

Ports below 1024 need CAP_NET_BIND_SERVICE (the packaged systemd unit grants it) or root.
`)
}

// runServe runs the server. On a first run with no config it bootstraps a pending config from
// the flags/defaults and serves it (the setup URL is logged prominently); with an existing
// config it loads that and the flags are ignored (a differing --port is warned about).
func runServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	port := fs.Int("port", 0, "port to serve on (only used on a first run with no config)")
	bind := fs.String("bind", "", "bind address; empty = all interfaces, e.g. 127.0.0.1 to pin to loopback behind a proxy")
	_ = fs.Parse(args)

	cfg, created, err := bootstrapServe(*port, *bind)
	if err != nil {
		log.Fatalf("could not create initial config: %v", err)
	}
	if created {
		fmt.Println("No config found - a pending install was created. Complete setup in a browser:")
		printSetupURLs(cfg)
	}

	if err := server.Run(); err != nil {
		log.Fatal(err)
	}
}

// bootstrapServe prepares the config the server will load: when none exists it creates a
// pending config from port/bind (returning it with created=true); when one exists it warns on
// a differing --port and returns created=false.
func bootstrapServe(port int, bind string) (cfg *config.Config, created bool, err error) {
	if config.Exists() {
		if port != 0 {
			if existing, e := config.Load(); e == nil && port != existing.Port {
				log.Printf("--port %d ignored: the existing config is set to port %d", port, existing.Port)
			}
		}
		return nil, false, nil
	}
	p := port
	if p == 0 {
		p = config.DefaultPort
	}
	cfg, err = config.Create(p, bind)
	if err != nil {
		return nil, false, err
	}
	return cfg, true, nil
}

// runSetup is the non-serving bootstrap the packaged path uses: it writes (or refreshes) a
// pending config with a fresh token, then prints the web-install URL and next steps and exits.
// It refuses to run once setup is already complete.
func runSetup(args []string) {
	fs := flag.NewFlagSet("setup", flag.ExitOnError)
	port := fs.Int("port", 0, "port to serve on (default 8080, or the existing pending port)")
	bind := fs.String("bind", "", "bind address; empty = all interfaces, 127.0.0.1 to pin to loopback behind a proxy")
	data := fs.String("data", "", "optional data directory (otherwise chosen in the web installer)")
	_ = fs.Parse(args)

	cfg, err := doSetup(*port, *bind, *data)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Pending install ready. Next steps:")
	fmt.Println("  1. Start the service (e.g. `systemctl enable --now filefin`).")
	fmt.Println("  2. Open one of these URLs in a browser and set the admin account + data folder:")
	printSetupURLs(cfg)
	fmt.Println()
	fmt.Println("If none of those hosts is reachable, use this template with your server's address:")
	fmt.Printf("  http://<host>:%d/?token=%s\n", cfg.Port, cfg.SetupToken)
}

// doSetup writes or refreshes a pending config with a fresh token and returns it. It refuses
// once setup is complete (an admin exists). A present pending config is kept and only the given
// overrides (port/bind/data) are applied; absent, it starts from the defaults.
func doSetup(port int, bind, data string) (*config.Config, error) {
	cfg := &config.Config{Port: config.DefaultPort, Users: map[string]config.User{}}
	if config.Exists() {
		existing, err := config.Load()
		if err != nil {
			return nil, fmt.Errorf("could not read the existing config: %w", err)
		}
		if existing.SetupComplete() {
			return nil, errors.New("setup already complete: an admin account exists; use the web UI to manage the server")
		}
		cfg = existing
	}
	if port != 0 {
		cfg.Port = port
	}
	if bind != "" {
		cfg.BindAddress = bind
	}
	if data != "" {
		abs := filepath.Clean(data)
		if !filepath.IsAbs(abs) {
			return nil, fmt.Errorf("--data must be an absolute path: %q", data)
		}
		cfg.DataDir = abs
	}
	token, err := config.NewSetupToken()
	if err != nil {
		return nil, fmt.Errorf("could not mint a setup token: %w", err)
	}
	cfg.SetupToken = token
	if err := config.Save(cfg); err != nil {
		return nil, fmt.Errorf("could not write the config: %w", err)
	}
	return cfg, nil
}

// printSetupURLs prints every candidate token-bearing install URL, indented.
func printSetupURLs(cfg *config.Config) {
	for _, u := range server.SetupURLs(cfg) {
		fmt.Printf("     %s\n", u)
	}
}
