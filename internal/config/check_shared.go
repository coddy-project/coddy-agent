package config

// The part of the -t / --test-config check that concerns shared models: what the
// loader cannot say because it needs the flags and the environment, or because
// it is a warning rather than a refusal.

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// sharedMaxCallWarnAfter is the max_call_ms above which a blocking shared model
// whose provider has no timeout_ms is worth a warning: the default, thirty
// minutes, is the bound the check trusts.
const sharedMaxCallWarnAfter = SharedModelsDefaultMaxCallMS * time.Millisecond

// sharedModelFindings reports the problems of shared models the loader does not
// refuse on its own: a token of two classes that only the flags and the
// environment reveal, shared models with no credential in front of them, and a
// list of warnings. loadErr is what the loader already reported, so the same
// complaint is not made twice.
func sharedModelFindings(cfg *Config, body *yaml.Node, extra ExtraTokens, loadErr error) []Finding {
	var out []Finding
	at := func(sev Severity, path, msg, fix string) {
		out = append(out, locatedFinding(body, sev, path, msg, fix))
	}

	if err := CheckSharedTokenClasses(cfg, extra); err != nil && (loadErr == nil || err.Error() != loadErr.Error()) {
		if clash, ok := err.(*SharedTokenClassError); ok {
			at(SeverityError, clash.Path(), strings.TrimPrefix(clash.Error(), clash.Path()+" "),
				"give the shared-model token a value of its own: a token that opens the whole API must never be handed to a borrower of a model")
		}
	}
	if err := SharedModelsAuthProblem(cfg, extra); err != nil {
		if problem, ok := err.(*SharedModelsAuthError); ok {
			at(SeverityError, problem.Path(), strings.TrimPrefix(problem.Error(), problem.Path()+": "), problem.Fix())
		}
	}

	for i := range cfg.Providers {
		p := &cfg.Providers[i]
		name, typ := strings.TrimSpace(p.Name), strings.TrimSpace(p.Type)
		if typ == "coddy" {
			if cleartextRemote(p.APIBase) {
				at(SeverityWarning, "providers["+name+"].api_base",
					"the address is plain http:// and its host is not loopback: the token and the whole conversation, file contents and tool output included, travel in clear text",
					"use https://, or reach the remote through a TLS reverse proxy or an https swarm relay mount")
			}
			continue
		}
		if p.BusyWaitMS > 0 {
			at(SeverityWarning, "providers["+name+"].busy_wait_ms",
				"busy_wait_ms has no effect on a provider of type "+typ+": only type coddy waits for a free slot of a remote",
				"remove busy_wait_ms from this provider")
		}
	}

	if hasCoddyProvider(cfg) {
		idle := cfg.Agent.EffectiveLLMStreamIdleTimeout()
		switch {
		case idle == 0:
			at(SeverityWarning, "agent.llm_stream_idle_timeout_ms",
				"0 switches off the liveness guard of the coddy providers: a remote that died is not noticed and the call waits until you press Stop",
				fmt.Sprintf("leave the key out (300000), or set it to at least %d: the remote sends a heartbeat every %d s", 2*SharedModelHeartbeat/time.Millisecond, SharedModelHeartbeat/time.Second))
		case idle < 2*SharedModelHeartbeat:
			at(SeverityWarning, "agent.llm_stream_idle_timeout_ms",
				fmt.Sprintf("%d ms is below twice the remote's %d s heartbeat: one late heartbeat would cut a live remote as stalled", idle/time.Millisecond, SharedModelHeartbeat/time.Second),
				fmt.Sprintf("set it to at least %d, or leave the key out (300000)", 2*SharedModelHeartbeat/time.Millisecond))
		}
	}

	if maxCall := cfg.HTTPServer.EffectiveSharedMaxCall(); maxCall > sharedMaxCallWarnAfter {
		for _, m := range cfg.SharedModelEntries() {
			prov := cfg.FindProvider(m.ProviderName())
			if m.EffectiveStream() || prov == nil || prov.TimeoutMS > 0 {
				continue
			}
			at(SeverityWarning, "models["+m.Model+"].stream",
				fmt.Sprintf("a shared model that does not stream, on provider %q with no timeout_ms, can hold a slot for as long as httpserver.shared_models.max_call_ms allows (%d ms)", prov.Name, maxCall/time.Millisecond),
				"set timeout_ms on the provider above the longest legitimate completion, or lower httpserver.shared_models.max_call_ms")
		}
	}

	out = append(out, blankSharedTokenFindings(body)...)
	out = append(out, swarmClientFindings(cfg, body, extra)...)
	out = append(out, swarmJoinLabelFindings(cfg, body)...)
	if h := cfg.HTTPServer.SharedModels; h.RateBurst > 0 && h.RatePerMinute == 0 {
		at(SeverityWarning, "httpserver.shared_models.rate_burst",
			"rate_burst has no effect without rate_per_minute", "set rate_per_minute, or remove rate_burst")
	}

	if len(cfg.HTTPServer.EffectiveSharedTokens()) > 0 && !hasMainCredential(&cfg.HTTPServer, extra) {
		at(SeverityWarning, "httpserver.shared_models.tokens",
			"the shared-model tokens are the only credential: they open GET /coddy/llm/models, GET /coddy/llm/models/{alias}/usage and POST /coddy/llm/completions, every other API route is closed to every caller and the web UI cannot sign in",
			"to administer this node over its API or its web UI as well, add httpserver.auth_token (or --auth-token / CODDY_HTTP_TOKEN) or httpserver.login")
	}
	return out
}

