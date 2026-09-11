package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/EvilFreelancer/coddy-agent/external/gateway"
	"github.com/EvilFreelancer/coddy-agent/external/httpserver"
	"github.com/EvilFreelancer/coddy-agent/external/scheduler"
	"github.com/EvilFreelancer/coddy-agent/external/swarm"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/dryrun"
	"github.com/EvilFreelancer/coddy-agent/internal/logger"
	"github.com/EvilFreelancer/coddy-agent/internal/serve"
	"github.com/EvilFreelancer/coddy-agent/internal/version"
)

// httpTokenEnvVar is where the HTTP API looks for its bearer credential when no
// flag carries one, so a token need not be written into config.yaml.
const httpTokenEnvVar = "CODDY_HTTP_TOKEN"

// runServe implements `coddy serve`: one process running every subsystem the
// configuration enables.
//
// The surfaces used to be subcommands, which made each of them a process of its
// own. That is why running a Telegram bot next to the web UI meant two managers
// over one sessions directory and no way to watch a chat conversation from a
// browser. Here they are goroutines sharing one manager, and turning one on is
// a line of YAML.
func runServe(args []string) error {
	// The control verbs come before the flag set, because they are about a
	// daemon that is already running and share none of its options.
	if len(args) > 0 {
		switch args[0] {
		case "status":
			return runServeStatus(args[1:])
		case "stop":
			return runServeStop(args[1:])
		case "restart":
			return runServeRestart(args[1:])
		}
	}

	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	cfgPath := fs.String("config", "", "path to config.yaml (CODDY_CONFIG, else <home>/config.yaml or legacy search paths)")
	logLevel := fs.String("log-level", "", "log level: a bare level (debug|info|warn|error), or a comma-separated spec with per-component overrides such as info,gateway.telegram=debug (default from config)")
	logOutput := fs.String("log-output", "", "stdout|stderr|file|both (default from config)")
	logFile := fs.String("log-file", "", "log file path when output includes file (default from config)")
	logFormat := fs.String("log-format", "", "text|json (default from config)")
	homeDir := fs.String("home", "", "agent state directory (CODDY_HOME, default ~/.coddy)")
	serveCWD := fs.String("cwd", "", "default session cwd when a client omits it (CODDY_CWD, default process cwd)")
	sessionsRoot := fs.String("sessions-dir", "", "sessions root (empty uses config sessions.dir or ~/.coddy/sessions)")
	persistedSession := fs.String("session-id", "", "optional session id for new sessions (folder name)")

	host := fs.String("H", "", "bind address for the HTTP API (default httpserver.host, else 127.0.0.1)")
	port := fs.String("P", "", "listen port for the HTTP API (default httpserver.port, else 12345)")
	fs.StringVar(host, "host", "", "alias of -H")
	fs.StringVar(port, "port", "", "alias of -P")
	authToken := fs.String("auth-token", "", "bearer token required on /v1/* and /coddy/* (else "+httpTokenEnvVar+", else httpserver.auth_token). Empty = no auth")

	swarmHost := fs.String("swarm-host", "", "bind address for the swarm relay (default swarm.host, else 0.0.0.0)")
	swarmPort := fs.String("swarm-port", "", "listen port for the swarm relay (default swarm.port, else 12346)")
	swarmAuthToken := fs.String("swarm-auth-token", "", "bearer token clients must present to the relay (else "+swarm.TokenEnvVar+", else swarm.auth_token)")
	swarmPairing := fs.String("swarm-pairing-token", "", "credential nodes must present to register (else "+swarm.PairingEnvVar+", else swarm.pairing_tokens)")
	swarmInsecure := fs.Bool("swarm-allow-insecure", false, "permit binding the relay off loopback without a client token")

	httpOn := fs.Bool("http", true, "run the HTTP API in this process; overrides httpserver.enable")
	gatewayOn := fs.Bool("gateway", false, "run the messenger gateway; overrides gateways.*.enable")
	swarmOn := fs.Bool("swarm", false, "run the swarm relay; overrides swarm.enable")
	schedulerOn := fs.Bool("scheduler", false, "run the cron scheduler; overrides scheduler.enable")

	daemon := fs.Bool("daemon", false, "run in the background under a dispatcher that restarts the process if it dies (see `coddy serve status|stop|restart`)")
	fs.BoolVar(daemon, "d", false, "alias of --daemon")

	skillsAutoDiscovery := fs.Bool(config.SkillsAutoDiscoveryFlagName, true, "model-driven skill auto-discovery (load_skill tool); pass =false to disable and override config")
	projectTrust := fs.String(config.ProjectTrustFlagName, config.ProjectTrustAsk, config.ProjectTrustFlagUsage)
	testConfig := config.AddCheckFlag(fs)
	dryRun := dryrun.AddFlag(fs)

	fs.Usage = func() {
		_, _ = fmt.Fprintf(fs.Output(), "Usage of serve (runs every subsystem enabled in config.yaml):\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}

	cli := config.CLIPaths{
		Home:   strings.TrimSpace(*homeDir),
		CWD:    strings.TrimSpace(*serveCWD),
		Config: strings.TrimSpace(*cfgPath),
	}
	// A config check reports on the file and leaves: nothing is created under
	// the home and no subsystem starts.
	if *testConfig {
		return runConfigTest(cli)
	}

	// applyProcessOverrides re-applies everything the operator decided outside
	// config.yaml: the flags they typed and the credentials they kept in the
	// environment. It runs on the document this process loaded and again on
	// every document the watcher picks up off the disk, because a reload that
	// dropped `--gateway` or `-H 0.0.0.0` would take away, on somebody else's
	// unrelated save, a decision only the command line carries.
	//
	// Flags override the file only where the operator actually typed one, so a
	// default in the flag set never silently outvotes a decision in config.yaml.
	applyProcessOverrides := func(c *config.Config) error {
		applySubsystemFlags(fs, c, subsystemFlags{
			http: httpOn, gateway: gatewayOn, swarm: swarmOn, scheduler: schedulerOn,
		})
		if *swarmInsecure {
			c.Swarm.AllowInsecure = true
		}
		if t := firstNonEmpty(strings.TrimSpace(*swarmPairing), os.Getenv(swarm.PairingEnvVar)); t != "" {
			c.Swarm.PairingTokens = append(c.Swarm.PairingTokens, t)
		}
		config.ApplySkillsAutoDiscoveryFlag(fs, c, skillsAutoDiscovery)
		if err := config.ApplyProjectTrustFlag(fs, c, projectTrust); err != nil {
			return err
		}
		c.Swarm.Normalize()
		if err := c.Swarm.Validate(); err != nil {
			return err
		}
		if err := c.Scheduler.Validate(c); err != nil {
			return fmt.Errorf("scheduler: %w", err)
		}
		c.Logger.ApplyOverrides(config.LoggerCLIOverrides{
			Level:  strings.TrimSpace(*logLevel),
			Output: strings.TrimSpace(*logOutput),
			File:   strings.TrimSpace(*logFile),
			Format: strings.TrimSpace(*logFormat),
		})
		// Where the relay actually listens decides what it is willing to dial,
		// and the flag is part of that answer: a relay told to bind loopback on
		// the command line is a development relay whatever the file says.
		c.Swarm.Host = firstNonEmpty(strings.TrimSpace(*swarmHost), c.Swarm.EffectiveHost())
		return nil
	}
	// The listen addresses are read from a configuration rather than captured,
	// because the supervisor asks the same questions of a reloaded one: an
	// address that moved is the one change a running process cannot adopt, and
	// answering it needs the flags weighed against the new file exactly as they
	// were against the old.
	httpListenAddr := func(c *config.Config) string {
		return net.JoinHostPort(
			firstNonEmpty(strings.TrimSpace(*host), c.HTTPServer.DefaultListenHost()),
			firstNonEmpty(strings.TrimSpace(*port), c.HTTPServer.DefaultListenPortString()),
		)
	}
	swarmListenAddr := func(c *config.Config) string {
		return net.JoinHostPort(c.Swarm.Host, firstNonEmpty(strings.TrimSpace(*swarmPort), strconv.Itoa(c.Swarm.EffectivePort())))
	}
	// A dry run resolves the subsystems and their addresses as a start would,
	// probes them together with everything the file names, and leaves.
	if *dryRun {
		return runServeDryRun(cli, applyProcessOverrides, httpListenAddr, swarmListenAddr)
	}
	paths, err := config.Resolve(cli)
	if err != nil {
		return err
	}
	if err := serve.EnsureHomeLayout(paths.Home); err != nil {
		return err
	}
	cfg, err := config.LoadFromCLI(cli)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if err := applyProcessOverrides(cfg); err != nil {
		return err
	}

	log, logCloser, err := logger.New(cfg.Logger)
	if err != nil {
		return fmt.Errorf("log: %w", err)
	}
	defer func() { _ = logCloser.Close() }()

	httpAddr := httpListenAddr(cfg)
	swarmAddr := swarmListenAddr(cfg)

	httpTokens := outOfBandTokens(*authToken, httpTokenEnvVar)

	rt := &serve.Runtime{}
	all := subsystems(rt, subsystemDeps{
		httpAddr:        httpAddr,
		swarmAddr:       swarmAddr,
		httpListenAddr:  httpListenAddr,
		swarmListenAddr: swarmListenAddr,
		home:            paths.Home,
		httpAuthTokens:  httpTokens,
		swarmAuthTokens: outOfBandTokens(*swarmAuthToken, swarm.TokenEnvVar),
	})
	// Resolve is the pre-flight: it refuses a configuration this binary cannot
	// honour before a single listener is opened, and reports what will run.
	enabled, err := serve.Resolve(cfg, all)
	if err != nil {
		return err
	}

	// Resolve doubles as the pre-flight for the background forms below: a
	// configuration this binary cannot honour is refused in the terminal that
	// typed the command, not in a log file nobody is tailing yet.
	daemonOpts := serve.DaemonOptions{
		Home:   paths.Home,
		Config: paths.ConfigPath,
		Args:   typedServeFlags(fs),
		Log:    log,
	}
	switch {
	case serve.Role() == serve.RoleDispatcher:
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return serve.RunDispatcher(ctx, daemonOpts)
	case *daemon:
		rec, err := serve.StartDetached(daemonOpts)
		if err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "coddy serve %s is running in the background\n  pid     %d\n  config  %s\n  log     %s\n",
			rec.Version, rec.PID, rec.Config, rec.Log)
		if !rec.Serving() {
			// The dispatcher is up and will keep trying, which is the whole
			// point of it - but nothing is being served yet, and saying only
			// "running" would send the operator looking in the wrong place.
			fmt.Fprintf(os.Stderr, "warning: no surface is up yet: %s\n  the dispatcher keeps retrying; `coddy serve status` reports when one comes up\n", rec.LastError)
		}
		return nil
	}

	if err := rt.Init(serve.Options{
		CLI:                cli,
		SessionsRoot:       strings.TrimSpace(*sessionsRoot),
		PreferredSessionID: strings.TrimSpace(*persistedSession),
		NeedsSessions:      needsSessions(enabled),
		Log:                log,
		Cfg:                cfg,
	}); err != nil {
		return err
	}

	log.Info("starting coddy serve", "version", version.Get(), "config", paths.ConfigPath, "workspace", paths.CWD)
	printServeBanner(cfg, enabled, httpAddr, swarmAddr, len(httpTokens) > 0)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	reloads := make(chan *config.Config, 1)
	if rt.Mgr != nil {
		// Every reload path - the settings screen, the agent's config_commit
		// tool, the console - lands in the manager, so watching it is how a
		// remote edit reaches a subsystem that has to be rebuilt to honour it.
		//
		// Only the newest configuration is worth keeping: an observer must not
		// block the goroutine that replaced it, and a queued older document is
		// already wrong by the time the supervisor would read it.
		removeObserver := rt.Mgr.AddConfigObserver(func(next *config.Config) {
			for {
				select {
				case reloads <- next:
					return
				default:
				}
				select {
				case <-reloads:
				default:
					return
				}
			}
		})
		defer removeObserver()

		// Not every writer of config.yaml is this process. `coddy providers
		// login` adds a provider and its models from another terminal, an
		// operator edits the file by hand, a deployment drops a new one in.
		// Watching the file turns all of those into the same swap the settings
		// screen makes, so an open model picker, a chat surface and the
		// supervisor see them together.
		watcher := &config.FileWatcher{
			Paths:   paths,
			Live:    rt.Mgr.Cfg,
			Install: rt.Mgr.ReplaceConfig,
			Adjust:  applyProcessOverrides,
			Log:     log,
		}
		go func() { _ = watcher.Run(ctx) }()
	}

	// The supervisor is handed every descriptor, not just the enabled ones, so
	// a later configuration change can turn a surface on as well as off.
	sup := serve.NewSupervisor(log, all)
	// Only a process with something waiting to start it again may end itself
	// over a configuration change. In the foreground the operator is the only
	// one who would bring it back, so they are told instead.
	sup.Restartable = serve.Supervised()
	err = sup.Run(ctx, cfg, reloads)
	if errors.Is(err, serve.ErrRestartRequested) {
		log.Info("exiting so the dispatcher can start a process with the new listen settings")
		return serve.ExitCodeError{Code: serve.ExitRestart, Err: err}
	}
	return err
}

// typedServeFlags rebuilds the command line for the processes this one starts:
// every flag the operator actually typed, minus the switch that asked for the
// background, which only makes sense once.
//
// It is rebuilt from the parsed flag set rather than filtered out of the raw
// arguments, because the raw form is ambiguous - `-home -d` puts "-d" in the
// value position - and because a canonical `-name=value` is what a record has to
// keep for `coddy serve restart` to bring back the same daemon a week later.
func typedServeFlags(fs *flag.FlagSet) []string {
	var out []string
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "d" || f.Name == "daemon" {
			return
		}
		out = append(out, "-"+f.Name+"="+f.Value.String())
	})
	return out
}

