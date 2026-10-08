package dryrun

// --dry-run over a provider of type coddy: the probe reads the remote's list of
// shared models (GET /coddy/llm/models) and tells an unreachable remote from a
// refused token, a protocol mismatch, a remote that does not offer shared
// models, and a configured alias the remote does not list
// (docs/plans/remote-model-provider.md, 4.3 and 5a).

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
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

// --dry-run compares the keys a coddy row writes with the remote's listing
// (docs/plans/remote-model-provider-phase2.md, 3.7 and 3.8 item 2): a written
// key wins over the listing, a refresh never writes one, and a key that
// disagrees stays refused or unused for as long as it is written, so every
// difference is a warning that names the key, both values and the fix.

// wireRow is the listing row of a remote that shares everything about itself;
// mod bends it for one case.
func wireRow(alias string, mod func(*llm.WireModelRow)) llm.WireModelRow {
	row := llm.WireModelRow{
		ID:                alias,
		Revision:          "r-" + alias,
		MaxContextTokens:  131072,
		Multimodal:        true,
		ReasoningLevels:   []string{"low", "medium", "high"},
		ReasoningDefault:  "medium",
		AllowReasoningOff: true,
	}
	if mod != nil {
		mod(&row)
	}
	return row
}

// rowsRemote serves the given rows as the remote's listing.
func rowsRemote(t *testing.T, rows ...llm.WireModelRow) *httptest.Server {
	t.Helper()
	return sharedRemote(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(llm.WireListing{Protocol: llm.CoddyProtocol, Data: rows})
	})
}

// coddyKeysYAML is one remote/coder row that writes the given lines.
func coddyKeysYAML(base string, keys ...string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "providers:\n  - name: remote\n    type: coddy\n    api_base: %s\n    api_key: shared-token\nmodels:\n  - model: remote/coder\n", base)
	for _, k := range keys {
		fmt.Fprintf(&b, "    %s\n", k)
	}
	b.WriteString("agent:\n  model: remote/coder\n")
	return b.String()
}

// keyChecks is every check the run made about a key of the remote/coder row.
func keyChecks(rep *Report) []Check {
	var out []Check
	for _, c := range rep.Checks {
		if strings.HasPrefix(c.Path, "models[remote/coder].") {
			out = append(out, c)
		}
	}
	return out
}

func TestCoddyKeyThatDiffersFromTheListingWarns(t *testing.T) {
	cases := []struct {
		name   string
		key    string
		line   string
		remote func(*llm.WireModelRow)
		want   []string
	}{
		{"multimodal true against a text-only remote", "multimodal", "multimodal: true",
			func(r *llm.WireModelRow) { r.Multimodal = false },
			[]string{"multimodal", "true here", "remote lists false", "images are sent"}},
		{"multimodal false against a remote that reads images", "multimodal", "multimodal: false",
			nil,
			[]string{"multimodal", "false here", "remote lists true", "never sends images"}},
		{"off allowed here, refused there", "allow_reasoning_off", "allow_reasoning_off: true",
			func(r *llm.WireModelRow) { r.AllowReasoningOff = false },
			[]string{"allow_reasoning_off", "true here", "remote lists false", "invalid_option"}},
		{"off refused here, allowed there", "allow_reasoning_off", "allow_reasoning_off: false",
			nil,
			[]string{"allow_reasoning_off", "false here", "remote lists true", "never offered"}},
		{"another default the remote offers", "reasoning_default", "reasoning_default: high",
			nil,
			[]string{"reasoning_default", `"high" here`, `remote lists "medium"`, "new chats start at"}},
		{"a default the remote does not offer", "reasoning_default", "reasoning_default: high\n    reasoning_levels: [low, medium, high]",
			func(r *llm.WireModelRow) { r.ReasoningLevels = []string{"low", "medium"} },
			[]string{"reasoning_default", `"high" here`, `remote lists "medium"`, "invalid_option"}},
		{"a default where the remote lists none", "reasoning_default", "reasoning_default: high",
			func(r *llm.WireModelRow) { r.ReasoningDefault = "" },
			[]string{"reasoning_default", `"high" here`, "no default"}},
		{"a bigger window than the remote's", "max_context_tokens", "max_context_tokens: 200000",
			nil,
			[]string{"max_context_tokens", "200000 here", "remote lists 131072", "compaction"}},
		{"a smaller window than the remote's", "max_context_tokens", "max_context_tokens: 32000",
			nil,
			[]string{"max_context_tokens", "32000 here", "remote lists 131072"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := rowsRemote(t, wireRow("coder", tc.remote))
			rep := run(t, coddyKeysYAML(srv.URL, tc.line), nil)

			c := find(t, rep, "models[remote/coder]."+tc.key)
			if c.Status != StatusWarning {
				t.Fatalf("status %s, want a warning that does not fail the run: %+v", c.Status, c)
			}
			for _, want := range tc.want {
				if !strings.Contains(c.Message, want) {
					t.Errorf("message %q does not say %q", c.Message, want)
				}
			}
			if !strings.Contains(c.Fix, "remove "+tc.key) || !strings.Contains(c.Fix, "remote") {
				t.Errorf("fix %q does not say to remove the key and inherit the remote's value", c.Fix)
			}
			if c.Line != 9 {
				t.Errorf("line %d, want the written key (9)", c.Line)
			}
			if rep.Errors() != 0 {
				t.Errorf("a key that differs must not fail the run: %+v", rep.Checks)
			}
			if m := find(t, rep, "models[remote/coder]"); m.Status != StatusOK {
				t.Errorf("the alias is still shared, got %+v", m)
			}
		})
	}
}

