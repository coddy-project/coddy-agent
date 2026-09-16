package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
)

// Permission mode constants for tools.permission_mode.
const (
	// PermModeAsk asks for user approval before each shell command and each file write.
	PermModeAsk = "ask"
	// PermModeAcceptEdits auto-approves file writes but still asks for shell commands.
	PermModeAcceptEdits = "accept_edits"
	// PermModeBypass skips all permission prompts (use only in fully trusted environments).
	PermModeBypass = "bypass"
)

// Tools is the YAML tools section (key tools).
type Tools struct {
	// PermissionMode controls when the agent asks for user approval before running tools.
	// Values: "ask" (default), "accept_edits", "bypass".
	PermissionMode   string   `yaml:"permission_mode"`
	CommandAllowlist []string `yaml:"command_allowlist"`

	// SSHConnectTimeout is the TCP dial timeout for SSH connections in seconds (default: 30).
	SSHConnectTimeout int `yaml:"ssh_connect_timeout"`

	// OutputLimits caps how many lines each tool result or error may return into
	// the LLM context. Enabled limits also activate the tooling-layer hard byte
	// safety ceiling so a huge single line cannot bypass them.
	OutputLimits ToolOutputLimits `yaml:"output_limits"`

	// Background bounds commands the agent runs as background tasks.
	Background ToolBackground `yaml:"background"`

	// WebSearch picks the search engines the websearch tool asks, and bounds
	// how long it may wait for them.
	WebSearch ToolWebSearch `yaml:"websearch"`

	// HTTPRequest is the policy of the http_request tool: the addresses it may
	// reach without asking the operator.
	HTTPRequest ToolHTTPRequest `yaml:"http_request"`
}

// Search engine names accepted in tools.websearch.engines.
const (
	WebSearchEngineBrave   = "brave"
	WebSearchEngineBing    = "bing"
	WebSearchEngineDDG     = "ddg"
	WebSearchEngineGoogle  = "google"
	WebSearchEngineSearXNG = "searxng"
)

// Defaults for tools.websearch. A blocked engine answers fast - a challenge
// page costs one round trip, not a timeout - so the per-engine budget only has
// to cover a stalled connection, and the total budget is what stops one such
// connection holding the turn.
const (
	WebSearchDefaultEngineTimeoutSeconds = 8
	WebSearchDefaultTotalTimeoutSeconds  = 20
	WebSearchDefaultMaxConcurrent        = 4
	WebSearchDefaultSnippetChars         = 320
	WebSearchDefaultCacheTTLSeconds      = 300
)

// WebSearchBraveAPIKeyEnv is the environment variable the Brave engine reads
// its API key from when tools.websearch.brave_api_key is empty, so the key can
// live in the environment or ${CODDY_HOME}/.env rather than in config.yaml.
const WebSearchBraveAPIKeyEnv = "BRAVE_API_KEY"

// WebSearchDefaultEngines is what the tool asks when the operator named no
// engines: Brave first, Bing behind it. DuckDuckGo and Google are deliberately
// absent - measured from a server, DuckDuckGo answers every query with an
// anti-bot interstitial and Google serves a page whose results are rendered in
// the browser, so asking them by default buys a round trip and a permanent red
// line in the engine report. An operator whose egress they still serve names
// them explicitly.
func WebSearchDefaultEngines() []string {
	return []string{WebSearchEngineBrave, WebSearchEngineBing}
}

// KnownWebSearchEngines lists every backend tools.websearch.engines accepts.
func KnownWebSearchEngines() []string {
	return []string{
		WebSearchEngineBrave, WebSearchEngineBing, WebSearchEngineDDG,
		WebSearchEngineGoogle, WebSearchEngineSearXNG,
	}
}

