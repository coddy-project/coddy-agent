package config

// The configuration side of models that a Coddy shares with other Coddys
// (docs/plans/remote-model-provider.md): the models[] keys that offer a row
// under an alias, the httpserver.shared_models block (the LLM-only token class
// and the limits of the shared routes), the wait for a free slot of a remote,
// and the cross-checks that keep the token classes apart and the shared routes
// behind a credential.

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
)

// Defaults and bounds of httpserver.shared_models.
const (
	// SharedModelsDefaultMaxStreams is how many shared-model calls one
	// credential may have running at once when max_streams is absent or 0.
	SharedModelsDefaultMaxStreams = 5
	// SharedModelsDefaultMaxCallMS bounds one blocking (stream: false) call of
	// a shared row when max_call_ms is absent: thirty minutes.
	SharedModelsDefaultMaxCallMS = 30 * 60 * 1000
	// SharedModelsMaxCallCeilingMS is the longest max_call_ms an operator may
	// ask for: eight hours. An explicit 0 means this ceiling, never "no bound",
	// so a hung blocking row always releases its slot.
	SharedModelsMaxCallCeilingMS = 8 * 60 * 60 * 1000
	// AgentDefaultSharedBusyWaitMS is how long a call to a `coddy` provider
	// waits for a free slot of the remote when neither providers[].busy_wait_ms
	// nor agent.shared_busy_wait_ms says otherwise.
	AgentDefaultSharedBusyWaitMS = 30000
)

// SharedModelHeartbeat is the longest the remote lets a shared-model response
// stay without a byte: the comment line it writes while the provider is
// silent. The `coddy` provider's liveness guard must be longer than twice this
// (the config check warns otherwise).
const SharedModelHeartbeat = 15 * time.Second

// sharedAliasPattern is the alias of a shared model: no slash, so a local
// selector <provider>/<alias> splits cleanly. The hyphen is escaped so the
// same text works as an HTML pattern attribute (the JavaScript v flag).
const sharedAliasPattern = `^[A-Za-z0-9][A-Za-z0-9._\-]{0,63}$`

// sharedAliasSchemaPattern is the pattern the published schema and the settings
// form carry: the alias, or nothing, since an empty shared_as keeps the row
// private and is not a mistake.
const sharedAliasSchemaPattern = `^$|^[A-Za-z0-9][A-Za-z0-9._\-]{0,63}$`

var sharedAliasRE = regexp.MustCompile(sharedAliasPattern)

// SharedModelsConfig is the YAML httpserver.shared_models section: the
// credentials and the limits of the routes that serve the models[] rows
// carrying shared_as.
type SharedModelsConfig struct {
	// Tokens are the LLM-only credentials: a token listed here opens
	// GET /coddy/llm/models, GET /coddy/llm/models/{alias}/usage and
	// POST /coddy/llm/completions and nothing else. "${ENV}" references are
	// expanded at load. No token may also be a main-class or swarm token
	// (CheckSharedTokenClasses). Write-only in the settings screen.
	Tokens []string `yaml:"tokens,omitempty"`
	// MaxStreams is how many shared-model calls one credential may have running
	// at once. 0 or absent means SharedModelsDefaultMaxStreams; negative is
	// refused.
	MaxStreams int `yaml:"max_streams,omitempty"`
	// MaxCallMS bounds one blocking call of a shared row, in milliseconds. A nil
	// pointer (key omitted) means SharedModelsDefaultMaxCallMS; an explicit 0
	// means the ceiling SharedModelsMaxCallCeilingMS; more than the ceiling or
	// negative is refused.
	MaxCallMS *int `yaml:"max_call_ms"`
	// RatePerMinute bounds the shared-model calls one credential may start per
	// minute, beside the stream slot. 0 or absent means no limit; negative is
	// refused.
	RatePerMinute int `yaml:"rate_per_minute,omitempty"`
	// RateBurst is how many calls may start at once before RatePerMinute
	// applies. 0 or absent means min(rate_per_minute, max_streams), at least 1.
	RateBurst int `yaml:"rate_burst,omitempty"`
}

// Normalize trims the tokens. A blank entry is kept, so a report can point at
// it, and ignored by EffectiveSharedTokens.
func (s *SharedModelsConfig) Normalize() {
	for i := range s.Tokens {
		s.Tokens[i] = strings.TrimSpace(s.Tokens[i])
	}
}

