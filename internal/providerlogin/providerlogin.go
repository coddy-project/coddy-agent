// Package providerlogin signs a model provider in without a key: the device
// sign-ins of NeuralDeep, Codex and OpenCode and the browser sign-in of Devin,
// then the catalog each one adds to config.yaml. The console's /login runs it
// whole; `coddy providers login` shares its provider resolution, its endpoint
// rules, its summary of what a sign-in added and the move of an agent.model
// that cannot answer (RepointAgentModel).
package providerlogin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// DefaultType is what a sign-in that names no provider signs in to.
const DefaultType = "neuraldeep"

// Types are the provider types that sign in without a key, in the order a
// surface offers them; the first is DefaultType.
var Types = []string{DefaultType, "codex", "devin", "opencode"}

// Supports reports whether providerType signs in here.
func Supports(providerType string) bool {
	return slices.Contains(Types, strings.TrimSpace(providerType))
}

// Title is the product name of a sign-in type, for a line a person reads.
func Title(providerType string) string {
	switch providerType {
	case "neuraldeep":
		return "NeuralDeep"
	case "codex":
		return "Codex"
	case "devin":
		return "Devin"
	case "opencode":
		return "OpenCode"
	}
	return providerType
}

// NotConfiguredError is Resolve's answer for a name config.yaml does not
// list that names no sign-in type either.
type NotConfiguredError struct{ Name string }

func (e *NotConfiguredError) Error() string {
	return fmt.Sprintf("provider %q is not in config.yaml", e.Name)
}

// UnsupportedTypeError is the answer for a provider type with no sign-in.
type UnsupportedTypeError struct{ Type string }

func (e *UnsupportedTypeError) Error() string {
	return fmt.Sprintf("type %q has no sign-in; %s sign in without a key", e.Type, strings.Join(Types, ", "))
}

// Resolve picks the provider entry a sign-in is for. A name config.yaml lists
// wins; otherwise providerType, or a name that is itself a sign-in type
// ("neuraldeep", "codex", "devin", "opencode"), makes a probe entry of that
// type, so a fresh install - or another profile of a type already configured
// - signs in before config.yaml names it. A configured row is returned
// whatever its type: whether it signs in is the caller's question (Supports).
func Resolve(cfg *config.Config, name, providerType string) (*config.ProviderConfig, error) {
	providerType = strings.TrimSpace(providerType)
	if prov := cfg.FindProvider(name); prov != nil {
		if providerType != "" && providerType != prov.Type {
			return nil, fmt.Errorf("provider %q has type %q in config.yaml, not %q", prov.Name, prov.Type, providerType)
		}
		return prov, nil
	}
	if providerType == "" && Supports(name) {
		providerType = name
	}
	switch {
	case providerType == "":
		return nil, &NotConfiguredError{Name: name}
	case !Supports(providerType):
		return nil, &UnsupportedTypeError{Type: providerType}
	}
	probe := config.ProviderConfig{Name: name, Type: providerType}
	probe.Normalize()
	if err := probe.Validate(); err != nil {
		return nil, err
	}
	return &probe, nil
}