// ToolWebSearch is the YAML tools.websearch section: which search engines the
// websearch tool asks, in what order their results merge, and what it is
// allowed to spend asking them.
type ToolWebSearch struct {
	// Engines is the backends to ask, in merge order. Unset means
	// WebSearchDefaultEngines; an unknown name is a configuration error rather
	// than a silently skipped engine.
	Engines []string `yaml:"engines"`

	// EngineTimeoutSeconds bounds one backend. 0 uses the default.
	EngineTimeoutSeconds int `yaml:"engine_timeout_seconds"`

	// TotalTimeoutSeconds bounds the whole call, however many engines it asks.
	TotalTimeoutSeconds int `yaml:"total_timeout_seconds"`

	// MaxConcurrentEngines caps how many backends are in flight at once.
	MaxConcurrentEngines int `yaml:"max_concurrent_engines"`

	// SnippetChars caps one result's description. A full paragraph per row,
	// multiplied by fifteen rows, is a measurable share of the context window.
	SnippetChars int `yaml:"snippet_chars"`

	// CacheTTLSeconds is how long an engine's answer to one query is reused.
	// It keeps a model that reaches for the same search twice from asking the
	// engine twice. 0 uses the default; a negative value turns caching off.
	CacheTTLSeconds int `yaml:"cache_ttl_seconds"`

	// SearXNGURL is the base address of the operator's own SearXNG instance,
	// asked over its JSON API. A self-hosted aggregator is the durable answer
	// to every way a scraped engine fails, and it usually listens on localhost
	// or a LAN address - which is why this address is not held to the SSRF
	// rules webfetch applies to a URL the model chose.
	SearXNGURL string `yaml:"searxng_url"`

	// BraveAPIKey routes the Brave backend to the official Search API instead
	// of reading the public result page: a stable contract with no parser to
	// break when Brave redeploys its front end.
	BraveAPIKey string `yaml:"brave_api_key"`
}

// validate rejects negative bounds and unknown engine names, and refuses an
// engine the configuration cannot actually reach.
func (w *ToolWebSearch) validate() error {
	for name, v := range map[string]int{
		"engine_timeout_seconds": w.EngineTimeoutSeconds,
		"total_timeout_seconds":  w.TotalTimeoutSeconds,
		"max_concurrent_engines": w.MaxConcurrentEngines,
		"snippet_chars":          w.SnippetChars,
	} {
		if v < 0 {
			return fmt.Errorf("tools.websearch.%s: must be >= 0", name)
		}
	}
	known := make(map[string]bool, len(KnownWebSearchEngines()))
	for _, e := range KnownWebSearchEngines() {
		known[e] = true
	}
	usesSearXNG := false
	for i, e := range w.Engines {
		e = strings.ToLower(strings.TrimSpace(e))
		w.Engines[i] = e
		if e == "" {
			return fmt.Errorf("tools.websearch.engines[%d]: empty engine name", i)
		}
		if !known[e] {
			return fmt.Errorf("tools.websearch.engines[%d]: unknown engine %q (known: %s)",
				i, e, strings.Join(KnownWebSearchEngines(), ", "))
		}
		if e == WebSearchEngineSearXNG {
			usesSearXNG = true
		}
	}
	if u := strings.TrimSpace(w.SearXNGURL); u != "" {
		if err := validateSearXNGURL(u); err != nil {
			return err
		}
	} else if usesSearXNG {
		return fmt.Errorf("tools.websearch: engine %q needs tools.websearch.searxng_url", WebSearchEngineSearXNG)
	}
	return nil
}

// validateSearXNGURL checks the operator's SearXNG address. Unlike the URL
// webfetch is handed, this one comes from the configuration rather than from
// the model, and the instances it names normally live on localhost or a LAN
// address - so private ranges are allowed on purpose: refusing them would
// refuse exactly the deployments this engine exists for.
//
// What is refused is the link-local range, where no SearXNG listens and where
// the cloud metadata service does. That is the one address at which a value
// typed wrong, or staged by the model through config_set, turns a search into
// a credential read. A hostname is resolved before the check, because a name
// pointing there reaches it just as well as the literal address does.
func validateSearXNGURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("tools.websearch.searxng_url: %w", err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("tools.websearch.searxng_url: scheme must be http or https, got %q", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("tools.websearch.searxng_url: no host in %q", raw)
	}
	host := u.Hostname()
	if ip := net.ParseIP(host); ip != nil {
		if err := checkSearXNGIP(ip); err != nil {
			return fmt.Errorf("tools.websearch.searxng_url: %w", err)
		}
		return nil
	}
	// A name that does not resolve is not a configuration error: an instance
	// that is down, or a name only the run host knows, is a runtime problem
	// the engine reports rather than a reason to refuse the whole file.
	ips, err := net.LookupIP(host)
	if err != nil {
		return nil
	}
	for _, ip := range ips {
		if err := checkSearXNGIP(ip); err != nil {
			return fmt.Errorf("tools.websearch.searxng_url: %q resolves to %w", host, err)
		}
	}
	return nil
}

