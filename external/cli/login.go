//go:build cli

package cli

// /login signs this machine in to a model provider without leaving the
// console: the sign-in `coddy providers login` runs in a shell, through
// internal/providerlogin. A bare /login signs in to NeuralDeep; a name picks
// codex, devin, opencode or a provider config.yaml lists. The sign-in adds
// the provider and every model it serves to config.yaml, and the console
// installs the new configuration at once, so the next prompt runs on it.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/external/cli/tui"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/platform"
	"github.com/EvilFreelancer/coddy-agent/internal/providerlogin"
)

// loginHint is what the console says while no provider can answer: in the
// header of a fresh install, and after a turn that failed for it.
const loginHint = "No model provider is set up yet. Type /login to sign in to NeuralDeep, or /login codex, devin or opencode for another provider."

const loginUsage = "Usage: /login [provider] - neuraldeep (the default), codex, devin, opencode, or a provider config.yaml lists"

// loginRun is the sign-in /login started, while it waits for the person.
type loginRun struct {
	// title names the product ("NeuralDeep"), typ the provider type.
	title string
	typ   string
	ctx   context.Context
	// cancel stops the sign-in (escape, or the console closing).
	cancel context.CancelFunc
	// paste feeds a Devin sign-in the address its browser was sent back to,
	// pasted into the editor; nil for the device flows.
	paste *io.PipeWriter
	// modelBefore is the model the session ran on when the sign-in started.
	modelBefore string
}

// loginTarget is the provider a /login names: NeuralDeep when it names none.
func loginTarget(args []string) string {
	if len(args) == 0 {
		return providerlogin.DefaultType
	}
	return args[0]
}

// needsLogin reports a local console whose configuration has no provider
// that can answer. Over --remote the server's configuration decides, and this
// one says nothing about it.
func (a *App) needsLogin() bool {
	return a.remoteURL == "" && !providerlogin.Ready(a.config())
}

// turnFailureHint is the line after a failed turn whose cause is the setup
// rather than the request: no provider can answer at all, or the session's
// model runs on one that cannot. "" otherwise, and over --remote.
func (a *App) turnFailureHint() string {
	if a.remoteURL != "" {
		return ""
	}
	cfg := a.config()
	if !providerlogin.Ready(cfg) {
		return loginHint
	}
	owner := usageProviderOf(a.modelID)
	if owner == "" || providerlogin.ProviderReady(cfg, owner) {
		return ""
	}
	if prov := cfg.FindProvider(owner); prov != nil && providerlogin.Supports(prov.Type) {
		return fmt.Sprintf("Provider %q has no sign-in: type /login %s, or press ctrl+l for a model of another provider.", owner, owner)
	}
	return fmt.Sprintf("Provider %q has no key: set api_key or %s, or press ctrl+l for a model of another provider.", owner, config.ProviderAPIKeyEnvVarName(owner))
}

// dispatchLogin handles /login [provider]. Every refusal says why before
// anything starts.
func (a *App) dispatchLogin(args []string) {
	switch {
	case a.remoteURL != "":
		a.appendStatus(roleWarning, "/login signs in a provider of this machine's config.yaml, and this console drives "+
			tui.SanitizeText(a.remoteURL)+": sign in on that server instead (Settings, or coddy providers login there)")
		return
	case a.login != nil:
		a.appendStatus(roleWarning, "A sign-in to "+a.login.title+" is already running (escape to stop it)")
		return
	case len(args) > 1:
		a.appendStatus(roleWarning, loginUsage)
		return
	}
	name := loginTarget(args)
	prov, err := providerlogin.Resolve(a.config(), name, "")
	if err != nil {
		var notConfigured *providerlogin.NotConfiguredError
		if errors.As(err, &notConfigured) {
			a.appendStatus(roleWarning, fmt.Sprintf("/login: no provider %q in config.yaml; name one of %s, or a provider config.yaml lists",
				tui.SanitizeText(name), strings.Join(providerlogin.Types, ", ")))
			return
		}
		a.appendStatus(roleWarning, "/login: "+err.Error())
		return
	}
	if !providerlogin.Supports(prov.Type) {
		if prov.Type == "acp" {
			a.appendStatus(roleWarning, fmt.Sprintf("Provider %q runs an ACP agent, which signs in on its own (claude login, codex login, ...)", prov.Name))
			return
		}
		a.appendStatus(roleWarning, fmt.Sprintf("Provider %q has type %q and takes a key: set api_key in config.yaml or %s. /login signs in to %s.",
			prov.Name, prov.Type, config.ProviderAPIKeyEnvVarName(prov.Name), strings.Join(providerlogin.Types, ", ")))
		return
	}
	a.startLogin(*prov)
}

