package main

import (
	"bufio"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/urfave/cli/v2"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/term"

	"filefin/internal/config"
)

func cmdSetup(c *cli.Context) error {
	if c.NArg() < 1 {
		return errors.New("usage: setup <data-dir>")
	}
	cfgPath, err := config.DefaultPath()
	if err != nil {
		return err
	}
	if _, err := os.Stat(cfgPath); err == nil && !c.Bool("force") {
		return fmt.Errorf("config already exists at %s (use --force to overwrite)", cfgPath)
	}
	dataDir, err := filepath.Abs(c.Args().First())
	if err != nil {
		return err
	}
	user := c.String("user")
	if user == "" {
		if user, err = prompt("Admin username: "); err != nil {
			return err
		}
	}
	pass := c.String("password")
	if pass == "" {
		if pass, err = promptPassword("Password: "); err != nil {
			return err
		}
	}

	if err := performSetup(cfgPath, dataDir, user, pass); err != nil {
		return err
	}
	fmt.Printf("Wrote config to %s\nData directory: %s\n", cfgPath, dataDir)
	return nil
}

// performSetup is the testable core of `setup`: validate input, create the data
// directory, and write the config with a hashed password.
func performSetup(cfgPath, dataDir, user, pass string) error {
	user = strings.TrimSpace(user)
	if user == "" {
		return errors.New("username cannot be empty")
	}
	if pass == "" {
		return errors.New("password cannot be empty")
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return err
	}
	cachePath, err := config.DefaultCachePath()
	if err != nil {
		return err
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(pass), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	cfg := config.New()
	cfg.DataDir = dataDir
	cfg.CachePath = cachePath
	cfg.Users[user] = config.User{Hash: string(hash), Admin: true}
	return cfg.Save(cfgPath)
}

func prompt(label string) (string, error) {
	fmt.Print(label)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.TrimSpace(line), err
}

func promptPassword(label string) (string, error) {
	fmt.Print(label)
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	return string(b), err
}
