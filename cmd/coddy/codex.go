package main

// The codex half of `coddy providers login|logout <name>`: the terminal
// counterpart of Settings -> LLM Providers -> Sign In with ChatGPT, for ACP and
// headless setups that never open the web UI. Credentials land in the same
// place the HTTP surface uses ($CODDY_HOME/providers/<name>/codex-auth.json).

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func codexLogin(cfg *config.Config, prov *config.ProviderConfig, name, authPath string, noConfig bool) error {
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
	if !noConfig {
		codexWriteConfig(ctx, cfg, prov, name, authPath)
	}
	return codexStatus(name, authPath)
}

// codexWriteConfig publishes the subscription catalog into config.yaml and
// reports what it added. The sign-in itself has already succeeded by the time
// it runs, so a catalog or config problem is a note on stderr, not a failed
// login: the credential is on disk either way.
func codexWriteConfig(ctx context.Context, cfg *config.Config, prov *config.ProviderConfig, name, authPath string) {
	added, err := llm.ApplyCodexLoginToConfig(ctx, cfg, name, authPath, prov.Proxy)
	if err != nil {
		fmt.Fprintf(os.Stderr, "note: could not update config.yaml: %v\n", err)
		return
	}
	if len(added) == 0 {
		fmt.Println("config.yaml already lists this provider and its models.")
		return
	}
	fmt.Printf("Updated %s: %s\n", cfg.Paths.ConfigPath, strings.Join(added, ", "))
	fmt.Println("A running `coddy serve` server keeps its loaded config; restart it (or edit settings in the UI) to pick the changes up.")
}

func codexStatus(name, authPath string) error {
	status, err := llm.InspectCodexAuth(authPath)
	if err != nil {
		return fmt.Errorf("codex status: %w", err)
	}
	if !status.Connected {
		fmt.Printf("Provider %q: not connected. Run `%s providers login %s`.\n", name, os.Args[0], name)
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
