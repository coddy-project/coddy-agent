package main

// The opencode half of `coddy providers login|logout <name>`: OpenCode Go and
// Console use account device authorization or a service account API key.
// The credential lands in
// $CODDY_HOME/providers/<name>/opencode-auth.json; an explicit api_key,
// api_key_command or the row's NAME_API_KEY still wins at request time.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"golang.org/x/term"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// opencodeKeyInput is where a login reads a pasted key from when it is not
// given on the flag and standard input is a pipe. A variable so the harness can
// feed it (or leave the terminal alone).
var opencodeKeyInput io.Reader = os.Stdin

// resolveOpenCodeAPIBase settles which OpenCode gateway the row the login
// writes talks to: --api-base wins, then the provider row, then the default
// (Go). The login itself belongs to the account and serves either gateway, as
// in the OpenCode client. An unknown flag value is an error rather than a
// silent fallback, so a row never ends up on a gateway the operator did not
// mean. A row that names no OpenCode endpoint resolves to the default
// explicitly, so the login repairs the row instead of leaving the unusable
// value behind.
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
	// A row that names no OpenCode endpoint - empty, or a value left over from
	// another provider type - resolves to the default explicitly, so the login
	// records it and repairs the row instead of leaving the unusable value
	// behind.
	return llm.OpenCodeDefaultAPIBase(), nil
}

// opencodeLogin signs in an account, or stores an explicit service account
// key. Unless --no-config, it also writes the gateway's catalog into config.yaml.
func opencodeLogin(cfg *config.Config, prov *config.ProviderConfig, apiKey string, noConfig bool, apiBase string) error {
	if noConfig && strings.TrimSpace(apiBase) != "" {
		return errors.New("providers login: --api-base picks the gateway of the row in config.yaml, which --no-config leaves alone; the OpenCode login serves both gateways, so set the row's api_base instead")
	}
	endpoint, err := resolveOpenCodeAPIBase(apiBase, prov.APIBase)
	if err != nil {
		return err
	}
	authPath := config.OpenCodeAuthPath(cfg.Paths.Home, prov.Name)
	if authPath == "" {
		return fmt.Errorf("providers: could not resolve the credential path for provider %q", prov.Name)
	}
	key := strings.TrimSpace(apiKey)
	if key == "-" {
		fmt.Println("Paste a service account API key from https://opencode.ai/console")
		key, err = readOpenCodeKey()
		if err != nil {
			return err
		}
	}
	if key == "" {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		login, err := llm.StartOpenCodeDeviceLogin(ctx, prov.Proxy)
		if err != nil {
			return fmt.Errorf("opencode login: %w", err)
		}
		fmt.Printf("Open %s\nand confirm the code %s\n", login.URL, login.UserCode)
		fmt.Println("Sign in and select your OpenCode organization. Waiting for confirmation...")
		if localBrowserAvailableFn() {
			_ = openBrowserFn(login.URL)
		}
		if err := login.Wait(ctx, authPath); err != nil {
			return fmt.Errorf("opencode login: %w", err)
		}
		fmt.Printf("Signed in. Credential stored at %s\n", authPath)
	} else {
		if err := llm.SaveOpenCodeAuth(authPath, key); err != nil {
			return fmt.Errorf("opencode login: %w", err)
		}
		fmt.Printf("Stored the OpenCode key at %s\n", authPath)
	}
	if explicitKeySource(prov) != "" {
		fmt.Printf("note: provider %q has %s, which wins over the login; remove it to use the login.\n", prov.Name, explicitKeySource(prov))
	}
	if noConfig {
		return nil
	}
	opencodeWriteConfig(cfg, prov, endpoint, key)
	return nil
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

// opencodeWriteConfig publishes the catalog after the credential is stored,
// so a config update failure is reported as a note.
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

// opencodeLogout removes the local credential. Server-side sessions and
// service account keys are revoked in the Console.
func opencodeLogout(cfg *config.Config, prov *config.ProviderConfig) error {
	authPath := config.OpenCodeAuthPath(cfg.Paths.Home, prov.Name)
	// A damaged credential must still be removable by logout.
	status, _ := llm.InspectOpenCodeAuth(authPath)
	if err := llm.RemoveOpenCodeAuth(authPath); err != nil {
		return fmt.Errorf("providers logout: %w", err)
	}
	fmt.Printf("Removed the OpenCode credential for provider %q.\n", prov.Name)
	if status.Account {
		fmt.Println("Revoke the account session in https://opencode.ai/console to end it on the server.")
	} else {
		fmt.Println("The key stays valid until you revoke it in the console: https://opencode.ai/console")
	}
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
		if st.Account {
			return "OpenCode account (" + st.Organization + ")"
		}
		return "stored OpenCode key (" + st.Masked + ")"
	default:
		return "no credentials; run `" + os.Args[0] + " providers login " + prov.Name + "` or set api_key / " + config.ProviderAPIKeyEnvVarName(prov.Name)
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
