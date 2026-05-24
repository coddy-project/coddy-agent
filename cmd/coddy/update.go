package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/update"
)

func runUpdate(args []string) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	check := fs.Bool("check", false, "report whether a newer release exists and exit")
	yes := false
	fs.BoolVar(&yes, "y", false, "install without confirmation")
	fs.BoolVar(&yes, "yes", false, "install without confirmation (same as -y)")
	version := fs.String("version", "", "install a specific release tag (X.Y.Z) instead of latest")
	repo := fs.String("repo", update.DefaultRepo, "GitHub repository owner/name for releases")
	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), "Usage of update:\n")
		fs.PrintDefaults()
		_, _ = fmt.Fprintf(fs.Output(), "\nDownloads release assets from https://github.com/%s/releases\n", update.DefaultRepo)
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	err := update.Run(context.Background(), update.Options{
		Repo:           strings.TrimSpace(*repo),
		TargetVersion:  strings.TrimSpace(*version),
		CheckOnly:      *check,
		Yes:            yes,
		Stdout:         os.Stdout,
	})
	if errors.Is(err, update.ErrUpdateAvailable) {
		os.Exit(1)
	}
	return err
}
