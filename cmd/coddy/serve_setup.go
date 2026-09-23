package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/EvilFreelancer/coddy-agent/internal/serve"
)

func runServeSetup(args []string) error {
	fs := flag.NewFlagSet("serve setup", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() {
		_, _ = fmt.Fprintln(fs.Output(), "Usage: coddy serve setup (enable and check the packaged systemd user service)")
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("coddy serve setup takes no arguments")
	}
	return serve.SetupUserService(context.Background(), os.Stdout)
}