// NeuralDeepAPIBase settles which NeuralDeep deployment a sign-in talks to:
// flagValue (--api-base) wins, then the provider row, then the default
// (reported as an empty string when the row is silent). An unknown flag value
// is an error rather than a silent fallback - the person would otherwise sign
// in against the wrong hub and only find out when requests start failing. A
// row that names no NeuralDeep endpoint resolves to the default explicitly,
// so the sign-in records it and repairs the row instead of leaving the
// unusable value behind.
func NeuralDeepAPIBase(flagValue, configured string) (string, error) {
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

// OpenCodeAPIBase settles which OpenCode gateway the row a sign-in writes
// talks to: flagValue (--api-base) wins, then the provider row, then the
// default (Go). The sign-in itself belongs to the account and serves either
// gateway, as in the OpenCode client. An unknown flag value is an error
// rather than a silent fallback, so a row never ends up on a gateway the
// operator did not mean. A row that names no OpenCode endpoint - empty, or a
// value left over from another provider type - resolves to the default
// explicitly, so the sign-in records it and repairs the row.
func OpenCodeAPIBase(flagValue, configured string) (string, error) {
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
	return llm.OpenCodeDefaultAPIBase(), nil
}

// SummarizeAdded names what a sign-in added to config.yaml, folding a long
// model list into a count so a catalog of forty models does not flood the
// terminal.
func SummarizeAdded(added []string) string {
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

// Ready reports whether cfg names a provider that can answer a request: a
// row with a key (api_key, api_key_command or its NAME_API_KEY variable), a
// sign-in stored for it or the CLI login it may use, an acp row (its agent
// signs in on its own), or an OpenAI-compatible row aimed at an endpoint of
// its own, which may need no key (a local server). A fresh install answers
// false - no config.yaml, or the example one with its key unset - and that
// is when /login is the way on. It runs no credential helper and makes no
// request, so a surface can ask it whenever it draws.
func Ready(cfg *config.Config) bool {
	if cfg == nil {
		return false
	}
	for i := range cfg.Providers {
		if rowReady(cfg, &cfg.Providers[i]) {
			return true
		}
	}
	return false
}

func rowReady(cfg *config.Config, prov *config.ProviderConfig) bool {
	home := cfg.Paths.Home
	switch prov.Type {
	case "acp":
		return prov.ACP != nil && strings.TrimSpace(prov.ACP.Command) != ""
	case "codex":
		// Not llm.InspectCodexAuth: that one waits on the lock a token
		// refresh holds through its request, and a surface asks this while
		// it draws.
		return llm.CodexLoginStored(config.CodexAuthPath(home, prov.Name), cfg.ProviderMayUseCLILogin(prov.Name, "codex"))
	}
	if keySource(prov) != "" {
		return true
	}
	switch prov.Type {
	case "openai", "anthropic":
		// An endpoint of its own may need no key: a local server, a gateway.
		return strings.TrimSpace(prov.APIBase) != ""
	case "neuraldeep":
		st, err := llm.InspectNeuralDeepAuth(config.NeuralDeepAuthPath(home, prov.Name))
		return err == nil && st.Connected
	case "devin":
		st, err := llm.InspectDevinAuth(config.DevinAuthPath(home, prov.Name), cfg.ProviderMayUseCLILogin(prov.Name, "devin"))
		return err == nil && st.Connected
	case "opencode":
		st, err := llm.InspectOpenCodeAuth(config.OpenCodeAuthPath(home, prov.Name))
		return err == nil && st.Connected
	}
	return false
}

// ProviderReady reports whether the provider row name can answer, the way
// Ready decides it for the whole configuration.
func ProviderReady(cfg *config.Config, name string) bool {
	if cfg == nil {
		return false
	}
	prov := cfg.FindProvider(name)
	return prov != nil && rowReady(cfg, prov)
}

// DefaultModel is the model a session moves to after a sign-in to provider:
// agent.model when it runs on that provider, else the provider's first
// model in config.yaml, which a sign-in lists in the provider's own order.
// "" when config.yaml lists none.
func DefaultModel(cfg *config.Config, provider string) string {
	if cfg == nil {
		return ""
	}
	if owner, _, _ := config.SplitModelRef(strings.TrimSpace(cfg.Agent.Model)); owner == provider {
		return strings.TrimSpace(cfg.Agent.Model)
	}
	for _, m := range cfg.Models {
		if owner, _, _ := config.SplitModelRef(m.Model); owner == provider {
			return m.Model
		}
	}
	return ""
}

// keySource names the key a row carries besides a sign-in, without running
// the credential helper: "api_key from config.yaml", "api_key_command", the
// NAME_API_KEY variable, or "" when there is none.
func keySource(prov *config.ProviderConfig) string {
	if !prov.TakesAPIKey() {
		return ""
	}
	if strings.TrimSpace(prov.APIKey) != "" {
		return "api_key from config.yaml"
	}
	if strings.TrimSpace(prov.APIKeyCommand) != "" {
		return "api_key_command"
	}
	if env := config.ProviderAPIKeyEnvVarName(prov.Name); env != "" && strings.TrimSpace(os.Getenv(env)) != "" {
		return env + " env var"
	}
	return ""
}

// Prompt is what a sign-in asks of the person.
type Prompt struct {
	// URL is the page to open in a browser.
	URL string
	// Code is the code that page asks to confirm; empty when the page sends
	// the browser back to this machine instead (Devin).
	Code string
	// Pasteable says the sign-in also completes from the address the
	// browser was sent back to, pasted where the sign-in runs: the way in
	// when that browser runs on another machine.
	Pasteable bool
}

// Hooks connect a sign-in to the surface that runs it.
type Hooks struct {
	// OnPrompt shows the prompt once the flow waits for the person.
	OnPrompt func(Prompt)
	// Paste is read line by line while a pasteable sign-in waits; a line
	// holding the address the browser was sent back to completes it.
	Paste io.Reader
	// OnPasteRejected reports a pasted line that completes nothing.
	OnPasteRejected func(reason string)
	// CodexIssuer replaces the ChatGPT OAuth issuer; empty is the official
	// one. Tests point it at a stand-in.
	CodexIssuer string
}

// Result is what a finished sign-in did.
type Result struct {
	// Provider and Type name the row signed in.
	Provider string
	Type     string
	// Account says who signed in, when the provider tells.
	Account string
	// AuthPath is where the credential is stored.
	AuthPath string
	// Added names what config.yaml gained: the provider row, its models,
	// agent.model when none was set. Empty when it already had them all.
	Added []string
	// ConfigErr is set when the sign-in succeeded but config.yaml could not
	// be updated; the credential is stored either way.
	ConfigErr error
	// Override names a key of the row that wins over the sign-in at every
	// request, so the sign-in is not what requests use until it goes.
	Override string
	// AgentModelFrom is the agent.model the sign-in replaced because its
	// provider could not answer; "" when it left agent.model alone.
	AgentModelFrom string
	// AgentModelErr is set when that replacement failed; the provider and
	// its models are in config.yaml either way.
	AgentModelErr error
}

// Run signs prov in the way a person at a terminal can - the device flow of
// NeuralDeep, Codex and OpenCode, the browser sign-in of Devin - and then
// adds the provider row and every model the account serves to config.yaml,
// agent.model too when none is set: the catalog `coddy providers login` adds.
// An agent.model whose provider cannot answer moves to the provider signed in
// (RepointAgentModel).
// Every request goes through the row's own proxy setting. A sign-in stopped
// through ctx returns an error that wraps ctx's.
func Run(ctx context.Context, cfg *config.Config, prov *config.ProviderConfig, h Hooks) (Result, error) {
	if cfg == nil || prov == nil {
		return Result{}, errors.New("sign-in: no configuration")
	}
	if !Supports(prov.Type) {
		return Result{}, &UnsupportedTypeError{Type: prov.Type}
	}
	res := Result{Provider: prov.Name, Type: prov.Type, Override: keySource(prov)}
	var err error
	switch prov.Type {
	case "neuraldeep":
		err = runNeuralDeep(ctx, cfg, prov, h, &res)
	case "codex":
		err = runCodex(ctx, cfg, prov, h, &res)
	case "devin":
		err = runDevin(ctx, cfg, prov, h, &res)
	case "opencode":
		err = runOpenCode(ctx, cfg, prov, h, &res)
	}
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil && !errors.Is(err, ctxErr) {
			err = fmt.Errorf("%w: %v", ctxErr, err)
		}
		return Result{}, err
	}
	if res.ConfigErr == nil {
		from, to, err := RepointAgentModel(cfg.Paths, prov.Name)
		switch {
		case err != nil:
			res.AgentModelErr = err
		case to != "":
			res.Added = append(res.Added, "agent.model "+to)
			res.AgentModelFrom = from
		}
	}
	return res, nil
}

// RepointAgentModel moves agent.model to provider, just signed in, when the
// model it names runs on a row that cannot answer - the example config's
// openai row with OPENAI_API_KEY unset - so a new session does not start on a
// model no request reaches. It decides on config.yaml as it is now, after the
// sign-in wrote its catalog: an agent.model that answers, or one the operator
// changed meanwhile to a provider that answers, stays. It returns the model
// it replaced and the one it set, both "" when it left agent.model alone.
func RepointAgentModel(paths config.Paths, provider string) (from, to string, err error) {
	cfg, err := config.LoadWithPaths(paths)
	if err != nil {
		return "", "", err
	}
	current := strings.TrimSpace(cfg.Agent.Model)
	if current == "" {
		return "", "", nil
	}
	owner, _, _ := config.SplitModelRef(current)
	if owner == provider || ProviderReady(cfg, owner) {
		return "", "", nil
	}
	for _, m := range cfg.Models {
		if o, _, _ := config.SplitModelRef(m.Model); o == provider {
			to = m.Model
			break
		}
	}
	if to == "" {
		return "", "", nil
	}
	if _, err := config.CommitUCICommands(paths, []config.UCICommand{{Op: config.UCIOpSet, Path: "agent.model", Value: to}}); err != nil {
		return "", "", err
	}
	return current, to, nil
}

func (h Hooks) prompt(p Prompt) {
	if h.OnPrompt != nil {
		h.OnPrompt(p)
	}
}

func authPathFor(path, name string) (string, error) {
	if path == "" {
		return "", fmt.Errorf("could not resolve the credential path for provider %q", name)
	}
	return path, nil
}

func runNeuralDeep(ctx context.Context, cfg *config.Config, prov *config.ProviderConfig, h Hooks, res *Result) error {
	endpoint, err := NeuralDeepAPIBase("", prov.APIBase)
	if err != nil {
		return err
	}
	authPath, err := authPathFor(config.NeuralDeepAuthPath(cfg.Paths.Home, prov.Name), prov.Name)
	if err != nil {
		return err
	}
	client, err := llm.HTTPClientForProviderProxy(prov.Proxy)
	if err != nil {
		return err
	}
	hub := llm.NeuralDeepHubFor(endpoint)
	key, err := llm.NeuralDeepDeviceSignIn(ctx, hub, client, authPath, llm.NeuralDeepDeviceLabel(prov.Name), func(l llm.NeuralDeepDeviceLogin) {
		h.prompt(Prompt{URL: l.VerificationTarget(), Code: l.UserCode})
	})
	if err != nil {
		return fmt.Errorf("neuraldeep sign-in: %w", err)
	}
	res.AuthPath = authPath
	if who, whoErr := llm.FetchNeuralDeepWhoami(ctx, hub, key, client); whoErr == nil {
		display := who.Name
		if display == "" {
			display = who.Email
		}
		if display != "" && who.Tier != "" {
			display += " (tier " + who.Tier + ")"
		}
		res.Account = display
	}
	res.Added, res.ConfigErr = llm.ApplyNeuralDeepLoginToConfig(ctx, cfg, prov.Name, hub, endpoint, key, client)
	return nil
}

func runCodex(ctx context.Context, cfg *config.Config, prov *config.ProviderConfig, h Hooks, res *Result) error {
	authPath, err := authPathFor(config.CodexAuthPath(cfg.Paths.Home, prov.Name), prov.Name)
	if err != nil {
		return err
	}
	client, err := llm.HTTPClientForProviderProxy(prov.Proxy)
	if err != nil {
		return err
	}
	issuer := h.CodexIssuer
	if issuer == "" {
		issuer = llm.CodexIssuerURL
	}
	err = llm.CodexDeviceSignIn(ctx, issuer, client, authPath, func(login llm.CodexDeviceLogin) {
		h.prompt(Prompt{URL: login.VerificationURL, Code: login.UserCode})
	})
	if err != nil {
		return fmt.Errorf("codex sign-in: %w", err)
	}
	res.AuthPath = authPath
	if st, stErr := llm.InspectCodexAuth(authPath, false); stErr == nil && st.AccountID != "" {
		res.Account = "ChatGPT account " + st.AccountID
	}
	res.Added, res.ConfigErr = llm.ApplyCodexLoginToConfig(ctx, cfg, prov.Name, authPath, prov.Proxy)
	return nil
}

func runDevin(ctx context.Context, cfg *config.Config, prov *config.ProviderConfig, h Hooks, res *Result) error {
	authPath, err := authPathFor(config.DevinAuthPath(cfg.Paths.Home, prov.Name), prov.Name)
	if err != nil {
		return err
	}
	client, err := llm.HTTPClientForProviderProxy(prov.Proxy)
	if err != nil {
		return err
	}
	account, err := llm.DevinSignIn(ctx, client, authPath, llm.DevinSignInOptions{
		OnPrompt: func(p llm.DevinLoginPrompt) {
			h.prompt(Prompt{URL: p.AuthURL, Pasteable: true})
		},
		Paste:           h.Paste,
		OnPasteRejected: h.OnPasteRejected,
	})
	if err != nil {
		return fmt.Errorf("devin sign-in: %w", err)
	}
	res.AuthPath = authPath
	switch {
	case account.Name != "" && account.Email != "" && account.Name != account.Email:
		res.Account = account.Name + " <" + account.Email + ">"
	case account.Email != "":
		res.Account = account.Email
	default:
		res.Account = account.Name
	}
	res.Added, res.ConfigErr = llm.ApplyDevinLoginToConfig(ctx, cfg, prov.Name, "", authPath, prov.Proxy)
	return nil
}

func runOpenCode(ctx context.Context, cfg *config.Config, prov *config.ProviderConfig, h Hooks, res *Result) error {
	endpoint, err := OpenCodeAPIBase("", prov.APIBase)
	if err != nil {
		return err
	}
	authPath, err := authPathFor(config.OpenCodeAuthPath(cfg.Paths.Home, prov.Name), prov.Name)
	if err != nil {
		return err
	}
	login, err := llm.StartOpenCodeDeviceLogin(ctx, prov.Proxy)
	if err != nil {
		return fmt.Errorf("opencode sign-in: %w", err)
	}
	h.prompt(Prompt{URL: login.URL, Code: login.UserCode})
	if err := login.Wait(ctx, authPath); err != nil {
		return fmt.Errorf("opencode sign-in: %w", err)
	}
	res.AuthPath = authPath
	if st, stErr := llm.InspectOpenCodeAuth(authPath); stErr == nil {
		res.Account = st.Organization
	}
	added, cfgErr := llm.ApplyOpenCodeLoginToConfig(ctx, cfg, prov.Name, endpoint, "", prov.Proxy)
	if cfgErr != nil {
		cfgErr = errors.New(llm.RedactOpenCodeSecrets(cfgErr.Error()))
	}
	res.Added, res.ConfigErr = added, cfgErr
	return nil
}