func TestCoddyReasoningLevelsNameBothDirectionsOfTheDifference(t *testing.T) {
	srv := rowsRemote(t, wireRow("coder", nil))
	rep := run(t, coddyKeysYAML(srv.URL, "reasoning_levels: [low, high, xhigh]"), nil)

	c := find(t, rep, "models[remote/coder].reasoning_levels")
	if c.Status != StatusWarning {
		t.Fatalf("%+v", c)
	}
	for _, want := range []string{"[low, high, xhigh] here", "remote lists [low, medium, high]", "refused", "xhigh", "invalid_option", "unused", "medium"} {
		if !strings.Contains(c.Message, want) {
			t.Errorf("message %q does not say %q", c.Message, want)
		}
	}
	if !strings.Contains(c.Fix, "remove reasoning_levels") {
		t.Errorf("fix %q", c.Fix)
	}
	if c.Line != 9 {
		t.Errorf("line %d, want 9", c.Line)
	}
}

// A list that only leaves levels out refuses nothing: the remote's levels it
// omits are unused, and the message says only that.
func TestCoddyReasoningLevelsThatOnlyOmitAreUnusedNotRefused(t *testing.T) {
	srv := rowsRemote(t, wireRow("coder", nil))
	rep := run(t, coddyKeysYAML(srv.URL, "reasoning_levels: [low, high]"), nil)

	c := find(t, rep, "models[remote/coder].reasoning_levels")
	if !strings.Contains(c.Message, "unused") || !strings.Contains(c.Message, "medium") {
		t.Errorf("message %q", c.Message)
	}
	if strings.Contains(c.Message, "invalid_option") {
		t.Errorf("a list inside the remote's levels refuses nothing: %q", c.Message)
	}
}

// The explicit opt-out wins over the listing, and it is still a difference.
func TestCoddyEmptyReasoningLevelsAgainstARemoteWithLevelsWarns(t *testing.T) {
	srv := rowsRemote(t, wireRow("coder", nil))
	rep := run(t, coddyKeysYAML(srv.URL, "reasoning_levels: []"), nil)

	c := find(t, rep, "models[remote/coder].reasoning_levels")
	if c.Status != StatusWarning || !strings.Contains(c.Message, "[] here") || !strings.Contains(c.Message, "low") {
		t.Errorf("%+v", c)
	}
}

func TestCoddyReasoningLevelsOnARemoteWithoutLevelsAreAllRefused(t *testing.T) {
	srv := rowsRemote(t, wireRow("coder", func(r *llm.WireModelRow) {
		r.ReasoningLevels, r.ReasoningDefault, r.AllowReasoningOff = nil, "", false
	}))
	rep := run(t, coddyKeysYAML(srv.URL, "reasoning_levels: [low, high]"), nil)

	c := find(t, rep, "models[remote/coder].reasoning_levels")
	if c.Status != StatusWarning || !strings.Contains(c.Message, "no levels") || !strings.Contains(c.Message, "low, high") || !strings.Contains(c.Message, "invalid_option") {
		t.Errorf("%+v", c)
	}
}

// No warning for a key that is absent, and none for one that says what the
// remote says: the order of a list is not a difference.
func TestCoddyKeysAbsentOrEqualAreSilent(t *testing.T) {
	srv := rowsRemote(t, wireRow("coder", nil))
	cases := map[string][]string{
		"no key at all": nil,
		"every key equal": {
			"max_context_tokens: 131072", "multimodal: true", "allow_reasoning_off: true",
			"reasoning_default: medium", "reasoning_levels: [low, medium, high]",
		},
		"the same levels in another order, off named": {"reasoning_levels: [high, off, low, medium, low]"},
		"a default with stray spaces":                 {`reasoning_default: " medium "`},
		"an unset window":                             {"max_context_tokens: 0"},
	}
	for name, keys := range cases {
		t.Run(name, func(t *testing.T) {
			rep := run(t, coddyKeysYAML(srv.URL, keys...), nil)
			if got := keyChecks(rep); len(got) != 0 {
				t.Errorf("warned about keys that match the listing: %+v", got)
			}
			if rep.Warnings() != 0 || rep.Errors() != 0 {
				t.Errorf("%d warnings and %d errors in %+v", rep.Warnings(), rep.Errors(), rep.Checks)
			}
		})
	}
}