// checkSearXNGIP refuses the link-local range and nothing else. Loopback and
// private addresses are where a self-hosted instance actually lives.
func checkSearXNGIP(ip net.IP) error {
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return fmt.Errorf("%s, a link-local address where the cloud metadata service lives, not a search instance", ip)
	}
	return nil
}

// Defaults for tools.background.
const (
	BackgroundDefaultMaxConcurrent     = 5
	BackgroundDefaultTimeoutSeconds    = 900
	BackgroundDefaultMaxTimeoutSeconds = 3600
	BackgroundDefaultOutputBufferBytes = 262144
)

// ToolBackground is the YAML tools.background section. It governs run_command
// calls that ask to run detached, and the task pool that owns them.
type ToolBackground struct {
	// Enabled exposes the background option on run_command and the background
	// task tools. Unset means enabled; set it to false to keep every command in
	// the foreground.
	Enabled *bool `yaml:"enable"`

	// MaxConcurrent is how many background tasks one session may run at once.
	MaxConcurrent int `yaml:"max_concurrent"`

	// DefaultTimeoutSeconds is the hard limit for a task whose caller gave
	// neither a timeout nor a duration estimate.
	DefaultTimeoutSeconds int `yaml:"default_timeout_seconds"`

	// MaxTimeoutSeconds caps whatever timeout the caller or the estimate asked
	// for, so no single task can outlive the ceiling the operator set.
	MaxTimeoutSeconds int `yaml:"max_timeout_seconds"`

	// OutputBufferBytes is how much of each task's output stays in memory for
	// the status ticker. The full log is still written to the session bundle.
	OutputBufferBytes int `yaml:"output_buffer_bytes"`
}

// ResolvedEnabled reports whether background execution is offered, defaulting to
// true when the field is unset.
func (b *ToolBackground) ResolvedEnabled() bool {
	if b == nil || b.Enabled == nil {
		return true
	}
	return *b.Enabled
}

// Resolved returns the section with every unset knob replaced by its default.
func (b *ToolBackground) Resolved() ToolBackground {
	out := ToolBackground{}
	if b != nil {
		out = *b
	}
	if out.MaxConcurrent <= 0 {
		out.MaxConcurrent = BackgroundDefaultMaxConcurrent
	}
	if out.DefaultTimeoutSeconds <= 0 {
		out.DefaultTimeoutSeconds = BackgroundDefaultTimeoutSeconds
	}
	if out.MaxTimeoutSeconds <= 0 {
		out.MaxTimeoutSeconds = BackgroundDefaultMaxTimeoutSeconds
	}
	if out.DefaultTimeoutSeconds > out.MaxTimeoutSeconds {
		out.DefaultTimeoutSeconds = out.MaxTimeoutSeconds
	}
	if out.OutputBufferBytes <= 0 {
		out.OutputBufferBytes = BackgroundDefaultOutputBufferBytes
	}
	return out
}

// validate rejects negative knobs; zero keeps meaning "use the default".
func (b *ToolBackground) validate() error {
	for name, v := range map[string]int{
		"max_concurrent":          b.MaxConcurrent,
		"default_timeout_seconds": b.DefaultTimeoutSeconds,
		"max_timeout_seconds":     b.MaxTimeoutSeconds,
		"output_buffer_bytes":     b.OutputBufferBytes,
	} {
		if v < 0 {
			return fmt.Errorf("tools.background.%s: must be >= 0", name)
		}
	}
	return nil
}

