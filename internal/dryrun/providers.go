package dryrun

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// providerProbes asks every provider for its model list, which exercises the
// address, the credential and the transport in one request, and then checks
// that every model of that provider is one the server names.
func (r *runner) providerProbes() []probe {
	cfg := r.req.Cfg
	byProvider := map[string][]int{}
	for i, m := range cfg.Models {
		name, _, _ := strings.Cut(strings.TrimSpace(m.Model), "/")
		byProvider[name] = append(byProvider[name], i)
	}
	var out []probe
	for i := range cfg.Providers {
		prov := &cfg.Providers[i]
		models := byProvider[prov.Name]
		out = append(out, func(ctx context.Context) []Check { return r.probeProvider(ctx, prov, models) })
	}
	return out
}

func (r *runner) probeProvider(ctx context.Context, prov *config.ProviderConfig, models []int) []Check {
	path := "providers[" + prov.Name + "]"
	base := displayBase(prov)
	var out []Check

	key, kerr := prov.EffectiveAPIKeyContextErr(ctx)
	if kerr != nil {
		out = append(out, r.check(StatusWarning, path, path+".api_key_command",
			"api_key_command did not answer in time; the environment variable is the fallback",
			"make the credential helper answer within a few seconds, or set api_key"))
	}
	if needsKey(prov) && key == "" {
		env := config.ProviderAPIKeyEnvVarName(prov.Name)
		out = append(out, r.check(StatusError, path, path, "no credential for "+base,
			fmt.Sprintf("set api_key or api_key_command, or export %s; a local server needs api_base instead", env)))
		return append(out, r.skipModels(models, "provider "+prov.Name+" has no credential")...)
	}

	in := llm.ProviderInput{
		Name:     prov.Name,
		Type:     prov.Type,
		APIKey:   key,
		BaseURL:  prov.APIBase,
		ProxyURL: prov.Proxy,
		AuthPath: config.ProviderAuthPath(r.req.Paths.Home, prov.Name, prov.Type),
		Timeout:  r.req.Timeout,
	}
	pctx, cancel := context.WithTimeout(ctx, r.req.Timeout)
	defer cancel()
	listed, err := llm.ListModels(pctx, in)
	if err != nil {
		var unsupported *llm.UnsupportedProviderError
		if errors.As(err, &unsupported) {
			out = append(out, r.check(StatusSkipped, path, path, "provider type "+prov.Type+" cannot be probed", ""))
			return append(out, r.skipModels(models, "provider "+prov.Name+" was not probed")...)
		}
		msg, fix := classifyProviderError(prov, base, err)
		out = append(out, r.check(StatusError, path, path, msg, fix))
		return append(out, r.skipModels(models, "provider "+prov.Name+" failed")...)
	}

	ids := make(map[string]bool, len(listed))
	names := make([]string, 0, len(listed))
	for _, m := range listed {
		ids[m.ID] = true
		names = append(names, m.ID)
	}
	out = append(out, r.check(StatusOK, path, path, fmt.Sprintf("%s at %s lists %s", prov.Type, base, plural(len(listed), "model")), ""))
	for _, mi := range models {
		m := r.req.Cfg.Models[mi]
		mpath := "models[" + m.Model + "]"
		_, id, _ := strings.Cut(strings.TrimSpace(m.Model), "/")
		switch {
		case ids[id]:
			out = append(out, r.check(StatusOK, mpath, mpath, "listed by provider "+prov.Name, ""))
		case len(listed) == 0:
			out = append(out, r.check(StatusWarning, mpath, mpath, "provider "+prov.Name+" lists no models, so the id cannot be confirmed", ""))
		default:
			out = append(out, r.check(StatusWarning, mpath, mpath,
				fmt.Sprintf("not in the model list of provider %s (the server may still serve it)", prov.Name),
				"check the model id; the provider lists "+sample(names, 8)))
		}
	}
	return out
}

// skipModels marks the models of a provider that could not be probed.
func (r *runner) skipModels(models []int, reason string) []Check {
	var out []Check
	for _, mi := range models {
		mpath := "models[" + r.req.Cfg.Models[mi].Model + "]"
		out = append(out, r.check(StatusSkipped, mpath, mpath, reason, ""))
	}
	return out
}

// needsKey reports whether a provider aimed at a vendor's official endpoint
// has to carry a credential: there is no point asking OpenAI or Anthropic
// with nothing to present.
func needsKey(prov *config.ProviderConfig) bool {
	if strings.TrimSpace(prov.APIBase) != "" {
		return false
	}
	return prov.Type == "openai" || prov.Type == "anthropic"
}

// displayBase names the endpoint a provider talks to, for messages.
func displayBase(prov *config.ProviderConfig) string {
	if base := strings.TrimSpace(prov.APIBase); base != "" {
		return base
	}
	switch prov.Type {
	case "anthropic":
		return "https://api.anthropic.com"
	case "neuraldeep":
		return llm.NeuralDeepDefaultAPIBase()
	case "codex":
		return "the Codex backend"
	default:
		return llm.OpenAIDefaultAPIBase()
	}
}

var httpStatusRE = regexp.MustCompile(`HTTP (\d{3})`)

// classifyProviderError turns a model-list failure into what is wrong and
// how to fix it.
func classifyProviderError(prov *config.ProviderConfig, base string, err error) (string, string) {
	msg := err.Error()
	code := ""
	if m := httpStatusRE.FindStringSubmatch(msg); m != nil {
		code = m[1]
	}
	switch code {
	case "401", "403":
		return fmt.Sprintf("credential rejected by %s (HTTP %s)", base, code), credentialFix(prov)
	case "404":
		fix := "check api_base: the model list is not where this value points"
		trimmed := strings.TrimRight(strings.TrimSpace(prov.APIBase), "/")
		if prov.Type == "openai" && trimmed != "" && !strings.HasSuffix(trimmed, "/v1") {
			fix = fmt.Sprintf("api_base must include the API prefix, for OpenAI-compatible servers usually /v1 (try %s/v1)", trimmed)
		}
		return fmt.Sprintf("%s answered HTTP 404 to the model list", base), fix
	case "":
		fix := "check api_base and that the server is running"
		if strings.TrimSpace(prov.Proxy) != "" {
			fix += "; the request went through proxy " + prov.Proxy
		}
		return fmt.Sprintf("cannot reach %s: %s", base, shortErr(err)), fix
	default:
		return fmt.Sprintf("%s answered HTTP %s to the model list", base, code), "check api_base and the server's logs"
	}
}

// credentialFix names the credential a provider type actually uses.
func credentialFix(prov *config.ProviderConfig) string {
	switch prov.Type {
	case "codex":
		return fmt.Sprintf("run `coddy providers login %s` to sign in again", prov.Name)
	case "neuraldeep":
		return fmt.Sprintf("run `coddy providers login %s` again, or check api_key if you set one", prov.Name)
	default:
		return fmt.Sprintf("check api_key (or api_key_command / %s); the endpoint says it is not valid", config.ProviderAPIKeyEnvVarName(prov.Name))
	}
}

// shortErr strips the request wrapper from a transport error so the message
// says what failed rather than repeating the URL.
func shortErr(err error) string {
	var uerr *url.Error
	if errors.As(err, &uerr) && uerr.Err != nil {
		return uerr.Err.Error()
	}
	return strings.TrimPrefix(err.Error(), "list models: ")
}

// sample joins the first n names, marking how many more there are.
func sample(names []string, n int) string {
	if len(names) <= n {
		return strings.Join(names, ", ")
	}
	return strings.Join(names[:n], ", ") + fmt.Sprintf(" and %d more", len(names)-n)
}