// hasCoddyProvider reports whether any provider row is of type coddy.
func hasCoddyProvider(cfg *Config) bool {
	for i := range cfg.Providers {
		if strings.TrimSpace(cfg.Providers[i].Type) == "coddy" {
			return true
		}
	}
	return false
}

// cleartextRemote reports an api_base that is plain http to a host that is not
// this machine.
func cleartextRemote(apiBase string) bool {
	u, err := url.Parse(strings.TrimSpace(apiBase))
	if err != nil || u.Scheme != "http" || u.Host == "" {
		return false
	}
	host := u.Hostname()
	return !isLoopbackHostname(host) && !isLoopbackOriginHost(host)
}

// blankSharedTokenFindings warns about every entry of httpserver.shared_models.tokens
// that is empty once the file is expanded: an ${ENV} reference to an unset
// variable, in practice. Such an entry is ignored, so the operator believes in
// a token that does not exist. The document is read rather than the loaded
// configuration because the decoder drops a null entry.
func blankSharedTokenFindings(body *yaml.Node) []Finding {
	node := body
	for _, key := range []string{"httpserver", "shared_models", "tokens"} {
		k := mappingKey(node, key)
		if k == nil {
			return nil
		}
		node = resolveAlias(mappingValueAfterKey(resolveAlias(node), k))
	}
	if node == nil || node.Kind != yaml.SequenceNode {
		return nil
	}
	var out []Finding
	for i, item := range node.Content {
		item = resolveAlias(item)
		if item == nil || item.Kind != yaml.ScalarNode {
			continue
		}
		if item.ShortTag() != "!!null" && strings.TrimSpace(item.Value) != "" {
			continue
		}
		path := fmt.Sprintf("httpserver.shared_models.tokens[%d]", i)
		f := Finding{
			Severity: SeverityWarning, Line: item.Line, Column: item.Column, Path: path,
			Message: "the token is empty (an ${ENV} reference to an unset variable?) and is ignored",
			Fix:     "set the variable, or remove the entry",
		}
		if root, err := loadSchema(); err == nil {
			f.Doc = root.lookup("httpserver.shared_models.tokens").doc()
		}
		out = append(out, f)
	}
	return out
}

// locatedFinding builds a finding at the place path has in the document, with
// the schema's description of the key as its doc line.
func locatedFinding(body *yaml.Node, sev Severity, path, msg, fix string) Finding {
	f := Finding{Severity: sev, Path: path, Message: msg, Fix: fix}
	if n := locatePath(body, path, true); n != nil {
		f.Line, f.Column = n.Line, n.Column
	}
	if root, err := loadSchema(); err == nil {
		f.Doc = root.lookup(selectorRE.ReplaceAllString(path, "")).doc()
	}
	return f
}

// swarmClientFindings reports the warnings of swarm.clients that the loader
// does not refuse: an entry nobody can authenticate as, a burst with no rate,
// and scoped clients on a relay with no full token (whose own routes would then
// be open or reachable only with a token generated per run).
func swarmClientFindings(cfg *Config, body *yaml.Node, extra ExtraTokens) []Finding {
	if len(cfg.Swarm.Clients) == 0 {
		return nil
	}
	var out []Finding
	at := func(path, msg, fix string) {
		out = append(out, locatedFinding(body, SeverityWarning, path, msg, fix))
	}
	for i, c := range cfg.Swarm.Clients {
		base := fmt.Sprintf("swarm.clients[%d]", i)
		if !c.HasCredential() {
			at(base+".token",
				"the token is empty (an ${ENV} reference to an unset variable?) and the entry has no cert_names: nothing can authenticate as this client, so it is ignored",
				"set the variable, add cert_names, or remove the entry")
		}
		if len(c.CertNames) > 0 && strings.TrimSpace(cfg.Swarm.TLS.ClientCAFile) == "" {
			at(base+".cert_names",
				"cert_names have no effect without swarm.tls.client_ca_file: the relay verifies no client certificate, so none of these names can match",
				"set swarm.tls.client_ca_file (and swarm.tls.cert_file and key_file), or remove cert_names")
		}
		if c.RateBurst > 0 && c.RatePerMinute == 0 {
			at(base+".rate_burst", "rate_burst has no effect without rate_per_minute", "set rate_per_minute, or remove rate_burst")
		}
	}
	if len(cfg.Swarm.EffectiveClientTokens(extra)) == 0 {
		at("swarm.clients",
			"no swarm.auth_token, --swarm-auth-token or CODDY_SWARM_TOKEN: the relay's own routes (node list, sessions, topology, settings) are then open, or reachable only with a token generated for each run, while the scoped clients below are configured",
			"set swarm.auth_token (a ${ENV} reference) for the full class")
	}
	return out
}

// swarmJoinLabelFindings warns about a hand-written coddy.token_class label on a
// join whose token is not a shared-model token: the relay would stop listing
// this node's sessions although its token is a main one.
func swarmJoinLabelFindings(cfg *Config, body *yaml.Node) []Finding {
	shared := map[string]bool{}
	for _, t := range cfg.HTTPServer.EffectiveSharedTokens() {
		shared[t] = true
	}
	var out []Finding
	for i, j := range cfg.Swarm.Join {
		if j.Labels[LabelTokenClass] != TokenClassSharedModels || shared[strings.TrimSpace(j.Token)] {
			continue
		}
		out = append(out, locatedFinding(body, SeverityWarning, fmt.Sprintf("swarm.join[%d].labels", i),
			LabelTokenClass+": "+TokenClassSharedModels+" tells the relay that this token opens only the shared-model routes, and the relay will not list this node's sessions, but the token of this join is not one of httpserver.shared_models.tokens",
			"remove the label (a current node sets it itself when the token is a shared-model token), or join with a shared-model token"))
	}
	return out
}
