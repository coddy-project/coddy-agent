package config

// The credential rules of shared models: a shared-model token is its own class
// and never doubles as a main-class or swarm token, and the shared routes never
// run without some credential (docs/plans/remote-model-provider.md, 4.5).

import (
	"fmt"
	"os"
	"strings"
)

// The environment variables that carry credentials the file does not. The
// owners of the names are external/httpserver (TokenEnvVar, LoginUserEnvVar,
// LoginPasswordEnvVar) and external/swarm (TokenEnvVar, PairingEnvVar), which
// sit behind build tags this package must not import; the config check reads
// the same variables so that it sees what a start would see.
const (
	envHTTPToken         = "CODDY_HTTP_TOKEN"
	envHTTPUser          = "CODDY_HTTP_USER"
	envHTTPPassword      = "CODDY_HTTP_PASSWORD"
	envSwarmToken        = "CODDY_SWARM_TOKEN"
	envSwarmPairingToken = "CODDY_SWARM_PAIRING_TOKEN"
)

// ExtraTokens are the credentials a process holds that config.yaml does not
// carry, which the loader therefore cannot see: the bearer tokens given with
// --auth-token / CODDY_HTTP_TOKEN, the swarm tokens given with
// --swarm-auth-token / CODDY_SWARM_TOKEN (and the pairing credential of
// --swarm-pairing-token / CODDY_SWARM_PAIRING_TOKEN), and a web sign-in account
// from CODDY_HTTP_USER and CODDY_HTTP_PASSWORD.
type ExtraTokens struct {
	// HTTP are main-class tokens: they open the whole API.
	HTTP []string
	// Swarm are the tokens of the swarm plane.
	Swarm []string
	// Login reports a web sign-in account supplied out of band.
	Login bool
}

// ExtraTokensFromEnv reads the credential variables of the process
// environment. A flag value is added by the caller that parsed the flags.
func ExtraTokensFromEnv() ExtraTokens {
	var out ExtraTokens
	if t := strings.TrimSpace(os.Getenv(envHTTPToken)); t != "" {
		out.HTTP = append(out.HTTP, t)
	}
	for _, name := range []string{envSwarmToken, envSwarmPairingToken} {
		if t := strings.TrimSpace(os.Getenv(name)); t != "" {
			out.Swarm = append(out.Swarm, t)
		}
	}
	out.Login = strings.TrimSpace(os.Getenv(envHTTPUser)) != "" && os.Getenv(envHTTPPassword) != ""
	return out
}

// merge returns the union of two sets of out-of-band credentials.
func (e ExtraTokens) merge(o ExtraTokens) ExtraTokens {
	return ExtraTokens{
		HTTP:  append(append([]string(nil), e.HTTP...), o.HTTP...),
		Swarm: append(append([]string(nil), e.Swarm...), o.Swarm...),
		Login: e.Login || o.Login,
	}
}

// namedToken is a credential and the key it came from, for a message that must
// name the key and never the value.
type namedToken struct {
	value string
	name  string
}

// CheckSharedTokenClasses refuses a configuration in which a shared-model token
// is also a token of another class. A shared-model token opens only the three
// LLM routes; the main token (httpserver.auth_token, --auth-token,
// CODDY_HTTP_TOKEN) opens the whole API and the swarm tokens (swarm.auth_token,
// swarm.pairing_tokens, a joined relay's pairing_token, --swarm-auth-token,
// CODDY_SWARM_TOKEN) the relay: a token of two classes would grant the wider
// one to whoever was only meant to borrow a model.
//
// swarm.join[].token and swarm.upstreams[].token are not a class of their own:
// they are the credential a node hands to a relay, or a relay presents to a
// node, and a node that shares models through a relay is meant to use its
// shared-model token there.
//
// extra holds what the file cannot show (the flags, the environment). The
// error is a *SharedTokenClassError: it names both keys and never a value.
func CheckSharedTokenClasses(cfg *Config, extra ExtraTokens) error {
	if cfg == nil || (len(cfg.HTTPServer.SharedModels.Tokens) == 0 && len(cfg.Swarm.Clients) == 0) {
		return nil
	}
	var others []namedToken
	add := func(value, name string) {
		if value = strings.TrimSpace(value); value != "" {
			others = append(others, namedToken{value, name})
		}
	}
	add(cfg.HTTPServer.AuthToken, "httpserver.auth_token")
	for _, t := range extra.HTTP {
		add(t, "the token given with --auth-token / CODDY_HTTP_TOKEN")
	}
	add(cfg.Swarm.AuthToken, "swarm.auth_token")
	for i, t := range cfg.Swarm.PairingTokens {
		add(t, fmt.Sprintf("swarm.pairing_tokens[%d]", i))
	}
	for i, j := range cfg.Swarm.Join {
		add(j.PairingToken, fmt.Sprintf("swarm.join[%d].pairing_token", i))
	}
	for _, t := range extra.Swarm {
		add(t, "a swarm token given with --swarm-auth-token / --swarm-pairing-token or CODDY_SWARM_TOKEN / CODDY_SWARM_PAIRING_TOKEN")
	}
	owners := make(map[string]string, len(others))
	for _, o := range others {
		if _, seen := owners[o.value]; !seen {
			owners[o.value] = o.name
		}
	}
	for i, t := range cfg.HTTPServer.SharedModels.Tokens {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if name, clash := owners[t]; clash {
			return &SharedTokenClassError{Index: i, Other: name}
		}
	}
	// A scoped relay client's token is a class of its own as well: it opens the
	// shared-model routes of chosen nodes through the relay and nothing else.
	// It must differ from every other class and from every other scoped token.
	for i, t := range cfg.HTTPServer.SharedModels.Tokens {
		if t = strings.TrimSpace(t); t != "" {
			owners[t] = fmt.Sprintf("httpserver.shared_models.tokens[%d]", i)
		}
	}
	for i, c := range cfg.Swarm.Clients {
		t := strings.TrimSpace(c.Token)
		if t == "" {
			continue
		}
		if name, clash := owners[t]; clash {
			return &SharedTokenClassError{Where: fmt.Sprintf("swarm.clients[%d].token", i), Other: name}
		}
		owners[t] = fmt.Sprintf("swarm.clients[%d].token", i)
	}
	return nil
}