// Validate reports limits the shared routes could not honour.
func (s *SharedModelsConfig) Validate() error {
	if s.MaxStreams < 0 {
		return fmt.Errorf("httpserver.shared_models.max_streams must not be negative")
	}
	if s.RatePerMinute < 0 {
		return fmt.Errorf("httpserver.shared_models.rate_per_minute must not be negative")
	}
	if s.RateBurst < 0 {
		return fmt.Errorf("httpserver.shared_models.rate_burst must not be negative")
	}
	if s.MaxCallMS != nil && (*s.MaxCallMS < 0 || *s.MaxCallMS > SharedModelsMaxCallCeilingMS) {
		return fmt.Errorf("httpserver.shared_models.max_call_ms must be between 0 and %d (8 hours): got %d; 0 means the 8 hour ceiling, not \"no bound\"",
			SharedModelsMaxCallCeilingMS, *s.MaxCallMS)
	}
	return nil
}

// EffectiveSharedTokens returns the usable shared-model tokens: trimmed, blank
// entries (an unset ${ENV} reference expands to one) left out.
func (h *HTTPServerConfig) EffectiveSharedTokens() []string {
	if h == nil {
		return nil
	}
	var out []string
	for _, t := range h.SharedModels.Tokens {
		if t = strings.TrimSpace(t); t != "" {
			out = append(out, t)
		}
	}
	return out
}

// EffectiveSharedMaxStreams returns httpserver.shared_models.max_streams with
// the default applied: how many shared-model calls one credential may run at
// once.
func (h *HTTPServerConfig) EffectiveSharedMaxStreams() int {
	if h == nil || h.SharedModels.MaxStreams <= 0 {
		return SharedModelsDefaultMaxStreams
	}
	return h.SharedModels.MaxStreams
}

// EffectiveSharedRate returns the window limit of one shared-model credential:
// calls per minute (0 means no limit) and the burst, which defaults to the rate
// capped by the stream limit, at least 1.
func (h *HTTPServerConfig) EffectiveSharedRate() (perMinute, burst int) {
	if h == nil || h.SharedModels.RatePerMinute <= 0 {
		return 0, 0
	}
	perMinute = h.SharedModels.RatePerMinute
	if burst = h.SharedModels.RateBurst; burst > 0 {
		return perMinute, burst
	}
	burst = perMinute
	if streams := h.EffectiveSharedMaxStreams(); streams < burst {
		burst = streams
	}
	if burst < 1 {
		burst = 1
	}
	return perMinute, burst
}

// EffectiveSharedMaxCall returns httpserver.shared_models.max_call_ms as a
// duration with the default applied: thirty minutes when the key is absent and
// the eight hour ceiling for an explicit 0.
func (h *HTTPServerConfig) EffectiveSharedMaxCall() time.Duration {
	if h == nil || h.SharedModels.MaxCallMS == nil {
		return SharedModelsDefaultMaxCallMS * time.Millisecond
	}
	ms := *h.SharedModels.MaxCallMS
	if ms == 0 {
		ms = SharedModelsMaxCallCeilingMS
	}
	return time.Duration(ms) * time.Millisecond
}

// SharedAlias returns the alias this row is offered under, or "" when the row
// is private.
func (m *ModelEntry) SharedAlias() string {
	if m == nil {
		return ""
	}
	return strings.TrimSpace(m.SharedAs)
}

// SharedModelEntries lists the models[] rows that are shared, in file order.
func (c *Config) SharedModelEntries() []*ModelEntry {
	if c == nil {
		return nil
	}
	var out []*ModelEntry
	for i := range c.Models {
		if c.Models[i].SharedAlias() != "" {
			out = append(out, &c.Models[i])
		}
	}
	return out
}

// FindSharedModel returns the shared row offered under alias, or nil. The alias
// is the only name that finds a row: a provider/model selector never does.
func (c *Config) FindSharedModel(alias string) *ModelEntry {
	alias = strings.TrimSpace(alias)
	if c == nil || alias == "" {
		return nil
	}
	for i := range c.Models {
		if c.Models[i].SharedAlias() == alias {
			return &c.Models[i]
		}
	}
	return nil
}

