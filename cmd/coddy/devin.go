package main

// The devin half of `coddy providers login|logout <name>`: a browser sign-in
// to a Devin account (the PKCE flow of `devin auth login`), or, with
// --devin-cli, the login the Devin CLI already holds. The session token lands
// in $CODDY_HOME/providers/<name>/devin-auth.json; the Devin CLI's own file is
// only ever read.

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// devinPasteInput is where a sign-in reads a pasted callback address from.
// A variable so the harness can feed it (or leave the terminal alone).
var devinPasteInput io.Reader = os.Stdin

func devinLogin(cfg *config.Config, prov *config.ProviderConfig, useDevinCLI, noConfig bool) error {
	client, err := llm.HTTPClientForProviderProxy(prov.Proxy)
	if err != nil {
		return err
	}
	authPath := config.DevinAuthPath(cfg.Paths.Home, prov.Name)
	if authPath == "" {
		return fmt.Errorf("providers: could not resolve the credential path for provider %q", prov.Name)
	}
	// Ctrl-C must abandon the wait without leaving a half-written credential.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if useDevinCLI {
		// The Devin CLI login is one account and serves one row; tying a
		// second row to it would make two profiles of one account.
		if !cfg.ProviderMayUseCLILogin(prov.Name, "devin") {
			return fmt.Errorf("devin login: the Devin CLI login serves %s; sign provider %q in with `%s providers login %s` instead",
				llm.CLILoginServes(cfg, "devin"), prov.Name, os.Args[0], prov.Name)
		}
		// A Coddy-managed login wins over the Devin CLI one at every request,
		// so going on would publish one account's catalog while the requests
		// run on another.
		if managed, _ := llm.InspectDevinAuth(authPath, false); managed.Source == llm.DevinSourceCoddy {
			return fmt.Errorf("devin login: provider %q has a Coddy-managed login at %s, which takes precedence over the Devin CLI login; run `%s providers logout %s` first",
				prov.Name, authPath, os.Args[0], prov.Name)
		}
		cliPath := llm.DevinCLICredentialsPath()
		st, account, err := llm.VerifyDevinCredential(ctx, client, "", "", true)
		if err != nil {
			return fmt.Errorf("devin login: the Devin CLI login at %s cannot be used: %w", cliPath, err)
		}
		fmt.Printf("Using the Devin CLI login at %s%s\n", st.Path, devinAccountSuffix(account))
	} else {
		account, err := llm.DevinSignIn(ctx, client, authPath, llm.DevinSignInOptions{
			OnPrompt: func(p llm.DevinLoginPrompt) {
				fmt.Printf("Open %s\n", p.AuthURL)
				fmt.Println("Sign in there; the page then sends the browser back to this machine.")
				fmt.Println("If the browser runs on another machine, that last page will not load: copy its address from the address bar and paste it here.")
				fmt.Println("Waiting for the sign-in to finish...")
				if localBrowserAvailableFn() {
					_ = openBrowserFn(p.AuthURL)
				}
			},
			Paste: devinPasteInput,
			OnPasteRejected: func(reason string) {
				fmt.Fprintf(os.Stderr, "note: %s\n", reason)
			},
		})
		if err != nil {
			return fmt.Errorf("devin login: %w", err)
		}
		fmt.Printf("Signed in%s. Credential stored at %s\n", devinAccountSuffix(account), authPath)
	}
	if explicitKeySource(prov) != "" {
		fmt.Printf("note: provider %q has %s, which wins over the login; remove it to use the login.\n", prov.Name, explicitKeySource(prov))
	}
	if !noConfig {
		devinWriteConfig(ctx, cfg, prov, authPath)
	}
	return nil
}

func devinAccountSuffix(a llm.DevinAccount) string {
	switch {
	case a.Email != "" && a.Name != "" && a.Name != a.Email:
		return " as " + a.Name + " <" + a.Email + ">"
	case a.Email != "":
		return " as " + a.Email
	case a.Name != "":
		return " as " + a.Name
	}
	return ""
}

// devinWriteConfig publishes the account's catalog into config.yaml. The
// sign-in has succeeded by the time it runs, so a problem here is a note.
func devinWriteConfig(ctx context.Context, cfg *config.Config, prov *config.ProviderConfig, authPath string) {
	added, err := llm.ApplyDevinLoginToConfig(ctx, cfg, prov.Name, "", authPath, prov.Proxy)
	if err != nil {
		fmt.Fprintf(os.Stderr, "note: could not update config.yaml: %v\n", err)
		return
	}
	if len(added) == 0 {
		fmt.Println("config.yaml already lists this provider and its models.")
		return
	}
	fmt.Printf("Updated %s: %s\n", cfg.Paths.ConfigPath, summarizeAdded(added))
	fmt.Println("A running `coddy serve` server keeps its loaded config; restart it (or edit settings in the UI) to pick the changes up.")
}

// summarizeAdded names what a login added, folding a long model list into a
// count so a catalog of forty families does not flood the terminal.
func summarizeAdded(added []string) string {
	var models, other []string
	for _, a := range added {
		if strings.HasPrefix(a, "model ") {
			models = append(models, strings.TrimPrefix(a, "model "))
			continue
		}
		other = append(other, a)
	}
	if len(models) > 6 {
		other = append(other, fmt.Sprintf("%d models (%s, ...)", len(models), strings.Join(models[:3], ", ")))
	} else {
		for _, m := range models {
			other = append(other, "model "+m)
		}
	}
	return strings.Join(other, ", ")
}

// devinCredentialSummary is the `providers list` line of a devin row.
func devinCredentialSummary(cfg *config.Config, prov *config.ProviderConfig) string {
	cliLogin := cfg.ProviderMayUseCLILogin(prov.Name, "devin")
	st, err := llm.InspectDevinAuth(config.DevinAuthPath(cfg.Paths.Home, prov.Name), cliLogin)
	explicit := explicitKeySource(prov)
	switch {
	case err != nil:
		return "login unreadable: " + err.Error()
	case explicit != "" && st.Connected:
		return explicit + " overrides the Devin login (" + st.Masked + "); remove it to use the login"
	case explicit != "":
		return explicit
	case st.Source == llm.DevinSourceCoddy:
		who := st.Masked
		if st.Email != "" {
			who = st.Email + ", " + st.Masked
		}
		return "signed in to Devin (" + who + ")"
	case st.Source == llm.DevinSourceDevinCLI:
		return "Devin CLI login " + st.Path + " (" + st.Masked + ")"
	default:
		if present, _ := llm.DevinCLILoginPresent(); present && !cliLogin {
			return "not connected (the Devin CLI login serves " + llm.CLILoginServes(cfg, "devin") + "); run `" + os.Args[0] + " providers login " + prov.Name + "`"
		}
		return "not connected; run `" + os.Args[0] + " providers login " + prov.Name + "` (or `devin auth login`)"
	}
}

func devinLogout(cfg *config.Config, prov *config.ProviderConfig) error {
	authPath := config.DevinAuthPath(cfg.Paths.Home, prov.Name)
	if err := llm.RemoveDevinAuth(authPath); err != nil {
		return fmt.Errorf("providers logout: %w", err)
	}
	fmt.Printf("Removed the Coddy-managed Devin credential for provider %q.\n", prov.Name)
	if st, err := llm.InspectDevinAuth(authPath, cfg.ProviderMayUseCLILogin(prov.Name, "devin")); err == nil && st.Source == llm.DevinSourceDevinCLI {
		fmt.Printf("The Devin CLI login at %s stays and the provider keeps using it; run `devin auth logout` to end that one.\n", st.Path)
	}
	return nil
}