// A remote that lists no window gives nothing to compare max_context_tokens with.
func TestCoddyWindowIsNotComparedWithAListingThatReportsNone(t *testing.T) {
	srv := rowsRemote(t, wireRow("coder", func(r *llm.WireModelRow) { r.MaxContextTokens = 0 }))
	rep := run(t, coddyKeysYAML(srv.URL, "max_context_tokens: 200000"), nil)
	if got := keyChecks(rep); len(got) != 0 {
		t.Errorf("%+v", got)
	}
}

// The phase 1 residue: Settings saved multimodal: false and allow_reasoning_off:
// false on every row. Against a remote that lists true they pin the row to no,
// which is exactly what the warning is for; against a remote that lists false
// they are equal and say nothing.
func TestCoddyPhaseOneResidueIsWarnedOnlyWhereTheRemoteDisagrees(t *testing.T) {
	residue := []string{"multimodal: false", "allow_reasoning_off: false"}

	capable := rowsRemote(t, wireRow("coder", nil))
	rep := run(t, coddyKeysYAML(capable.URL, residue...), nil)
	if got := keyChecks(rep); len(got) != 2 {
		t.Fatalf("want a warning for each of the two pinned keys, got %+v", got)
	}
	if find(t, rep, "models[remote/coder].multimodal").Line != 9 || find(t, rep, "models[remote/coder].allow_reasoning_off").Line != 10 {
		t.Errorf("each warning is located at its own line: %+v", keyChecks(rep))
	}

	plain := rowsRemote(t, wireRow("coder", func(r *llm.WireModelRow) {
		r.Multimodal, r.AllowReasoningOff = false, false
	}))
	rep = run(t, coddyKeysYAML(plain.URL, residue...), nil)
	if got := keyChecks(rep); len(got) != 0 {
		t.Errorf("equal values warned: %+v", got)
	}
}

func TestCoddyEveryDifferingKeyGetsItsOwnWarning(t *testing.T) {
	srv := rowsRemote(t, wireRow("coder", nil))
	rep := run(t, coddyKeysYAML(srv.URL,
		"max_context_tokens: 64000", "multimodal: false", "allow_reasoning_off: false",
		"reasoning_default: low", "reasoning_levels: [low]"), nil)
	got := keyChecks(rep)
	if len(got) != 5 {
		t.Fatalf("want five warnings, got %+v", got)
	}
	for i, key := range []string{"max_context_tokens", "multimodal", "allow_reasoning_off", "reasoning_default", "reasoning_levels"} {
		if got[i].Path != "models[remote/coder]."+key || got[i].Status != StatusWarning {
			t.Errorf("warning %d is %+v, want %s", i, got[i], key)
		}
	}
	if rep.Warnings() != 5 || rep.Errors() != 0 {
		t.Errorf("%d warnings and %d errors", rep.Warnings(), rep.Errors())
	}
}

// Only the model the row names is compared: another alias of the same remote
// does not lend its values to this row.
func TestCoddyKeysAreComparedWithTheirOwnAlias(t *testing.T) {
	srv := rowsRemote(t,
		wireRow("coder", func(r *llm.WireModelRow) { r.Multimodal = false }),
		wireRow("planner", nil))
	rep := run(t, coddyKeysYAML(srv.URL, "multimodal: true"), nil)
	c := find(t, rep, "models[remote/coder].multimodal")
	if !strings.Contains(c.Message, "remote lists false") {
		t.Errorf("%+v", c)
	}
}

// The remote does not share the alias: that stays the error of phase 1, and
// there is no listing row to compare a key with.
func TestCoddyUnlistedAliasKeepsItsErrorAndGetsNoKeyWarning(t *testing.T) {
	srv := rowsRemote(t, wireRow("planner", nil))
	rep := run(t, coddyKeysYAML(srv.URL, "multimodal: true", "reasoning_levels: [low]"), nil)

	if m := find(t, rep, "models[remote/coder]"); m.Status != StatusError || !strings.Contains(m.Message, "does not share") {
		t.Errorf("%+v", m)
	}
	if got := keyChecks(rep); len(got) != 0 {
		t.Errorf("keys of an alias the remote lacks have nothing to differ from: %+v", got)
	}
}

