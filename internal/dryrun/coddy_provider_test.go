package dryrun

// --dry-run over a provider of type coddy: the probe reads the remote's list of
// shared models (GET /coddy/llm/models) and tells an unreachable remote from a
// refused token, a protocol mismatch, a remote that does not offer shared
// models, and a configured alias the remote does not list
// (docs/plans/remote-model-provider.md, 4.3 and 5a).

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// sharedRemote is a stand-in remote coddy serve: it answers the listing route
// with handler and everything else with 404.
func sharedRemote(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, llm.CoddyModelsPath) {
			http.NotFound(w, r)
			return
		}
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func listing(aliases ...string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		var rows []string
		for _, a := range aliases {
			rows = append(rows, fmt.Sprintf(`{"id":%q,"revision":"r-%s","max_context_tokens":131072,"multimodal":false,"allow_reasoning_off":false}`, a, a))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"protocol":%d,"data":[%s]}`, llm.CoddyProtocol, strings.Join(rows, ","))
	}
}

func coddyYAML(base string, models ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "providers:\n  - name: remote\n    type: coddy\n    api_base: %s\n    api_key: shared-token\n", base)
	b.WriteString("models:\n")
	for _, m := range models {
		fmt.Fprintf(&b, "  - model: remote/%s\n", m)
	}
	fmt.Fprintf(&b, "agent:\n  model: remote/%s\n", models[0])
	return b.String()
}

func TestCoddyProviderListsTheSharedModelsAndChecksTheAliases(t *testing.T) {
	srv := sharedRemote(t, listing("coder", "planner"))
	rep := run(t, coddyYAML(srv.URL, "coder", "ghost"), nil)

	p := find(t, rep, "providers[remote]")
	if p.Status != StatusOK || !strings.Contains(p.Message, "2 models") || !strings.Contains(p.Message, "coddy") {
		t.Errorf("provider check %+v, want it to say the remote shares 2 models", p)
	}
	if m := find(t, rep, "models[remote/coder]"); m.Status != StatusOK || !strings.Contains(m.Message, "shared") {
		t.Errorf("listed alias %+v", m)
	}
	ghost := find(t, rep, "models[remote/ghost]")
	if ghost.Status != StatusError || ghost.Line == 0 {
		t.Fatalf("an alias the remote does not share: %+v, want an error located at its line: the remote refuses it with a 404", ghost)
	}
	for _, want := range []string{"ghost", "does not share"} {
		if !strings.Contains(ghost.Message, want) {
			t.Errorf("message %q does not say %q", ghost.Message, want)
		}
	}
	if !strings.Contains(ghost.Fix, "coder") || !strings.Contains(ghost.Fix, "planner") {
		t.Errorf("fix %q does not list the aliases the remote shares", ghost.Fix)
	}
}

// A remote that shares nothing is not "no way to confirm the id": every alias
// of the row is refused by it.
func TestCoddyRemoteSharingNothingIsAnError(t *testing.T) {
	srv := sharedRemote(t, listing())
	rep := run(t, coddyYAML(srv.URL, "coder"), nil)

	if m := find(t, rep, "models[remote/coder]"); m.Status != StatusError {
		t.Errorf("model of a remote that shares nothing: %+v", m)
	}
	p := find(t, rep, "providers[remote]")
	if p.Status != StatusWarning || !strings.Contains(p.Message, "shares no models") {
		t.Errorf("provider check %+v, want a warning that the remote shares nothing", p)
	}
}

func TestCoddyRefusedTokenIsACredentialError(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var gotAuth string
			srv := sharedRemote(t, func(w http.ResponseWriter, r *http.Request) {
				gotAuth = r.Header.Get("Authorization")
				w.Header().Set("Content-Type", "text/plain")
				w.WriteHeader(status)
				_, _ = fmt.Fprint(w, "Unauthorized")
			})
			rep := run(t, coddyYAML(srv.URL, "coder"), nil)

			p := find(t, rep, "providers[remote]")
			if p.Status != StatusError || !strings.Contains(p.Message, "refused the credential") || p.Line != 3 {
				t.Errorf("provider check %+v, want a refused credential at the provider entry", p)
			}
			if !strings.Contains(p.Fix, "api_key") || !strings.Contains(p.Fix, "shared_models.tokens") {
				t.Errorf("fix %q does not point at api_key and the remote's shared-model tokens", p.Fix)
			}
			if gotAuth != "Bearer shared-token" {
				t.Errorf("the probe presented %q", gotAuth)
			}
			if strings.Contains(p.Message+p.Fix, "shared-token") {
				t.Errorf("the report carries the token: %+v", p)
			}
			if m := find(t, rep, "models[remote/coder]"); m.Status != StatusSkipped {
				t.Errorf("a model of a failed provider is skipped, got %+v", m)
			}
		})
	}
}

func TestCoddyProtocolMismatchNamesBothVersions(t *testing.T) {
	srv := sharedRemote(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{"protocol":2,"data":[{"id":"coder"}]}`)
	})
	rep := run(t, coddyYAML(srv.URL, "coder"), nil)

	p := find(t, rep, "providers[remote]")
	if p.Status != StatusError || !strings.Contains(p.Message, "protocol 2") || !strings.Contains(p.Message, fmt.Sprintf("speaks %d", llm.CoddyProtocol)) {
		t.Errorf("provider check %+v, want both protocol versions named", p)
	}
	if !strings.Contains(p.Fix, "update") {
		t.Errorf("fix %q does not say to update the older Coddy", p.Fix)
	}
}