// subsystemFlags collects the explicit on/off switches.
type subsystemFlags struct {
	http, gateway, swarm, scheduler *bool
}

// applySubsystemFlags copies only the switches the operator actually typed.
func applySubsystemFlags(fs *flag.FlagSet, cfg *config.Config, f subsystemFlags) {
	fs.Visit(func(fl *flag.Flag) {
		switch fl.Name {
		case "http":
			v := *f.http
			cfg.HTTPServer.Enabled = &v
		case "gateway":
			cfg.Gateways.Telegram.Enabled = *f.gateway
		case "swarm":
			cfg.Swarm.Enabled = *f.swarm
		case "scheduler":
			cfg.Scheduler.Enabled = *f.scheduler
		}
	})
}

// subsystemDeps are the already-resolved values the descriptors close over.
type subsystemDeps struct {
	httpAddr  string
	swarmAddr string
	// httpListenAddr and swarmListenAddr answer where a surface would bind under
	// some other configuration, which is what tells a reload that moved an
	// address from one that left it alone.
	httpListenAddr  func(*config.Config) string
	swarmListenAddr func(*config.Config) string
	home            string
	httpAuthTokens  []string
	swarmAuthTokens []string
}

// subsystems describes every surface this binary knows about, available or not.
//
// A descriptor exists even when its build tag is missing, so a configuration
// that asks for a surface this binary cannot run is refused by name instead of
// starting a daemon that quietly does less than it was told to.
func subsystems(rt *serve.Runtime, deps subsystemDeps) []serve.Subsystem {
	return []serve.Subsystem{
		{
			Kind:      serve.KindHTTP,
			ConfigKey: "httpserver.enable",
			BuildTag:  "http",
			Available: httpserver.Available,
			Enabled:   func(c *config.Config) bool { return c.HTTPServer.IsEnabled() },
			// No Fingerprint: the listener is what the caller is talking
			// through, so it is not rebuilt underneath them. Moving it takes a
			// fresh process, which under a dispatcher is exactly what happens -
			// that is how an operator changes the port of the very server whose
			// settings screen they are typing into.
			RestartKey: deps.httpListenAddr,
			Run: func(ctx context.Context) error {
				return httpserver.Serve(ctx, httpserver.Options{
					Cfg: rt.Cfg(), Mgr: rt.Mgr, Log: rt.Log,
					DefaultCWD: rt.Paths.CWD, Home: deps.home,
					ListenAddr: deps.httpAddr, ExtraAuthTokens: deps.httpAuthTokens,
					OnServer: func(s *httpserver.Server) {
						if s == nil {
							rt.SetTurnMirror(nil)
							return
						}
						rt.SetTurnMirror(s)
					},
				})
			},
		},
		{
			Kind:      serve.KindGateway,
			ConfigKey: "gateways.telegram.enable",
			BuildTag:  "gateway",
			Available: gateway.Available,
			Enabled:   func(c *config.Config) bool { return c.Gateways.Telegram.Enabled },
			// A bot is a client of somebody else's server, so it can be rebuilt
			// in place: that is how a token rotated from the settings screen
			// takes effect without anyone reaching the machine.
			Fingerprint: gatewayFingerprint,
			Run: func(ctx context.Context) error {
				return gateway.Serve(ctx, gateway.Options{
					Cfg: rt.Cfg(), Mgr: rt.Mgr, Log: rt.Log,
					DefaultCWD: rt.Paths.CWD, Mirror: rt,
				})
			},
		},
		{
			Kind:       serve.KindSwarm,
			ConfigKey:  "swarm.enable",
			BuildTag:   "swarm",
			Available:  swarm.Available,
			Enabled:    func(c *config.Config) bool { return c.Swarm.Enabled },
			RestartKey: deps.swarmListenAddr,
			Run: func(ctx context.Context) error {
				return swarm.Serve(ctx, swarm.Options{
					Cfg: rt.Cfg(), Log: rt.Log, Home: deps.home,
					ListenAddr: deps.swarmAddr, ExtraAuthTokens: deps.swarmAuthTokens,
				})
			},
		},
		{
			Kind:      serve.KindScheduler,
			ConfigKey: "scheduler.enable",
			BuildTag:  "scheduler",
			Available: scheduler.Available,
			Enabled:   func(c *config.Config) bool { return c.SchedulerEffectiveEnabled() },
			// The daemon reads its jobs from disk, so a change to where it
			// looks or how long a run may take needs a fresh one.
			Fingerprint: schedulerFingerprint,
			Run: func(ctx context.Context) error {
				return scheduler.Serve(ctx, scheduler.Options{
					Cfg: rt.Cfg(), Log: rt.Log, ProcessCWD: rt.Paths.CWD,
				})
			},
		},
	}
}

