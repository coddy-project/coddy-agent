package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/serve"
)

// stopGrace is how long a dispatcher is given to stop its worker and go before
// it is killed. It is the worker's own drain plus room for the shutdown around
// it, because the point of asking politely is that a turn in flight finishes.
const stopGrace = 45 * time.Second

// runServeStatus implements `coddy serve status`: what is running under this
// agent home, if anything.
//
// It reports the dispatcher rather than the worker, because the worker is
// replaced whenever it dies and its pid would be a number that stops being true
// without anything having gone wrong.
func runServeStatus(args []string) error {
	home, ok, err := controlHome(args, "status")
	if err != nil || !ok {
		return err
	}
	rec, err := serve.ReadRecord(home)
	if errors.Is(err, serve.ErrNoDispatcher) {
		fmt.Printf("coddy serve is not running (no dispatcher recorded in %s)\n", home)
		return nil
	}
	if err != nil {
		return err
	}
	if !rec.Running() {
		fmt.Printf("coddy serve is not running (pid %d from a previous run is gone)\n  log     %s\n", rec.PID, rec.Log)
		return nil
	}
	fmt.Printf("coddy serve %s is running\n  pid     %d\n  since   %s\n  config  %s\n  log     %s\n",
		rec.Version, rec.PID, rec.StartedAt.Local().Format(time.RFC3339), rec.Config, rec.Log)
	if len(rec.Args) > 0 {
		fmt.Printf("  args    %s\n", strings.Join(rec.Args, " "))
	}
	// Whether the surfaces are up is a different question from whether the
	// dispatcher is, and it is the one an operator came here to ask.
	if rec.Serving() {
		fmt.Printf("  worker  %d\n", rec.WorkerPID)
	} else {
		fmt.Printf("  worker  none right now, the dispatcher is retrying\n")
	}
	if rec.LastError != "" {
		fmt.Printf("  last    %s\n", rec.LastError)
	}
	return nil
}

// runServeStop implements `coddy serve stop`.
func runServeStop(args []string) error {
	home, ok, err := controlHome(args, "stop")
	if err != nil || !ok {
		return err
	}
	rec, err := serve.ReadRecord(home)
	if errors.Is(err, serve.ErrNoDispatcher) {
		fmt.Println("coddy serve is not running")
		return nil
	}
	if err != nil {
		return err
	}
	if !rec.Running() {
		// The record outlived the process, which is what a machine that went
		// down without a shutdown leaves behind. Clear it rather than making
		// the operator find and delete a file to be able to start again.
		fmt.Printf("coddy serve is not running (clearing the record of pid %d)\n", rec.PID)
		return serve.RemoveRecord(home)
	}
	if err := rec.Stop(stopGrace); err != nil {
		return fmt.Errorf("stop pid %d: %w", rec.PID, err)
	}
	fmt.Printf("coddy serve stopped (pid %d)\n", rec.PID)
	return nil
}

// runServeRestart implements `coddy serve restart`: the same daemon, started
// again, with the arguments it was started with the first time.
//
// Reading the arguments back off the record is what makes this usable at all. A
// daemon started with `-H 0.0.0.0 --swarm` that came back on the defaults would
// be a different daemon, and the operator who typed `restart` would have no way
// to know until something that used to answer stopped answering.
func runServeRestart(args []string) error {
	home, ok, err := controlHome(args, "restart")
	if err != nil || !ok {
		return err
	}
	rec, err := serve.ReadRecord(home)
	if errors.Is(err, serve.ErrNoDispatcher) {
		return errors.New("coddy serve is not running; start it with `coddy serve --daemon`")
	}
	if err != nil {
		return err
	}
	if rec.Running() {
		if err := rec.Stop(stopGrace); err != nil {
			return fmt.Errorf("stop pid %d: %w", rec.PID, err)
		}
	}
	started, err := serve.StartDetached(serve.DaemonOptions{
		Home:    home,
		Config:  rec.Config,
		LogPath: rec.Log,
		Args:    rec.Args,
	})
	if err != nil {
		return err
	}
	fmt.Printf("coddy serve %s restarted\n  pid     %d\n  config  %s\n  log     %s\n",
		started.Version, started.PID, started.Config, started.Log)
	return nil
}

// controlHome resolves which agent home a control verb is about. The verbs
// share nothing else with `coddy serve` itself, so they take only the flag that
// says which installation to talk to. It reports false when the caller asked for
// the usage text and there is nothing left to do.
func controlHome(args []string, verb string) (string, bool, error) {
	fs := flag.NewFlagSet("serve "+verb, flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	homeDir := fs.String("home", "", "agent state directory (CODDY_HOME, default ~/.coddy)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return "", false, nil
		}
		return "", false, err
	}
	paths, err := config.Resolve(config.CLIPaths{Home: strings.TrimSpace(*homeDir)})
	if err != nil {
		return "", false, err
	}
	return paths.Home, true, nil
}
