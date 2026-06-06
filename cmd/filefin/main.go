// Command filefin is a single binary that is both the CLI and the HTTP media
// server. It always runs as exactly one subcommand.
package main

import (
	"fmt"
	"os"

	"github.com/urfave/cli/v2"

	"filefin/internal/config"
	"filefin/internal/logging"
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
			{Name: "optimize", Usage: "pre-transcode non-native media to direct-play copies, then exit", Action: cmdOptimize},
			{
				Name:      "import",
				Usage:     "copy a media file into the data directory",
				ArgsUsage: "[category] <file>",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "category", Usage: "target category"},
					&cli.StringFlag{Name: "title", Usage: "override the detected title"},
					&cli.IntFlag{Name: "year", Usage: "override the detected year"},
					&cli.IntFlag{Name: "season", Usage: "season number (for an episode)"},
					&cli.IntFlag{Name: "episode", Usage: "episode number (for an episode)"},
					&cli.IntFlag{Name: "part", Usage: "part number (for one file of a multi-file item)"},
					&cli.BoolFlag{Name: "yes", Aliases: []string{"y"}, Usage: "accept detected values without prompting"},
					&cli.BoolFlag{Name: "move", Usage: "move the file instead of copying"},
					&cli.BoolFlag{Name: "force", Usage: "overwrite an existing target file"},
					&cli.BoolFlag{Name: "no-fetch", Usage: "skip OMDb metadata/poster lookup"},
				},
				Action: cmdImport,
			},
			{
				Name:      "plex",
				Usage:     "import media and metadata from a Plex database",
				ArgsUsage: "<library.db>",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "metadata-dir", Usage: "Plex Metadata directory (default: derived from the db path)"},
					&cli.StringSliceFlag{Name: "remap", Usage: "rewrite a source path prefix, old=new (repeatable)"},
					&cli.StringFlag{Name: "section", Usage: "only import this Plex section"},
					&cli.StringFlag{Name: "category", Usage: "override the target category (default: the Plex section name)"},
					&cli.IntFlag{Name: "limit", Usage: "limit the number of items (for testing)"},
					&cli.BoolFlag{Name: "dry-run", Usage: "show the plan without writing anything"},
					&cli.BoolFlag{Name: "yes", Aliases: []string{"y"}, Usage: "skip the confirmation prompt"},
					&cli.BoolFlag{Name: "force", Usage: "overwrite existing media folders"},
					&cli.BoolFlag{Name: "no-posters", Usage: "do not copy posters"},
					&cli.BoolFlag{Name: "no-fetch", Usage: "skip OMDb metadata lookup (use Plex metadata only)"},
				},
				Action: cmdPlex,
			},
			{
				Name:      "jellyfin",
				Usage:     "import a Jellyfin/NFO media library directory",
				ArgsUsage: "[category] <source-dir>",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "category", Usage: "target category"},
					&cli.BoolFlag{Name: "dry-run", Usage: "show the plan without writing anything"},
					&cli.BoolFlag{Name: "yes", Aliases: []string{"y"}, Usage: "skip the confirmation prompt"},
					&cli.BoolFlag{Name: "force", Usage: "overwrite existing media folders"},
					&cli.BoolFlag{Name: "no-posters", Usage: "do not copy posters"},
				},
				Action: cmdJellyfin,
			},
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

// openLogger builds the app logger from config, returning a closer to defer. A bad
// level/output falls back to an STDOUT info logger rather than failing the command.
func openLogger(cfg *config.Config) (*logging.Logger, func()) {
	lg, closer, err := logging.Open(cfg.LogLevel, cfg.LogOutput)
	if err != nil {
		lg = logging.New(logging.Info, os.Stdout)
		lg.For(logging.Backend).Error("log setup failed, using stdout/info", logging.Fields{"error": err.Error()})
		return lg, func() {}
	}
	return lg, func() { _ = closer.Close() }
}
