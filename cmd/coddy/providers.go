package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

// neuralDeepLogoutTimeout bounds the best-effort server-side key revoke.
const neuralDeepLogoutTimeout = 15 * time.Second

// runProviders implements `coddy providers list|login|logout`: one place to
// see every configured LLM backend with its active credential source, and to
// sign in to providers that support browser login (neuraldeep, codex) without
// pasting keys by hand. Credentials land in the same files the HTTP surface
// and the SPA use ($CODDY_HOME/providers/<name>/*-auth.json).
func runProviders(args []string) error {
	if len(args) == 0 {
		return providersUsageErr()
	}
	sub := args[0]
	rest := args[1:]
	var name string
	if sub == "login" || sub == "logout" {
		if len(rest) == 0 || strings.HasPrefix(rest[0], "-") {
			return providersUsageErr()
		}
		name, rest = rest[0], rest[1:]
	} else if sub != "list" {
		return providersUsageErr()
	}

	fs := flag.NewFlagSet("providers", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	home := fs.String("home", "", "override CODDY_HOME")
	device := fs.Bool("device", false, "neuraldeep: the device flow, which is the default (accepted for compatibility)")
	browser := fs.Bool("browser", false, "neuraldeep: sign in through a loopback browser callback instead of the device flow")
	noConfig := fs.Bool("no-config", false, "login: do not add the provider and its models to config.yaml after login")
	apiBase := fs.String("api-base", "", "neuraldeep: API endpoint to sign in against, one of "+strings.Join(llm.NeuralDeepAPIBases(), ", ")+" (default: the provider's api_base, else the first)")
	if err := fs.Parse(rest); err != nil {
		return err
	}

	cfg, err := config.LoadFromCLI(config.CLIPaths{Home: *home})
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	switch sub {
	case "list":
		for _, line := range providersListLines(cfg) {
			fmt.Println(line)
		}
		return nil
	case "login":
		return providersLogin(cfg, name, *device, *browser, *noConfig, *apiBase)
	case "logout":
		return providersLogout(cfg, name)
	default:
		return providersUsageErr()
	}
}

func providersUsageErr() error {
	return fmt.Errorf("usage: %s providers list | login <name> [--browser] [--no-config] [--api-base URL] | logout <name> [--home DIR]", os.Args[0])
}

// resolveLoginProvider picks the provider entry for login/logout. A name
// present in config.yaml wins; otherwise the conventional names "neuraldeep"
// and "codex" synthesize a probe entry of that type, so a fresh install can
// sign in before editing config.yaml.
func resolveLoginProvider(cfg *config.Config, name string) (*config.ProviderConfig, error) {
	if prov := cfg.FindProvider(name); prov != nil {
		return prov, nil
	}
	if name == "neuraldeep" || name == "codex" {
		probe := config.ProviderConfig{Name: name, Type: name}
		probe.Normalize()
		if err := probe.Validate(); err != nil {
			return nil, err
		}
		return &probe, nil
	}
	return nil, fmt.Errorf("provider %q is not in config.yaml; add it first or use the conventional names \"neuraldeep\" / \"codex\"", name)
}

func providersLogin(cfg *config.Config, name string, device, browser, noConfig bool, apiBase string) error {
	if device && browser {
		return errors.New("providers login: --device and --browser ask for different flows; pass at most one")
	}
	prov, err := resolveLoginProvider(cfg, name)
	if err != nil {
		return err
	}
	switch prov.Type {
	case "codex":
		authPath := config.CodexAuthPath(cfg.Paths.Home, prov.Name)
		if authPath == "" {
			return fmt.Errorf("providers: could not resolve the credential path for provider %q", prov.Name)
		}
		return codexLogin(cfg, prov, prov.Name, authPath, noConfig)
	case "neuraldeep":
		return neuralDeepLogin(cfg, prov, browser, noConfig, apiBase)
	default:
		return fmt.Errorf("provider %q has type %q: it authenticates with api_key (or the %s env var), browser sign-in exists only for neuraldeep and codex",
			prov.Name, prov.Type, config.ProviderAPIKeyEnvVarName(prov.Name))
	}
}

// resolveNeuralDeepAPIBase settles which NeuralDeep deployment a login talks
// to: --api-base wins, then the provider row, then the default (reported as an
// empty string when the row is silent). An unknown flag value is an error
// rather than a silent fallback - the user would otherwise sign in against the
// wrong hub and only find out when requests start failing. A row that names
// no NeuralDeep endpoint resolves to the default explicitly, so the login
// records it and repairs the row instead of leaving the unusable value behind.
func resolveNeuralDeepAPIBase(flagValue, configured string) (string, error) {
	if strings.TrimSpace(flagValue) != "" {
		base, ok := llm.NormalizeNeuralDeepAPIBase(flagValue)
		if !ok {
			return "", fmt.Errorf("--api-base %q is not a NeuralDeep endpoint; use one of %s",
				strings.TrimSpace(flagValue), strings.Join(llm.NeuralDeepAPIBases(), ", "))
		}
		return base, nil
	}
	if base, ok := llm.NormalizeNeuralDeepAPIBase(configured); ok {
		return base, nil
	}
	if strings.TrimSpace(configured) != "" {
		return llm.NeuralDeepDefaultAPIBase(), nil
	}
	return "", nil
}

// neuralDeepLogin signs in to the hub. The device flow is the default: the
// loopback callback only completes when a browser can reach this process's own
// 127.0.0.1, which is false for every machine reached over SSH, in a container,
// or in CI - and that failure is silent, a browser page that never comes back.
// The device flow costs one confirmation click and works from any browser, so
// --browser is the opt-in rather than the other way round (Codex sign-in has
// been device-only from the start for the same reason).
func neuralDeepLogin(cfg *config.Config, prov *config.ProviderConfig, browser, noConfig bool, apiBase string) error {
	// The endpoint decides which hub mints the key, so it has to be settled
	// before the flow starts: --api-base wins, then the provider row, then the
	// default deployment. A fresh install outside Russia has no row yet.
	endpoint, err := resolveNeuralDeepAPIBase(apiBase, prov.APIBase)
	if err != nil {
		return err
	}
	authPath := config.NeuralDeepAuthPath(cfg.Paths.Home, prov.Name)
	if authPath == "" {
		return fmt.Errorf("providers: could not resolve the credential path for provider %q", prov.Name)
	}
	client, err := llm.HTTPClientForOptionalProxy(prov.Proxy)
	if err != nil {
		return err
	}
	// Ctrl-C must abandon the wait without leaving a half-written credential.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	hub := llm.NeuralDeepHubFor(endpoint)
	var key string
	if browser {
		key, err = llm.NeuralDeepSignIn(ctx, hub, client, authPath, func(p llm.NeuralDeepLoginPrompt) {
			fmt.Printf("Open %s\n", p.AuthURL)
			fmt.Println("The page comes back to this machine, so open it in a browser running here.")
			fmt.Println("Waiting for the browser sign-in to finish...")
			// Best-effort: the printed URL is the contract, a browser is a bonus.
			_ = openBrowserFn(p.AuthURL)
		})
	} else {
		// No terminal check here on purpose: `ssh host 'coddy providers login
		// neuraldeep'` has no pty, and its output still reaches the person who
		// typed it. Refusing there would close the door on the very setup this
		// flow exists for; a login nobody answers ends on the hub's own
		// fifteen-minute deadline, and Ctrl-C ends it sooner.
		key, err = llm.NeuralDeepDeviceSignIn(ctx, hub, client, authPath, neuralDeepDeviceLabel(), func(l llm.NeuralDeepDeviceLogin) {
			fmt.Printf("Open %s\n", l.VerificationTarget())
			fmt.Printf("and confirm the code %s\n", l.UserCode)
			fmt.Println("Any browser will do, including one on your phone; the page asks you to sign in first.")
			fmt.Println("Only continue in the browser if you started this login yourself.")
			fmt.Println("Waiting for confirmation...")
			// Opening a browser here is a convenience for a desktop; on a
			// machine reached over SSH there is nothing to open, and the
			// printed URL is what the user carries to their own browser.
			if localBrowserAvailableFn() {
				_ = openBrowserFn(l.VerificationTarget())
			}
		})
	}
	if err != nil {
		return fmt.Errorf("neuraldeep login: %w", err)
	}
	fmt.Printf("Signed in. Credential stored at %s\n", authPath)

	if who, whoErr := llm.FetchNeuralDeepWhoami(ctx, hub, key, client); whoErr == nil {
		display := who.Name
		if display == "" {
			display = who.Email
		}
		fmt.Printf("Logged in as %s (tier %s)\n", display, who.Tier)
	}

	if noConfig {
		return nil
	}
	if err := neuralDeepWriteConfig(ctx, cfg, prov.Name, hub, endpoint, key, client); err != nil {
		// The login itself succeeded; a config write problem must not read as
		// a failed sign-in.
		fmt.Fprintf(os.Stderr, "note: could not update config.yaml: %v\n", err)
	}
	return nil
}

// neuralDeepWriteConfig reports what ApplyNeuralDeepLoginToConfig added.
func neuralDeepWriteConfig(ctx context.Context, cfg *config.Config, name, hub, apiBase, key string, client *http.Client) error {
	added, err := llm.ApplyNeuralDeepLoginToConfig(ctx, cfg, name, hub, apiBase, key, client)
	if err != nil {
		return err
	}
	if len(added) == 0 {
		fmt.Println("config.yaml already lists this provider and its models.")
		return nil
	}
	fmt.Printf("Updated %s: %s\n", cfg.Paths.ConfigPath, strings.Join(added, ", "))
	fmt.Println("A running `coddy http` server keeps its loaded config; restart it (or edit settings in the UI) to pick the changes up.")
	return nil
}

func providersLogout(cfg *config.Config, name string) error {
	prov, err := resolveLoginProvider(cfg, name)
	if err != nil {
		return err
	}
	switch prov.Type {
	case "codex":
		authPath := config.CodexAuthPath(cfg.Paths.Home, prov.Name)
		if err := llm.RemoveCodexAuth(authPath); err != nil {
			return fmt.Errorf("providers logout: %w", err)
		}
		fmt.Printf("Removed the Coddy-managed Codex credential for provider %q.\n", prov.Name)
		return nil
	case "neuraldeep":
		authPath := config.NeuralDeepAuthPath(cfg.Paths.Home, prov.Name)
		key, loadErr := llm.LoadNeuralDeepKey(authPath)
		if loadErr == nil && key != "" {
			ctx, cancel := context.WithTimeout(context.Background(), neuralDeepLogoutTimeout)
			defer cancel()
			client, _ := llm.HTTPClientForOptionalProxy(prov.Proxy)
			st, _ := llm.InspectNeuralDeepAuth(authPath)
			hub := st.Hub
			if hub == "" {
				hub = llm.NeuralDeepHubFor(prov.APIBase)
			}
			if revokeErr := llm.RevokeNeuralDeepKey(ctx, hub, key, client); revokeErr != nil {
				fmt.Fprintf(os.Stderr, "note: could not revoke the key on the hub (%v); revoke it in the dashboard: %s/app\n", revokeErr, hub)
			} else {
				fmt.Println("Revoked the key on the NeuralDeep hub.")
			}
		}
		if err := llm.RemoveNeuralDeepAuth(authPath); err != nil {
			return fmt.Errorf("providers logout: %w", err)
		}
		fmt.Printf("Removed the NeuralDeep credential for provider %q.\n", prov.Name)
		return nil
	default:
		return fmt.Errorf("provider %q has type %q: nothing to log out of (api_key lives in config.yaml)", prov.Name, prov.Type)
	}
}

// providersListLines renders one line per configured provider with the
// credential source requests will actually use. Keys are never printed raw.
func providersListLines(cfg *config.Config) []string {
	if len(cfg.Providers) == 0 {
		return []string{"no providers configured; run `" + os.Args[0] + " providers login neuraldeep` or edit config.yaml"}
	}
	lines := make([]string, 0, len(cfg.Providers))
	for i := range cfg.Providers {
		prov := &cfg.Providers[i]
		lines = append(lines, "  "+prov.Name+" ("+prov.Type+"): "+providerCredentialSummary(cfg, prov)+neuralDeepEndpointNote(prov))
	}
	return lines
}

// neuralDeepEndpointNote names the deployment when a row is not on the default
// one, so `providers list` shows which mirror it talks to. Rows on the default
// endpoint read exactly as they did before.
func neuralDeepEndpointNote(prov *config.ProviderConfig) string {
	if prov.Type != "neuraldeep" {
		return ""
	}
	base, ok := llm.NormalizeNeuralDeepAPIBase(prov.APIBase)
	if !ok || base == llm.NeuralDeepDefaultAPIBase() {
		return ""
	}
	return " [" + base + "]"
}

func providerCredentialSummary(cfg *config.Config, prov *config.ProviderConfig) string {
	switch prov.Type {
	case "codex":
		st, err := llm.InspectCodexAuth(config.CodexAuthPath(cfg.Paths.Home, prov.Name))
		if err != nil || !st.Connected {
			return "not connected; run `" + os.Args[0] + " providers login " + prov.Name + "`"
		}
		// This line is the only credential report for codex rows, so it names
		// the file the token actually comes from and the account behind it.
		source := "Coddy-managed credential"
		if st.Source == "codex_cli" {
			source = "Codex CLI login " + llm.CodexCLIAuthPath()
		}
		account := st.AccountID
		if account == "" {
			account = "unknown"
		}
		return "connected via ChatGPT (" + source + "), account " + account
	case "neuraldeep":
		st, err := llm.InspectNeuralDeepAuth(config.NeuralDeepAuthPath(cfg.Paths.Home, prov.Name))
		explicit := explicitKeySource(prov)
		switch {
		case err != nil:
			return "login unreadable: " + err.Error()
		case explicit != "" && st.Connected:
			return explicit + " overrides the NeuralDeep login (" + st.Masked + "); remove it to use the login"
		case explicit != "":
			return explicit
		case st.Connected:
			return "signed in to NeuralDeep (" + st.Masked + ")"
		default:
			return "no credentials; run `" + os.Args[0] + " providers login " + prov.Name + "`"
		}
	default:
		if src := explicitKeySource(prov); src != "" {
			return src
		}
		return "no credentials; set api_key or " + config.ProviderAPIKeyEnvVarName(prov.Name)
	}
}

// explicitKeySource names the non-OAuth credential source, if any.
func explicitKeySource(prov *config.ProviderConfig) string {
	if strings.TrimSpace(prov.APIKey) != "" {
		return "api_key from config.yaml"
	}
	if strings.TrimSpace(prov.APIKeyCommand) != "" {
		return "api_key_command"
	}
	env := config.ProviderAPIKeyEnvVarName(prov.Name)
	if strings.TrimSpace(os.Getenv(env)) != "" {
		return env + " env var"
	}
	return ""
}

func neuralDeepDeviceLabel() string {
	host, err := os.Hostname()
	if err != nil || strings.TrimSpace(host) == "" {
		return "coddy"
	}
	return "coddy @ " + host
}

// openBrowserFn opens a URL in the user's default browser, best-effort. It is
// a variable so tests (and callers that must not spawn processes) can replace
// it; the printed URL always remains the source of truth.
var openBrowserFn = openBrowser

func openBrowser(url string) error {
	argv := platform.BrowserOpenArgv(url)
	if len(argv) == 0 {
		return errors.New("no browser opener found in PATH")
	}
	cmd := exec.Command(argv[0], argv[1:]...) //nolint:gosec // argv comes from a fixed list of openers
	if err := cmd.Start(); err != nil {
		return err
	}
	// A sign-in waits for up to fifteen minutes; an unreaped opener would sit
	// as a zombie for all of it.
	go func() { _ = cmd.Wait() }()
	return nil
}

// localBrowserAvailableFn is a variable so tests can decide what the machine
// looks like without touching the environment.
var localBrowserAvailableFn = platform.LocalBrowserAvailable
