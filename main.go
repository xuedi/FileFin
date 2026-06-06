// Command filefin is a single binary that is both the CLI and the HTTP media
// server. It always runs as exactly one subcommand.
package main

import (
	"fmt"
	"os"

	"github.com/urfave/cli/v2"

	"filefin/internal/config"
)

func main() {
	app := &cli.App{
		Name:  config.AppName,
		Usage: "a filesystem-driven media server",
		Commands: []*cli.Command{
			{
				Name:      "setup",
				Usage:     "create the data directory and write the global config",
				ArgsUsage: "<data-dir>",
				Flags: []cli.Flag{
					&cli.BoolFlag{Name: "force", Usage: "overwrite an existing config"},
					&cli.StringFlag{Name: "user", Usage: "admin username (skips the prompt)"},
					&cli.StringFlag{Name: "password", Usage: "admin password (skips the prompt; for scripting)"},
				},
				Action: cmdSetup,
			},
			{Name: "serve", Usage: "run the media server", Action: cmdServe},
			{Name: "validate", Usage: "check that the data directory is readable", Action: cmdValidate},
			{Name: "rebuild", Usage: "rebuild the cache database from the data directory", Action: cmdRebuild},
			{Name: "import", Usage: "import a media file (not implemented)", Action: notImplemented},
			{Name: "plex", Usage: "import from a Plex database (not implemented)", Action: notImplemented},
			{Name: "jellyfin", Usage: "import from a Jellyfin library (not implemented)", Action: notImplemented},
		},
	}
	if err := app.Run(os.Args); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func notImplemented(c *cli.Context) error {
	return fmt.Errorf("%q is not implemented yet", c.Command.Name)
}

func loadConfig() (*config.Config, error) {
	path, err := config.DefaultPath()
	if err != nil {
		return nil, err
	}
	cfg, err := config.Load(path)
	if err != nil {
		return nil, fmt.Errorf("could not load config at %s (run `setup` first?): %w", path, err)
	}
	return cfg, nil
}