// gatewayFingerprint is everything a rebuilt bot would read differently.
func gatewayFingerprint(c *config.Config) string {
	if c == nil {
		return ""
	}
	tg := c.Gateways.Telegram
	return strings.Join([]string{
		strconv.FormatBool(tg.Enabled),
		tg.EffectiveToken(),
		tg.Proxy,
		strconv.FormatBool(tg.RichMessages),
		string(tg.DefaultAccess),
		string(tg.DefaultIsolation),
		fmt.Sprint(tg.Admins),
		fmt.Sprint(tg.UserGroups),
		fmt.Sprint(tg.Chats),
	}, "\x00")
}

// schedulerFingerprint is everything a rebuilt daemon would read differently.
func schedulerFingerprint(c *config.Config) string {
	if c == nil {
		return ""
	}
	s := c.Scheduler
	return strings.Join([]string{
		strconv.FormatBool(s.Enabled), s.Dir, s.Timeout,
		strconv.Itoa(s.MaxQueue), strconv.Itoa(s.RetainSessions),
	}, "\x00")
}

// needsSessions reports whether any enabled surface runs agent turns. A process
// that only relays a swarm opens no session store and builds no manager.
func needsSessions(enabled []serve.Subsystem) bool {
	for _, sub := range enabled {
		if sub.Kind != serve.KindSwarm {
			return true
		}
	}
	return false
}