// Defaults for tools.output_limits (maximum lines a tool result or error may
// contribute to the LLM context; 0 disables both line and byte limits). Derived
// empirically from typical per-line token density.
const (
	OutputLimitDefaultRead          = 1000
	OutputLimitDefaultGrep          = 200
	OutputLimitDefaultGlob          = 300
	OutputLimitDefaultPrintTree     = 400
	OutputLimitDefaultRunCommand    = 500
	OutputLimitDefaultSSHRunCommand = 500
	OutputLimitDefaultWebFetch      = 800
	OutputLimitDefaultWebSearch     = 200
	OutputLimitDefaultDefault       = 1000
)

// ToolOutputLimits is the YAML tools.output_limits section: per-tool line
// ceilings for results and errors. Any positive limit also activates the shared
// hard byte ceiling. Pointer fields preserve unset versus explicit zero.
type ToolOutputLimits struct {
	Read          *int `yaml:"read"`
	Grep          *int `yaml:"grep"`
	Glob          *int `yaml:"glob"`
	PrintTree     *int `yaml:"print_tree"`
	RunCommand    *int `yaml:"run_command"`
	SSHRunCommand *int `yaml:"ssh_run_command"`
	WebFetch      *int `yaml:"webfetch"`
	WebSearch     *int `yaml:"websearch"`
	// Default applies to any tool not named above, including MCP tools.
	Default *int `yaml:"default"`
}

// outputLimitDefaultKey is the map key used for the Default limit (empty tool name).
const outputLimitDefaultKey = ""

// MaxLines returns the effective line ceiling for a tool, applying the built-in
// default when the field is unset. 0 disables both line and byte limits.
func (l *ToolOutputLimits) MaxLines(tool string) int {
	if l == nil {
		return 0
	}
	pick := func(p *int, def int) int {
		if p != nil {
			return *p
		}
		return def
	}
	switch tool {
	case "read":
		return pick(l.Read, OutputLimitDefaultRead)
	case "grep":
		return pick(l.Grep, OutputLimitDefaultGrep)
	case "glob":
		return pick(l.Glob, OutputLimitDefaultGlob)
	case "print_tree":
		return pick(l.PrintTree, OutputLimitDefaultPrintTree)
	case "run_command":
		return pick(l.RunCommand, OutputLimitDefaultRunCommand)
	case "ssh_run_command":
		return pick(l.SSHRunCommand, OutputLimitDefaultSSHRunCommand)
	case "webfetch":
		return pick(l.WebFetch, OutputLimitDefaultWebFetch)
	case "websearch":
		return pick(l.WebSearch, OutputLimitDefaultWebSearch)
	default:
		return pick(l.Default, OutputLimitDefaultDefault)
	}
}

// AsMap materializes the effective per-tool ceilings for the tool execution layer.
// The empty-string key carries the Default limit for unlisted (and MCP) tools.
func (l *ToolOutputLimits) AsMap() map[string]int {
	names := []string{
		"read", "grep", "glob", "print_tree",
		"run_command", "ssh_run_command", "webfetch", "websearch",
	}
	out := make(map[string]int, len(names)+1)
	for _, n := range names {
		out[n] = l.MaxLines(n)
	}
	out[outputLimitDefaultKey] = l.MaxLines(outputLimitDefaultKey)
	return out
}

// validate checks that no explicitly set limit is negative.
func (l *ToolOutputLimits) validate() error {
	for name, p := range map[string]*int{
		"read": l.Read, "grep": l.Grep, "glob": l.Glob, "print_tree": l.PrintTree,
		"run_command": l.RunCommand, "ssh_run_command": l.SSHRunCommand,
		"webfetch": l.WebFetch, "websearch": l.WebSearch, "default": l.Default,
	} {
		if p != nil && *p < 0 {
			return fmt.Errorf("tools.output_limits.%s: must be >= 0", name)
		}
	}
	return nil
}

