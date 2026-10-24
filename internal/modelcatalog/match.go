package modelcatalog

import (
	"net"
	"net/url"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// knownHosts names the catalog providers whose entries carry no api field by
// the base URL their OpenAI-compatible endpoint answers at, in the spelling
// apiKey gives it. A provider is looked up only when the catalog has it.
var knownHosts = map[string]string{
	"api.openai.com":              "openai",
	"api.anthropic.com":           "anthropic",
	"api.groq.com/openai":         "groq",
	"api.mistral.ai":              "mistral",
	"api.x.ai":                    "xai",
	"api.together.xyz":            "togetherai",
	"api.deepinfra.com/v1/openai": "deepinfra",
	"api.cerebras.ai":             "cerebras",
	"generativelanguage.googleapis.com/v1beta/openai": "google",
	"api.perplexity.ai":           "perplexity",
	"api.cohere.ai/compatibility": "cohere",
}

// ProviderFor names the catalog provider that serves the models of a provider
// row of type providerType at apiBase, "" when none is known for sure: a row
// whose type the catalog does not carry, a base nothing in the catalog names,
// or one two catalog providers name alike. A wrong window is worse than none,
// so there is no guessing past that.
func (idx Index) ProviderFor(providerType, apiBase string) string {
	id := providerFor(idx, strings.ToLower(strings.TrimSpace(providerType)), strings.TrimSpace(apiBase))
	if _, ok := idx[id]; !ok {
		return ""
	}
	return id
}

func providerFor(idx Index, providerType, apiBase string) string {
	switch providerType {
	case "openai":
		if apiBase == "" {
			return "openai"
		}
		return idx.byBase(apiBase)
	case "anthropic":
		if apiBase == "" {
			return "anthropic"
		}
		return idx.byBase(apiBase)
	case "codex":
		// The Codex backend serves OpenAI's models under their own ids.
		return "openai"
	case "opencode":
		// Coddy sends an opencode row to the Go subscription unless its
		// api_base names the pay-as-you-go Console; the catalog keeps the
		// two apart because their model lists differ.
		if key := apiKey(apiBase); key != "" && key != apiKey(llm.OpenCodeDefaultAPIBase()) {
			for _, known := range llm.OpenCodeAPIBases() {
				if key == apiKey(known) {
					return "opencode"
				}
			}
		}
		return "opencode-go"
	default:
		// neuraldeep, devin and acp: the catalog does not carry them, and
		// the first two report their windows themselves.
		return ""
	}
}

// byBase finds the catalog provider whose endpoint is apiBase.
func (idx Index) byBase(apiBase string) string {
	key := apiKey(apiBase)
	if key == "" {
		return ""
	}
	if id, ok := knownHosts[key]; ok {
		return id
	}
	match := ""
	for id, p := range idx {
		if !matchable(p.API) || apiKey(p.API) != key {
			continue
		}
		if match != "" {
			// Two providers behind one endpoint: no telling which one's
			// limits the row gets.
			return ""
		}
		match = id
	}
	return match
}

// matchable reports whether a catalog api names one public endpoint: not a
// template ("https://${HOST}/v1") and not this machine or a private network,
// where a catalog entry describes somebody's local server rather than the one
// a row points at (LM Studio's http://127.0.0.1:1234/v1 would hand its limits
// to any local server on that port).
func matchable(api string) bool {
	if api == "" || strings.ContainsAny(api, "${}") {
		return false
	}
	u, err := url.Parse(api)
	if err != nil {
		return false
	}
	host := asciiLower(u.Hostname())
	if host == "" || host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") {
		return false
	}
	if ip := net.ParseIP(host); ip != nil {
		return !ip.IsLoopback() && !ip.IsPrivate() && !ip.IsLinkLocalUnicast() && !ip.IsUnspecified()
	}
	return true
}

// apiKey is the one spelling of a base URL two endpoints are compared in:
// the host in lower case without its default port, then the path without a
// trailing slash and without a final /v1 (an Anthropic base names none, the
// catalog's names it). The scheme, credentials, query and fragment do not
// take part. "" for anything that is not an http or https URL with a host.
func apiKey(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}
	scheme := asciiLower(u.Scheme)
	if scheme != "http" && scheme != "https" {
		return ""
	}
	host := asciiLower(u.Hostname())
	if host == "" {
		return ""
	}
	if port := u.Port(); port != "" && port != defaultPorts[scheme] {
		host = net.JoinHostPort(host, port)
	}
	path := strings.TrimRight(u.EscapedPath(), "/")
	path = strings.TrimSuffix(path, "/v1")
	path = strings.TrimRight(path, "/")
	return host + path
}

// defaultPorts are the ports a URL of each scheme names by leaving them out.
var defaultPorts = map[string]string{"http": "80", "https": "443"}

// asciiLower folds ASCII letters only: folding Unicode would let a lookalike
// name another host.
func asciiLower(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'A' && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}, s)
}