// outOfBandTokens collects a credential from a flag and then the environment,
// so it never has to be written into config.yaml.
func outOfBandTokens(flagValue, envVar string) []string {
	var out []string
	if t := strings.TrimSpace(flagValue); t != "" {
		out = append(out, t)
	}
	if t := strings.TrimSpace(os.Getenv(envVar)); t != "" {
		out = append(out, t)
	}
	return out
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

// printServeBanner says on stderr what this process actually started.
//
// One command that starts a different set of surfaces per config file is only
// as clear as what it reports, so the reachable addresses and whether the API
// is open are stated rather than left to be inferred from the logs.
func printServeBanner(cfg *config.Config, enabled []serve.Subsystem, httpAddr, swarmAddr string, extraAuth bool) {
	fmt.Fprintf(os.Stderr, "coddy serve %s\n", version.Get())
	for _, sub := range enabled {
		switch sub.Kind {
		case serve.KindHTTP:
			auth := "no auth"
			if extraAuth || len(cfg.HTTPServer.EffectiveAuthTokens()) > 0 {
				auth = "bearer auth"
			}
			fmt.Fprintf(os.Stderr, "  httpserver  http://%s  (%s)\n", httpAddr, auth)
		case serve.KindSwarm:
			scheme := "http"
			if cfg.Swarm.TLS.Enabled() {
				scheme = "https"
			}
			fmt.Fprintf(os.Stderr, "  swarm       %s://%s  (relay)\n", scheme, swarmAddr)
		case serve.KindGateway:
			fmt.Fprintf(os.Stderr, "  gateway     telegram\n")
		case serve.KindScheduler:
			fmt.Fprintf(os.Stderr, "  scheduler   %s\n", cfg.Scheduler.Dir)
		}
	}
}
