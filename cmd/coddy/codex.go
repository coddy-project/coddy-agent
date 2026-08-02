package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// runCodex implements `coddy codex login|status|logout`: the terminal
// counterpart of Settings -> LLM Providers -> Sign In with ChatGPT, for ACP and
// headless setups that never open the web UI. Credentials land in the same
// place the HTTP surface uses ($CODDY_HOME/providers/<name>/codex-auth.json).
func runCodex(args []string) error {
	if len(args) == 0 {
		return codexUsageErr()
	}
	fs := flag.NewFlagSet("codex", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	provider := fs.String("provider", "", "codex provider name from config.yaml (default: the first codex provider, else \"codex\")")
	home := fs.String("home", "", "override CODDY_HOME")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}

	cfg, err := config.LoadFromCLI(config.CLIPaths{Home: *home})
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	name, prov, err := resolveCodexProvider(cfg, *provider)
	if err != nil {
		return err
	}
	authPath := config.CodexAuthPath(cfg.Paths.Home, name)
	if authPath == "" {
		return fmt.Errorf("codex: could not resolve the credential path for provider %q", name)
	}

	switch args[0] {
	case "login":
		return codexLogin(prov, name, authPath)
	case "status":
		return codexStatus(name, authPath)
	case "logout":
		if err := llm.RemoveCodexAuth(authPath); err != nil {
			return fmt.Errorf("codex logout: %w", err)
		}
		fmt.Printf("Removed the Coddy-managed Codex credential for provider %q.\n", name)
		return codexStatus(name, authPath)
	default:
		return codexUsageErr()
	}
}

func codexUsageErr() error {
	return fmt.Errorf("usage: %s codex login|status|logout [--provider NAME] [--home DIR]", os.Args[0])
}

// resolveCodexProvider picks the provider entry to sign in for. An explicit
// name must exist and be a codex provider; without one the first codex provider
// in config.yaml wins, falling back to the conventional name "codex" so a fresh
// install can sign in before editing config.yaml.
func resolveCodexProvider(cfg *config.Config, requested string) (string, *config.ProviderConfig, error) {
	requested = strings.TrimSpace(requested)
	if requested != "" {
		for i := range cfg.Providers {
			if cfg.Providers[i].Name != requested {
				continue
			}
			if cfg.Providers[i].Type != "codex" {
				return "", nil, fmt.Errorf("provider %q has type %q, not codex", requested, cfg.Providers[i].Type)
			}
			return requested, &cfg.Providers[i], nil
		}
		probe := config.ProviderConfig{Name: requested, Type: "codex"}
		probe.Normalize()
		if err := probe.Validate(); err != nil {
			return "", nil, err
		}
		return requested, &probe, nil
	}
	for i := range cfg.Providers {
		if cfg.Providers[i].Type == "codex" {
			return cfg.Providers[i].Name, &cfg.Providers[i], nil
		}
	}
	return "codex", &config.ProviderConfig{Name: "codex", Type: "codex"}, nil
}

func codexLogin(prov *config.ProviderConfig, name, authPath string) error {
	client, err := llm.HTTPClientForOptionalProxy(prov.Proxy)
	if err != nil {
		return err
	}
	// Ctrl-C must abandon the wait without leaving a half-written credential.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	err = llm.CodexDeviceSignIn(ctx, llm.CodexIssuerURL, client, authPath, func(login llm.CodexDeviceLogin) {
		fmt.Printf("Open %s and enter the code %s\n", login.VerificationURL, login.UserCode)
		fmt.Println("Waiting for confirmation in the browser...")
	})
	if err != nil {
		return fmt.Errorf("codex login: %w", err)
	}
	fmt.Printf("Signed in. Credential stored at %s\n", authPath)
	return codexStatus(name, authPath)
}

func codexStatus(name, authPath string) error {
	status, err := llm.InspectCodexAuth(authPath)
	if err != nil {
		return fmt.Errorf("codex status: %w", err)
	}
	if !status.Connected {
		fmt.Printf("Provider %q: not connected. Run `%s codex login`.\n", name, os.Args[0])
		return nil
	}
	source := "Coddy-managed credential"
	if status.Source == "codex_cli" {
		source = "Codex CLI login (" + llm.CodexCLIAuthPath() + ")"
	}
	account := status.AccountID
	if account == "" {
		account = "unknown"
	}
	fmt.Printf("Provider %q: connected via %s, ChatGPT account %s\n", name, source, account)
	return nil
}
