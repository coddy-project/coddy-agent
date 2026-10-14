package main

// The opencode half of `coddy providers login|logout <name>`: OpenCode Go and
// Console have no OAuth or device flow, so signing in is pasting the API key
// the web console issues. The key lands in
// $CODDY_HOME/providers/<name>/opencode-auth.json; an explicit api_key,
// api_key_command or OPENCODE_API_KEY still wins at request time.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// opencodeKeyInput is where a login reads a pasted key from when it is not
// given on the flag and standard input is a pipe. A variable so the harness can
// feed it (or leave the terminal alone).
var opencodeKeyInput io.Reader = os.Stdin

// resolveOpenCodeAPIBase settles which OpenCode gateway a login stores and
// talks to: --api-base wins, then the provider row, then the default (Go). An
// unknown flag value is an error rather than a silent fallback, so a key is
// never stored against a gateway the operator did not mean. A row that names no
// OpenCode endpoint resolves to the default explicitly, so the login records it
// and repairs the row instead of leaving the unusable value behind.
func resolveOpenCodeAPIBase(flagValue, configured string) (string, error) {
	if strings.TrimSpace(flagValue) != "" {
		base, ok := llm.NormalizeOpenCodeAPIBase(flagValue)
		if !ok {
			return "", fmt.Errorf("--api-base %q is not an OpenCode gateway; use one of %s",
				strings.TrimSpace(flagValue), strings.Join(llm.OpenCodeAPIBases(), ", "))
		}
		return base, nil
	}
	if base, ok := llm.NormalizeOpenCodeAPIBase(configured); ok {
		return base, nil
	}
	if strings.TrimSpace(configured) != "" {
		return llm.OpenCodeDefaultAPIBase(), nil
	}
	return llm.OpenCodeDefaultAPIBase(), nil
}

// opencodeLogin stores a pasted API key for an opencode provider and, unless
// --no-config, writes the provider row and the gateway's models into
// config.yaml. apiKey is the --api-key flag value; empty reads it from the
// terminal (without echo) or from a piped standard input.
func opencodeLogin(cfg *config.Config, prov *config.ProviderConfig, apiKey string, noConfig bool, apiBase string) error {
	endpoint, err := resolveOpenCodeAPIBase(apiBase, prov.APIBase)
	if err != nil {
		return err
	}
	authPath := config.OpenCodeAuthPath(cfg.Paths.Home, prov.Name)
	if authPath == "" {
		return fmt.Errorf("providers: could not resolve the credential path for provider %q", prov.Name)
	}
	key := strings.TrimSpace(apiKey)
	if key == "" {
		fmt.Printf("Copy your API key from %s, then paste it here.\n", opencodeConsoleURL(endpoint))
		key, err = readOpenCodeKey()
		if err != nil {
			return err
		}
	}
	if err := llm.SaveOpenCodeAuth(authPath, key, endpoint); err != nil {
		return fmt.Errorf("opencode login: %w", err)
	}
	fmt.Printf("Stored the OpenCode key for %s at %s\n", endpoint, authPath)
	if explicitKeySource(prov) != "" {
		fmt.Printf("note: provider %q has %s, which wins over the login; remove it to use the login.\n", prov.Name, explicitKeySource(prov))
	}
	if noConfig {
		return nil
	}
	opencodeWriteConfig(cfg, prov, endpoint, key)
	return nil
}

// opencodeConsoleURL names the web console a key is copied from, for the
// gateway the login stores against.
func opencodeConsoleURL(endpoint string) string {
	return "https://opencode.ai/console"
}

// readOpenCodeKey reads one secret line: from the terminal without echo when
// standard input is one, else from the piped input, so the command works by
// hand and in a script alike.
func readOpenCodeKey() (string, error) {
	if f, ok := opencodeKeyInput.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		fmt.Print("API key: ")
		b, err := term.ReadPassword(int(f.Fd()))
		_, _ = fmt.Fprintln(os.Stderr)
		if err != nil {
			return "", fmt.Errorf("read key: %w", err)
		}
		return requireOpenCodeKey(string(b))
	}
	reader := bufio.NewReader(opencodeKeyInput)
	line, err := reader.ReadString('\n')
	if err != nil && (err != io.EOF || line == "") {
		return "", fmt.Errorf("read key from stdin: %w", err)
	}
	return requireOpenCodeKey(line)
}

func requireOpenCodeKey(s string) (string, error) {
	key := strings.TrimSpace(s)
	if key == "" {
		return "", errors.New("the API key is empty, nothing was stored")
	}
	return key, nil
}

// opencodeWriteConfig publishes the gateway's catalog into config.yaml. The key
// is already stored by the time it runs, so a problem here is a note.
func opencodeWriteConfig(cfg *config.Config, prov *config.ProviderConfig, endpoint, key string) {
	ctx := context.Background()
	added, err := llm.ApplyOpenCodeLoginToConfig(ctx, cfg, prov.Name, endpoint, key, prov.Proxy)
	if err != nil {
		fmt.Fprintf(os.Stderr, "note: could not update config.yaml: %v\n", llm.RedactOpenCodeSecrets(err.Error()))
		return
	}
	if len(added) == 0 {
		fmt.Println("config.yaml already lists this provider and its models.")
		return
	}
	fmt.Printf("Updated %s: %s\n", cfg.Paths.ConfigPath, summarizeAdded(added))
	fmt.Println("A running `coddy serve` server keeps its loaded config; restart it (or edit settings in the UI) to pick the changes up.")
}

// opencodeLogout removes the stored key. The gateway issues long-lived keys with
// no revoke endpoint Coddy can call, so ending the key itself happens in the
// web console.
func opencodeLogout(cfg *config.Config, prov *config.ProviderConfig) error {
	authPath := config.OpenCodeAuthPath(cfg.Paths.Home, prov.Name)
	if err := llm.RemoveOpenCodeAuth(authPath); err != nil {
		return fmt.Errorf("providers logout: %w", err)
	}
	fmt.Printf("Removed the OpenCode credential for provider %q.\n", prov.Name)
	fmt.Println("The key stays valid until you revoke it in the console: https://opencode.ai/console")
	return nil
}

// opencodeCredentialSummary is the `providers list` line of an opencode row.
func opencodeCredentialSummary(cfg *config.Config, prov *config.ProviderConfig) string {
	st, err := llm.InspectOpenCodeAuth(config.OpenCodeAuthPath(cfg.Paths.Home, prov.Name))
	explicit := explicitKeySource(prov)
	switch {
	case err != nil:
		return "login unreadable: " + llm.RedactOpenCodeSecrets(err.Error())
	case explicit != "" && st.Connected:
		return explicit + " overrides the OpenCode login (" + st.Masked + "); remove it to use the login"
	case explicit != "":
		return explicit
	case st.Connected:
		return "stored OpenCode key (" + st.Masked + ")"
	default:
		return "no credentials; run `" + os.Args[0] + " providers login " + prov.Name + "` or set api_key / OPENCODE_API_KEY"
	}
}

// opencodeEndpointNote names the gateway when a row is not on the default (Go)
// one, so `providers list` shows which it talks to.
func opencodeEndpointNote(prov *config.ProviderConfig) string {
	if prov.Type != "opencode" {
		return ""
	}
	base, ok := llm.NormalizeOpenCodeAPIBase(prov.APIBase)
	if !ok || base == llm.OpenCodeDefaultAPIBase() {
		return ""
	}
	return " [" + base + "]"
}