// ProviderUsesSubscriptionLogin reports whether the credential behind a
// provider row is a subscription login rather than an API key the operator
// holds for this purpose: provider types codex and devin always, and
// neuraldeep when the row names no key of its own - no api_key, no
// api_key_command and no NAME_API_KEY variable - so that the hub login stored
// by `coddy providers login` fills in. Sharing such a row hands the quota of
// the login to every holder of a shared-model token.
//
// The command of api_key_command is not run: a row that names one has an
// explicit key by the operator's own statement.
func ProviderUsesSubscriptionLogin(p *ProviderConfig) bool {
	if p == nil {
		return false
	}
	switch strings.TrimSpace(p.Type) {
	case "codex", "devin":
		return true
	case "neuraldeep":
		return !providerNamesAKey(p)
	}
	return false
}

// providerNamesAKey reports whether any of the three sources of a request key
// (EffectiveAPIKey: api_key, api_key_command, NAME_API_KEY) is set.
func providerNamesAKey(p *ProviderConfig) bool {
	if strings.TrimSpace(p.APIKey) != "" || strings.TrimSpace(p.APIKeyCommand) != "" {
		return true
	}
	if env := ProviderAPIKeyEnvVarName(p.Name); env != "" {
		return strings.TrimSpace(os.Getenv(env)) != ""
	}
	return false
}

// validateSharing checks the shared_as keys of one models[] row against the
// provider that serves it and the aliases seen so far. selector names the row
// in messages; owners maps an alias to the selector that claimed it.
func (m *ModelEntry) validateSharing(prov *ProviderConfig, owners map[string]string) error {
	alias := m.SharedAlias()
	if alias == "" {
		return nil
	}
	if owner, dup := owners[alias]; dup {
		return fmt.Errorf("models[%s].shared_as: alias %q is already shared by models[%s]; every shared model needs an alias of its own", m.Model, alias, owner)
	}
	owners[alias] = m.Model
	if prov != nil && strings.TrimSpace(prov.Type) == "coddy" {
		return fmt.Errorf("models[%s].shared_as: a model served by a provider of type coddy cannot be shared again: two Coddys would lend each other's lent models", m.Model)
	}
	if ProviderUsesSubscriptionLogin(prov) && !m.SharedSubscriptionAck {
		return fmt.Errorf("models[%s].shared_as: provider %q (type %s) runs on a subscription login; sharing a login hands its quota to every holder of a shared-model token and may breach the vendor's terms of service. Set shared_subscription_ack: true on this model to accept that",
			m.Model, prov.Name, prov.Type)
	}
	return nil
}

// validateSharedAlias is the syntactic half of validateSharing, run with the
// rest of a row's own checks.
func (m *ModelEntry) validateSharedAlias() error {
	alias := m.SharedAlias()
	if alias == "" || sharedAliasRE.MatchString(alias) {
		return nil
	}
	return fmt.Errorf("models[%s].shared_as: %q is not a valid alias: use 1 to 64 letters, digits, dots, underscores or hyphens, starting with a letter or a digit (no slash, so a local model id provider/alias splits cleanly)", m.Model, alias)
}

// BusyWaitBudget resolves how long a call to a `coddy` provider waits for a
// free slot of the remote: the provider's busy_wait_ms when it is above zero,
// otherwise the global agent.shared_busy_wait_ms, where an absent key means
// AgentDefaultSharedBusyWaitMS and an explicit 0 means no waiting.
func BusyWaitBudget(providerMS int, global *int) time.Duration {
	if providerMS > 0 {
		return time.Duration(providerMS) * time.Millisecond
	}
	if global == nil {
		return AgentDefaultSharedBusyWaitMS * time.Millisecond
	}
	return time.Duration(*global) * time.Millisecond
}

// EffectiveSharedBusyWait returns agent.shared_busy_wait_ms as a duration with
// the default applied; an explicit 0 means no waiting.
func (c *Agent) EffectiveSharedBusyWait() time.Duration {
	return BusyWaitBudget(0, c.SharedBusyWaitMS)
}

// EffectiveBusyWait returns the wait budget for a call to the provider named
// name (BusyWaitBudget). A name the configuration does not hold gets the global
// budget.
func (c *Config) EffectiveBusyWait(name string) time.Duration {
	if c == nil {
		return AgentDefaultSharedBusyWaitMS * time.Millisecond
	}
	providerMS := 0
	if prov := c.FindProvider(name); prov != nil {
		providerMS = prov.BusyWaitMS
	}
	return BusyWaitBudget(providerMS, c.Agent.SharedBusyWaitMS)
}