// SharedTokenClassError says that a shared-model token is also a token of
// another class.
type SharedTokenClassError struct {
	// Index is the position of the offending entry in httpserver.shared_models.tokens.
	Index int
	// Where, when set, is the config path of the offending token instead of
	// httpserver.shared_models.tokens[Index] (a scoped relay client's token).
	Where string
	// Other names the key (or flag) that holds the same token. Never the value.
	Other string
}

// Path is the config path of the offending entry.
func (e *SharedTokenClassError) Path() string {
	if e.Where != "" {
		return e.Where
	}
	return fmt.Sprintf("httpserver.shared_models.tokens[%d]", e.Index)
}

// Error describes the clash.
func (e *SharedTokenClassError) Error() string {
	if e.Where != "" {
		return e.Path() + " is the same token as " + e.Other + ": a token belongs to one class, and a scoped client must open only the shared-model routes; give each its own"
	}
	return e.Path() + " is the same token as " + e.Other + ": a token belongs to one class, and a shared-model token must open only the LLM routes; give each its own"
}

// SharedModelsAuthError says that models are shared while no credential
// protects the routes that serve them.
type SharedModelsAuthError struct {
	// Model is the models[].model selector of the first shared row.
	Model string
}

// Path is the config path of the first shared_as, which a report places the
// finding at.
func (e *SharedModelsAuthError) Path() string {
	return "models[" + e.Model + "].shared_as"
}

// Error describes the problem.
func (e *SharedModelsAuthError) Error() string {
	return e.Path() + ": the shared-model routes need authentication, and no credential is configured: " +
		"no httpserver.auth_token, --auth-token / CODDY_HTTP_TOKEN, httpserver.shared_models.tokens or web sign-in, " +
		"so anyone who reaches the port could run this model on your account"
}

// Fix says how to resolve it.
func (e *SharedModelsAuthError) Fix() string {
	return "set httpserver.shared_models.tokens (a token that opens only the LLM routes; use a ${ENV} reference) or httpserver.auth_token; " +
		"httpserver.allow_insecure: true is for a node that already publishes its whole API without authentication"
}

// SharedModelsAuthProblem reports an error when a model is shared (shared_as)
// and no credential of any class protects the node: no main token, no web
// sign-in, no shared-model token and nothing out of band (extra), unless
// httpserver.allow_insecure is set. The shared routes need a credential
// whatever the listen address is, since a loopback listener behind a TLS proxy,
// or a node that joined a relay by tunnel, is as exposed as any other.
//
// A nil result means nothing is shared, a credential exists, or the operator
// accepted an open API.
func SharedModelsAuthProblem(cfg *Config, extra ExtraTokens) error {
	if cfg == nil {
		return nil
	}
	shared := cfg.SharedModelEntries()
	if len(shared) == 0 || cfg.HTTPServer.AllowInsecure {
		return nil
	}
	if hasHTTPCredential(&cfg.HTTPServer, extra) {
		return nil
	}
	return &SharedModelsAuthError{Model: shared[0].Model}
}

// hasHTTPCredential reports whether anything closes the API gate: a main token,
// a shared-model token, or the web sign-in form. A certificate is not a credential: TLS admits a peer at the
// handshake and the application reads no identity out of it.
func hasHTTPCredential(h *HTTPServerConfig, extra ExtraTokens) bool {
	return hasMainCredential(h, extra) || len(h.EffectiveSharedTokens()) > 0
}

// hasMainCredential is hasHTTPCredential without the shared-model tokens: what
// opens the whole API.
func hasMainCredential(h *HTTPServerConfig, extra ExtraTokens) bool {
	if len(h.EffectiveAuthTokens()) > 0 {
		return true
	}
	for _, t := range extra.HTTP {
		if strings.TrimSpace(t) != "" {
			return true
		}
	}
	return loginConfigured(h.Login, extra.Login)
}

// loginConfigured mirrors the sign-in policy of the HTTP server: an explicit
// enable: false wins over everything, otherwise an account (from the file or
// the environment) or an explicit enable: true - which fails closed without an
// account - puts the gate up.
func loginConfigured(l HTTPLoginConfig, envAccount bool) bool {
	if l.IsExplicitlyDisabled() {
		return false
	}
	return envAccount || l.HasAccount() || l.IsExplicitlyEnabled()
}
