package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
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
	noRestart := fs.Bool("no-restart", false, "Windows only: install the update but do not start Coddy again")
	noNotes := fs.Bool("no-notes", false, "do not print what changed after the update (also "+update.NotesEnvVar+"=0)")
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
		Repo:          strings.TrimSpace(*repo),
		TargetVersion: strings.TrimSpace(*version),
		CheckOnly:     *check,
		Yes:           yes,
		NoRestart:     *noRestart,
		NoNotes:       *noNotes || update.NotesDisabledByEnv(),
		Stdout:        os.Stdout,
		AfterInstall:  renewCertificates,
	})
	if errors.Is(err, update.ErrUpdateAvailable) {
		os.Exit(1)
	}
	return err
}

// renewCertificates asks the binary an update just installed to renew the built-in TLS certificates, when they are in use (their
// directory exists or the configuration asks for them): the new binary knows the rules of the new version. What it printed is shown; a
// failure is a warning, never a failed update.
func renewCertificates(ctx context.Context, binary string, out io.Writer) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "tls", "ensure", "--if-used")
	platform.AdaptCommand(cmd)
	raw, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(raw))
	switch {
	case err != nil:
		_, _ = fmt.Fprintf(out, "warning: the TLS certificates were not renewed by the new binary (%v); run `coddy tls ensure`\n", err)
		if text != "" {
			_, _ = fmt.Fprintln(out, text)
		}
	case text != "":
		_, _ = fmt.Fprintln(out, text)
	}
}