// ResolvedPermMode returns PermissionMode with a safe default of PermModeAsk.
func (c *Tools) ResolvedPermMode() string {
	switch c.PermissionMode {
	case PermModeAsk, PermModeAcceptEdits, PermModeBypass:
		return c.PermissionMode
	default:
		return PermModeAsk
	}
}

// Validate trims allowlist entries in place and normalises PermissionMode.
func (c *Tools) Validate() error {
	if c.PermissionMode == "" {
		c.PermissionMode = PermModeAsk
	}
	for i := range c.CommandAllowlist {
		c.CommandAllowlist[i] = strings.TrimSpace(c.CommandAllowlist[i])
	}
	if c.SSHConnectTimeout <= 0 {
		c.SSHConnectTimeout = 30
	}
	if err := c.OutputLimits.validate(); err != nil {
		return err
	}
	if err := c.Background.validate(); err != nil {
		return err
	}
	if err := c.HTTPRequest.validate(); err != nil {
		return err
	}
	return c.WebSearch.validate()
}

// ResolvedEngines returns the configured engine list with blanks and repeats
// removed, or the default set when the operator named none.
func (w *ToolWebSearch) ResolvedEngines() []string {
	if w == nil {
		return WebSearchDefaultEngines()
	}
	out := make([]string, 0, len(w.Engines))
	seen := make(map[string]bool, len(w.Engines))
	for _, e := range w.Engines {
		e = strings.ToLower(strings.TrimSpace(e))
		if e == "" || seen[e] {
			continue
		}
		seen[e] = true
		out = append(out, e)
	}
	if len(out) == 0 {
		return WebSearchDefaultEngines()
	}
	return out
}

// ToolSettings materializes the section for the tool execution layer, with
// every unset knob replaced by its default. The field order matches
// tooling.WebSearchSettings, which is what lets the tool convert it directly.
func (w *ToolWebSearch) ToolSettings() ToolWebSearchSettings {
	out := ToolWebSearchSettings{
		Engines:              w.ResolvedEngines(),
		EngineTimeoutSeconds: WebSearchDefaultEngineTimeoutSeconds,
		TotalTimeoutSeconds:  WebSearchDefaultTotalTimeoutSeconds,
		MaxConcurrentEngines: WebSearchDefaultMaxConcurrent,
		SnippetChars:         WebSearchDefaultSnippetChars,
		CacheTTLSeconds:      WebSearchDefaultCacheTTLSeconds,
		// Read when the settings are built, like the rest of the section, so a
		// key exported for the process (or set in ${CODDY_HOME}/.env) is used
		// without being written into config.yaml. A configured key wins.
		BraveAPIKey: strings.TrimSpace(os.Getenv(WebSearchBraveAPIKeyEnv)),
	}
	if w == nil {
		return out
	}
	if w.EngineTimeoutSeconds > 0 {
		out.EngineTimeoutSeconds = w.EngineTimeoutSeconds
	}
	if w.TotalTimeoutSeconds > 0 {
		out.TotalTimeoutSeconds = w.TotalTimeoutSeconds
	}
	if w.MaxConcurrentEngines > 0 {
		out.MaxConcurrentEngines = w.MaxConcurrentEngines
	}
	if w.SnippetChars > 0 {
		out.SnippetChars = w.SnippetChars
	}
	if w.CacheTTLSeconds != 0 {
		out.CacheTTLSeconds = w.CacheTTLSeconds
	}
	out.SearXNGURL = strings.TrimSpace(w.SearXNGURL)
	if key := strings.TrimSpace(w.BraveAPIKey); key != "" {
		out.BraveAPIKey = key
	}
	return out
}

// ToolWebSearchSettings is the resolved section handed to the tool layer. It
// mirrors tooling.WebSearchSettings field for field; config does not import
// tooling, so the two are kept in step by TestWebSearchSettingsMirrorTooling.
type ToolWebSearchSettings struct {
	Engines              []string
	EngineTimeoutSeconds int
	TotalTimeoutSeconds  int
	MaxConcurrentEngines int
	SnippetChars         int
	CacheTTLSeconds      int
	SearXNGURL           string
	BraveAPIKey          string
}