// An unreachable remote has no listing to compare with: the findings are the
// ones it always had, and the keys are not guessed at.
func TestCoddyUnreachableRemoteKeepsItsFindingsAndComparesNoKeys(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	base := srv.URL
	srv.Close()
	rep := run(t, coddyKeysYAML(base, "multimodal: true", "max_context_tokens: 1000"), nil)

	if p := find(t, rep, "providers[remote]"); p.Status != StatusError || !strings.Contains(p.Message, "cannot reach") {
		t.Errorf("%+v", p)
	}
	if m := find(t, rep, "models[remote/coder]"); m.Status != StatusSkipped {
		t.Errorf("%+v", m)
	}
	if got := keyChecks(rep); len(got) != 0 {
		t.Errorf("%+v", got)
	}
}

func TestCoddyRefusedTokenComparesNoKeys(t *testing.T) {
	srv := sharedRemote(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusUnauthorized) })
	rep := run(t, coddyKeysYAML(srv.URL, "multimodal: true"), nil)
	if p := find(t, rep, "providers[remote]"); p.Status != StatusError || !strings.Contains(p.Message, "refused the credential") {
		t.Errorf("%+v", p)
	}
	if got := keyChecks(rep); len(got) != 0 {
		t.Errorf("%+v", got)
	}
}

// The report prints the key, its place in the file and the fix, and the status
// line counts the warnings without failing.
func TestCoddyKeyWarningIsPrintedWithItsPlaceAndFix(t *testing.T) {
	srv := rowsRemote(t, wireRow("coder", func(r *llm.WireModelRow) { r.Multimodal = false }))
	rep := run(t, coddyKeysYAML(srv.URL, "multimodal: true"), nil)

	var buf bytes.Buffer
	rep.WriteProblems(&buf)
	out := buf.String()
	for _, want := range []string{"warning  models[remote/coder].multimodal: ", "config.yaml:9", "fix: remove multimodal", "dry run: 0 errors, 1 warning, "} {
		if !strings.Contains(out, want) {
			t.Errorf("report does not contain %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "shared-token") {
		t.Errorf("the report carries the token:\n%s", out)
	}
}

// Every other provider type keeps its checks: the same keys on an openai row
// are not compared with anything, whatever the server's list says.
func TestOtherTypesAreNotComparedKeyByKey(t *testing.T) {
	srv := modelServer(t, "qwen")
	rep := run(t, fmt.Sprintf(`providers:
  - name: local
    type: openai
    api_base: %s/v1
models:
  - model: local/qwen
    max_context_tokens: 200000
    multimodal: true
    allow_reasoning_off: true
    reasoning_default: high
    reasoning_levels: [low, high]
agent:
  model: local/qwen
`, srv.URL), nil)
	for _, c := range rep.Checks {
		if strings.HasPrefix(c.Path, "models[local/qwen].") {
			t.Errorf("a key check on an openai row: %+v", c)
		}
	}
	if m := find(t, rep, "models[local/qwen]"); m.Status != StatusOK || m.Message != "listed by provider local" {
		t.Errorf("%+v", m)
	}
	if rep.Warnings() != 0 || rep.Errors() != 0 {
		t.Errorf("%d warnings and %d errors in %+v", rep.Warnings(), rep.Errors(), rep.Checks)
	}
}

// coddyKeyFindings is a pure function of one row and one listing record.
func TestCoddyKeyFindingsOfARowThatWritesNothingIsEmpty(t *testing.T) {
	row := llm.ModelEntry{ID: "coder", ContextWindow: 131072, Multimodal: true, ReasoningLevels: []string{"low"}, ReasoningDefault: "low", AllowReasoningOff: true}
	if got := coddyKeyFindings(config.ModelEntry{Model: "remote/coder"}, row); len(got) != 0 {
		t.Errorf("%+v", got)
	}
}

func TestCoddyKeyFindingsIgnoreADefaultTheRowDoesNotOffer(t *testing.T) {
	// The row writes levels without "high": a default of "high" is ignored by
	// the loader (ModelEntry.ReasoningDefault), and the message says so.
	levels := []string{"low", "medium"}
	row := llm.ModelEntry{ID: "coder", ReasoningLevels: []string{"low", "medium", "high"}, ReasoningDefault: "medium"}
	got := coddyKeyFindings(config.ModelEntry{Model: "remote/coder", ReasoningLevels: &levels, ReasoningDefault: "high"}, row)
	var def *finding
	for i := range got {
		if got[i].key == "reasoning_default" {
			def = &got[i]
		}
	}
	if def == nil || !strings.Contains(def.message, "not one of this row's") {
		t.Fatalf("%+v", got)
	}
}