// startLogin runs the sign-in on a worker; what it asks of the person and
// how it ends come back to the UI loop.
func (a *App) startLogin(prov config.ProviderConfig) {
	ctx, cancel := context.WithCancel(a.workCtx)
	run := &loginRun{title: providerlogin.Title(prov.Type), typ: prov.Type, ctx: ctx, cancel: cancel, modelBefore: a.modelID}
	hooks := providerlogin.Hooks{
		OnPrompt: func(p providerlogin.Prompt) {
			a.postUI(func() { a.showLoginPrompt(run, p) })
		},
		OnPasteRejected: func(reason string) {
			a.postUI(func() { a.appendStatus(roleWarning, reason) })
		},
		CodexIssuer: a.codexIssuer,
	}
	var pasteWriter *io.PipeWriter
	if prov.Type == "devin" {
		var r *io.PipeReader
		r, pasteWriter = io.Pipe()
		run.paste = pasteWriter
		hooks.Paste = r
	}
	a.login = run
	target := run.title
	if prov.Name != prov.Type {
		target += " (provider " + prov.Name + ")"
	}
	a.appendStatus(roleDim, "Signing in to "+target+"...")

	cfg := a.config()
	sessionID := a.sessionID
	a.workers.Add(1)
	go func() {
		defer a.workers.Done()
		defer cancel()
		res, err := providerlogin.Run(ctx, cfg, &prov, hooks)
		if pasteWriter != nil {
			// The sign-in reads the pipe on a goroutine of its own, which
			// ends here; a paste sent after this fails instead of waiting.
			_ = pasteWriter.Close()
		}
		var reloadErr error
		if err == nil && res.ConfigErr == nil && len(res.Added) > 0 {
			reloadErr = a.adoptLoginConfig(cfg, sessionID)
		}
		a.postUI(func() { a.finishLogin(run, res, err, reloadErr) })
	}()
}

// adoptLoginConfig loads the config.yaml a sign-in has just written and
// installs it in the manager, with the console's own overrides, the way a
// settings save does; then the console refreshes its model catalog, footer
// and header from it (replaceConfig). It runs on the sign-in's worker.
func (a *App) adoptLoginConfig(cfg *config.Config, sessionID string) error {
	if a.installConfig == nil {
		return errors.New("this console cannot reload its configuration")
	}
	next, err := config.LoadWithPaths(cfg.Paths)
	if err != nil {
		return err
	}
	if err := a.installConfig(next); err != nil {
		return err
	}
	a.replaceConfig(next, a.mgr.SessionByID(sessionID))
	return nil
}

// showLoginPrompt prints the page to open and the code to confirm, and opens
// the page in this machine's browser when there is one to open.
func (a *App) showLoginPrompt(run *loginRun, p providerlogin.Prompt) {
	if a.login != run {
		return
	}
	th := a.theme
	lines := []string{th.Fg(roleDim, "Open ") + th.Fg(roleAccent, tui.SanitizeText(p.URL))}
	if p.Code != "" {
		verb := "confirm"
		if run.typ == "codex" {
			verb = "enter"
		}
		lines = append(lines, th.Fg(roleDim, "and "+verb+" the code ")+th.Bold(th.Fg(roleAccent, tui.SanitizeText(p.Code))))
		lines = append(lines, th.Fg(roleDim, "Any browser will do, including one on your phone. Escape stops the sign-in."))
	} else {
		lines = append(lines, th.Fg(roleDim, "Sign in there; the page then sends the browser back to this machine."))
	}
	if p.Pasteable {
		lines = append(lines, th.Fg(roleDim, "If that browser runs on another machine, its last page will not load: copy its address, paste it here and press enter. Escape stops the sign-in."))
	}
	a.chat.AddChild(&statusLine{tui.NewText(strings.Join(lines, "\n"), 1, 0, nil)})
	if a.browserAvailable != nil && a.browserAvailable() && a.openURL != nil {
		// Best effort: the printed address is what counts.
		_ = a.openURL(p.URL)
	}
}