func TestCoddyRemoteWithoutTheRoutesDoesNotOfferSharedModels(t *testing.T) {
	cases := map[string]http.HandlerFunc{
		"the web page of a build without the routes": func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, "<!doctype html><html><body>Coddy</body></html>")
		},
		"a plain 404": func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) },
	}
	for name, handler := range cases {
		t.Run(name, func(t *testing.T) {
			srv := sharedRemote(t, handler)
			rep := run(t, coddyYAML(srv.URL, "coder"), nil)

			p := find(t, rep, "providers[remote]")
			if p.Status != StatusError || !strings.Contains(p.Message, "does not offer shared models") {
				t.Errorf("provider check %+v, want 'does not offer shared models'", p)
			}
			if !strings.Contains(p.Fix, "shared_as") || !strings.Contains(p.Fix, "swarm/nodes") {
				t.Errorf("fix %q does not say what api_base should be (a remote with shared_as, or a relay mount)", p.Fix)
			}
		})
	}
}

func TestCoddyUnreachableRemoteIsAnError(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	base := srv.URL
	srv.Close()
	rep := run(t, coddyYAML(base, "coder"), nil)

	p := find(t, rep, "providers[remote]")
	if p.Status != StatusError || !strings.Contains(p.Message, "cannot reach") {
		t.Errorf("provider check %+v", p)
	}
	if m := find(t, rep, "models[remote/coder]"); m.Status != StatusSkipped {
		t.Errorf("model of an unreachable remote %+v", m)
	}
}

// A relay mount is a path under the relay's origin; the probe appends the route
// to it as the provider does.
func TestCoddyProviderThroughARelayMount(t *testing.T) {
	var path string
	srv := sharedRemote(t, func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		listing("coder")(w, r)
	})
	rep := run(t, coddyYAML(srv.URL+"/swarm/nodes/lab/", "coder"), nil)

	if p := find(t, rep, "providers[remote]"); p.Status != StatusOK {
		t.Errorf("provider check %+v", p)
	}
	if path != "/swarm/nodes/lab"+llm.CoddyModelsPath {
		t.Errorf("the probe asked %q, want the route under the mount", path)
	}
}

// Other provider types keep the checks they had: an id missing from the list
// of an OpenAI-compatible server is a warning, since the server may still serve
// it.
func TestOtherTypesKeepTheWarningForAnUnlistedModel(t *testing.T) {
	srv := modelServer(t, "qwen")
	rep := run(t, fmt.Sprintf("providers:\n  - name: local\n    type: openai\n    api_base: %s/v1\nmodels:\n  - model: local/qwen\n  - model: local/other\nagent:\n  model: local/qwen\n", srv.URL), nil)
	if other := find(t, rep, "models[local/other]"); other.Status != StatusWarning {
		t.Errorf("unlisted model of an openai row: %+v", other)
	}
}
