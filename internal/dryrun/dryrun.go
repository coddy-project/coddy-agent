// Package dryrun probes the world a config.yaml describes - the paths it
// names, the servers it points at, the credentials it carries, the addresses
// it would bind - without starting anything, and reports each finding next to
// the place in the file it comes from. It is the --dry-run flag of the
// console, coddy acp and coddy serve; the static half of that flag, the file
// against the schema and the loader's rules, is config.Check and runs first.
package dryrun

import (
	"context"
	"errors"
	"flag"
	"io"
	"sync"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/remote"
)

// The flag every entrypoint offers.
const (
	FlagName  = "dry-run"
	FlagUsage = "run the --test-config check, then probe what the file points at - paths, model servers and their credentials, listen addresses, MCP commands, the Telegram token - and exit without starting anything; exit status 1 when a probe fails"
)

// AddFlag registers --dry-run on fs.
func AddFlag(fs *flag.FlagSet) *bool {
	return fs.Bool(FlagName, false, FlagUsage)
}

// Surface names the command running the dry run. The console and acp skip
// what only coddy serve starts: the swarm joins and upstreams.
type Surface string

const (
	SurfaceConsole Surface = "console"
	SurfaceACP     Surface = "acp"
	SurfaceServe   Surface = "serve"
)

// Listener is an address a command would bind.
type Listener struct {
	// Path is the config section that owns the address ("httpserver", "swarm").
	Path string
	Addr string
}

// Request is what one dry run probes.
type Request struct {
	Cfg     *config.Config
	Paths   config.Paths
	Locator *config.Locator
	Surface Surface
	// Listeners are the addresses the command would bind; serve fills them
	// from the subsystems the configuration and the flags enable.
	Listeners []Listener
	// SubsystemErr is what serve.Resolve refused (a surface enabled but not
	// built into this binary); it is reported as an error check.
	SubsystemErr error
	// Remote is the --remote target of a console or acp run.
	Remote *remote.Options
	// Timeout bounds each network probe; zero means defaultTimeout.
	Timeout time.Duration
}

const (
	defaultTimeout = 10 * time.Second
	// networkParallelism bounds concurrent probes so a config with many
	// providers does not open every connection at once.
	networkParallelism = 8
)

// Prepared is a configuration that passed the static check, loaded without
// side effects, together with the means to locate findings in its file.
type Prepared struct {
	Cfg     *config.Config
	Paths   config.Paths
	Locator *config.Locator
}

// Prepare runs the static check on the config file the flags select and, when
// it passes, loads the file read-only. A file with static errors returns a nil
// Prepared and the report that says why: probing what a broken file names
// would only bury the first mistake under its consequences.
func Prepare(cli config.CLIPaths) (*Prepared, *config.CheckReport, error) {
	rep, err := config.Check(cli)
	if err != nil {
		return nil, nil, err
	}
	if !rep.Valid() {
		return nil, rep, nil
	}
	cfg, raw, err := config.LoadReadOnly(cli)
	if err != nil {
		return nil, rep, err
	}
	return &Prepared{Cfg: cfg, Paths: cfg.Paths, Locator: config.NewLocator(raw)}, rep, nil
}

// probe is one network check; it returns its checks in report order.
type probe func(ctx context.Context) []Check

// runner carries one run's request and collects its report.
type runner struct {
	req Request
	rep *Report
}

// Run probes everything the request describes. Local checks (paths, commands,
// listen addresses) run first and in order; network probes run concurrently
// and land in a fixed order after them.
func Run(ctx context.Context, req Request) *Report {
	rep := &Report{File: req.Paths.ConfigPath}
	if req.Cfg == nil {
		rep.add(Check{Status: StatusError, Path: "config", Message: "no configuration to probe"})
		return rep
	}
	if req.Timeout <= 0 {
		req.Timeout = defaultTimeout
	}
	r := &runner{req: req, rep: rep}
	r.subsystems()
	r.paths()
	r.mcpCommands()
	r.listeners()

	var probes []probe
	probes = append(probes, r.providerProbes()...)
	probes = append(probes, r.telegramProbes()...)
	probes = append(probes, r.mcpRemoteProbes()...)
	probes = append(probes, r.remoteProbes()...)
	probes = append(probes, r.swarmProbes()...)
	for _, checks := range runConcurrently(ctx, probes) {
		for _, c := range checks {
			rep.add(c)
		}
	}
	return rep
}

// runConcurrently runs the probes with bounded parallelism and returns their
// results in the probes' order.
func runConcurrently(ctx context.Context, probes []probe) [][]Check {
	results := make([][]Check, len(probes))
	sem := make(chan struct{}, networkParallelism)
	var wg sync.WaitGroup
	for i, p := range probes {
		wg.Add(1)
		go func(i int, p probe) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			results[i] = p(ctx)
		}(i, p)
	}
	wg.Wait()
	return results
}

// check builds a Check located at locPath in the config file (path itself
// when locPath is empty).
func (r *runner) check(status Status, path, locPath, message, fix string) Check {
	c := Check{Status: status, Path: path, Message: message, Fix: fix}
	if locPath == "" {
		locPath = path
	}
	if line, col, ok := r.req.Locator.Locate(locPath); ok {
		c.Line, c.Column = line, col
	}
	return c
}

// explicit reports whether a path is written in the file, as opposed to an
// applied default.
func (r *runner) explicit(path string) bool {
	_, _, ok := r.req.Locator.Locate(path)
	return ok
}

// serveOnly reports whether the run is for coddy serve (or for everything,
// when no surface is named).
func (r *runner) serveOnly() bool {
	return r.req.Surface == "" || r.req.Surface == SurfaceServe
}

// subsystems reports what serve.Resolve refused.
func (r *runner) subsystems() {
	if r.req.SubsystemErr == nil {
		return
	}
	r.rep.add(Check{Status: StatusError, Path: "serve", Message: r.req.SubsystemErr.Error(),
		Fix: "enable only the subsystems this binary was built with, or rebuild with the tag the message names"})
}

// RunAndReport is the driver every entrypoint shares: the static check first,
// then the probes, with the report printed to w and a non-nil error when the
// dry run failed, so the process exits non-zero. customize applies the flags
// the command would apply to a loaded configuration (--scheduler, --gateway,
// ...); build shapes the request from the prepared configuration.
func RunAndReport(w io.Writer, cli config.CLIPaths, customize func(*config.Config) error, build func(*Prepared) (Request, error)) error {
	prep, rep, err := Prepare(cli)
	if err != nil {
		return err
	}
	rep.Write(w)
	if prep == nil {
		return errors.New("dry run failed: fix the config file first")
	}
	if customize != nil {
		if err := customize(prep.Cfg); err != nil {
			return err
		}
	}
	req := Request{}
	if build != nil {
		req, err = build(prep)
		if err != nil {
			return err
		}
	}
	req.Cfg, req.Paths, req.Locator = prep.Cfg, prep.Paths, prep.Locator
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	report := Run(ctx, req)
	report.Write(w)
	if report.Errors() > 0 {
		return errors.New("dry run failed")
	}
	return nil
}

// RunConsole is RunAndReport for the console and acp: no listeners, and the
// --remote target probed when one is given.
func RunConsole(w io.Writer, surface Surface, cli config.CLIPaths, remoteArg, remoteToken string, customize func(*config.Config) error) error {
	return RunAndReport(w, cli, customize, func(prep *Prepared) (Request, error) {
		ropts, err := remote.Resolve(prep.Cfg, remoteArg, remoteToken)
		if err != nil {
			return Request{}, err
		}
		return Request{Surface: surface, Remote: ropts}, nil
	})
}