// finishLogin reports how the sign-in ended: who signed in and what
// config.yaml gained, or why it stopped.
func (a *App) finishLogin(run *loginRun, res providerlogin.Result, err, reloadErr error) {
	if a.login == run {
		a.login = nil
	}
	if err != nil {
		// Run wraps the cancellation of its context; the worker's own cancel
		// after Run returned must not turn a failure into a stop.
		if errors.Is(err, context.Canceled) {
			a.appendStatus(roleDim, "Sign-in to "+run.title+" stopped")
			return
		}
		a.appendStatus(roleError, "Sign-in to "+run.title+" failed: "+err.Error())
		return
	}
	signed := "Signed in to " + run.title
	if res.Account != "" {
		signed += " as " + res.Account
	}
	a.appendStatus(roleSuccess, signed)
	if res.Override != "" {
		a.appendStatus(roleWarning, fmt.Sprintf("Provider %q has %s, which wins over this sign-in; remove it to use the sign-in", res.Provider, res.Override))
	}
	switch {
	case res.ConfigErr != nil:
		a.appendStatus(roleWarning, "Could not update config.yaml: "+res.ConfigErr.Error())
	case len(res.Added) == 0:
		a.appendStatus(roleDim, "config.yaml already lists provider "+res.Provider+" and its models")
	default:
		a.appendStatus(roleDim, "Added to config.yaml: "+providerlogin.SummarizeAdded(res.Added))
		if reloadErr != nil {
			a.appendStatus(roleWarning, "The console could not load the new config.yaml ("+reloadErr.Error()+"); restart coddy to use it")
		}
	}
	if res.AgentModelErr != nil {
		a.appendStatus(roleWarning, "Could not move agent.model to provider "+res.Provider+": "+res.AgentModelErr.Error())
	}
	if res.AgentModelFrom != "" {
		a.appendStatus(roleDim, "agent.model named "+tui.SanitizeText(res.AgentModelFrom)+", whose provider cannot answer; new sessions start on the signed-in provider now")
	}
	if reloadErr == nil {
		a.adoptSignedInModel(res.Provider)
	}
	if run.modelBefore != "" && a.modelID != run.modelBefore {
		// The session moved: through agent.model the reload brought, or by
		// adoptSignedInModel.
		a.appendStatus(roleDim, "This session runs on "+tui.SanitizeText(a.modelID)+" now")
	}
	// The stored sign-in is what makes the provider ready, whether or not
	// config.yaml changed: the header and the usage line follow it now.
	a.populateHeader()
	a.refreshUsage(usageProviderOf(a.modelID), false)
}

// adoptSignedInModel moves the session to the provider just signed in when
// the model it runs on cannot answer - a fresh install's session on the
// example config's openai row without a key - and otherwise says how to pick
// one of the new models.
func (a *App) adoptSignedInModel(provider string) {
	if strings.HasPrefix(a.modelID, provider+"/") {
		return
	}
	cfg := a.config()
	if a.modelID != "" && providerlogin.ProviderReady(cfg, usageProviderOf(a.modelID)) {
		a.appendStatus(roleDim, "ctrl+l or /model picks one of its models")
		return
	}
	target := providerlogin.DefaultModel(cfg, provider)
	if target == "" {
		return
	}
	a.modelID = target
	a.refreshFooterModel()
	a.setModel(target)
}

// stopLogin stops the running sign-in; finishLogin reports it.
func (a *App) stopLogin() {
	if a.login != nil {
		a.login.cancel()
	}
}

// feedLoginPaste hands a pasted callback address to a Devin sign-in that
// waits for it. Anything else goes its usual way.
func (a *App) feedLoginPaste(text string) bool {
	run := a.login
	if run == nil || run.paste == nil || !looksLikeCallback(text) {
		return false
	}
	w := run.paste
	line := strings.TrimSpace(text) + "\n"
	// The sign-in reads the pipe on its own goroutine; once it has ended the
	// write fails on the closed pipe rather than holding the console.
	go func() { _, _ = io.WriteString(w, line) }()
	a.appendStatus(roleDim, "Address received, finishing the sign-in to "+run.title)
	return true
}

// looksLikeCallback reports what a Devin sign-in takes as pasted: one word
// with a code in it - the address the browser was sent back to, quoted or
// not, with its scheme or without, or the query alone. The sign-in checks the
// state itself and says why it refuses a line (OnPasteRejected).
func looksLikeCallback(text string) bool {
	t := strings.Trim(strings.TrimSpace(text), `"'`)
	return t != "" && !strings.ContainsAny(t, " \t\n") && strings.Contains(t, "code=")
}

// Defaults of the browser seams, which tests replace.
var (
	defaultOpenURL          = platform.OpenBrowser
	defaultBrowserAvailable = platform.LocalBrowserAvailable
)
