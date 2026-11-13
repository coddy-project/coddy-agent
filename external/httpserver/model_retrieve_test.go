//go:build http

package httpserver

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/tidwall/gjson"
)

// TestModelRetrieveByID serves one row of GET /v1/models at
// GET /v1/models/{id} (issue #579): the profile ids, a configured model whose
// id carries a slash - plain or percent-escaped - and a 404 in the OpenAI
// shape for a name the listing does not know.
func TestModelRetrieveByID(t *testing.T) {
	state := &openAIPassthroughState{}
	if err := state.reset(); err != nil {
		t.Fatal(err)
	}
	defer state.close()
	if err := state.startServer(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		path   string
		wantID string
	}{
		{"profile", "/v1/models/agent", "agent"},
		{"plain slashes", "/v1/models/local/qwen3-1.7b", "local/qwen3-1.7b"},
		{"escaped slash", "/v1/models/local%2Fqwen3-1.7b", "local/qwen3-1.7b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			status, raw := state.getRaw(t, tc.path)
			if status != http.StatusOK {
				t.Fatalf("GET %s -> %d: %s", tc.path, status, raw)
			}
			if got := gjson.Get(raw, "object").String(); got != "model" {
				t.Fatalf("object = %q, want model: %s", got, raw)
			}
			if got := gjson.Get(raw, "id").String(); got != tc.wantID {
				t.Fatalf("id = %q, want %q: %s", got, tc.wantID, raw)
			}
			if got := gjson.Get(raw, "limits"); !got.Exists() {
				t.Fatalf("the row lacks limits: %s", raw)
			}
		})
	}

	status, raw := state.getRaw(t, "/v1/models/local%2Fnope")
	if status != http.StatusNotFound {
		t.Fatalf("unknown id -> %d, want 404: %s", status, raw)
	}
	if got := gjson.Get(raw, "error.type").String(); got != "invalid_request_error" {
		t.Fatalf("error.type = %q, want invalid_request_error: %s", got, raw)
	}
	if got := gjson.Get(raw, "error.code").String(); got != "model_not_found" {
		t.Fatalf("error.code = %q, want model_not_found: %s", got, raw)
	}
}

// TestModelRetrieveRejectsPost answers 405 with the OpenAI Allow header for a
// non-GET method on the retrieve route.
func TestModelRetrieveRejectsPost(t *testing.T) {
	state := &openAIPassthroughState{}
	if err := state.reset(); err != nil {
		t.Fatal(err)
	}
	defer state.close()
	if err := state.startServer(); err != nil {
		t.Fatal(err)
	}
	status, _ := state.postRaw(t, "/v1/models/agent", `{}`)
	if status != http.StatusMethodNotAllowed {
		t.Fatalf("POST /v1/models/agent -> %d, want 405", status)
	}
}

// TestSupportedParametersRows checks the request parameters each row of
// GET /v1/models claims to take against what the endpoint actually accepts
// (issue #579): a profile takes no generation parameters, a codex model takes
// no output cap or temperature, a non-openai provider takes no top_p, and a
// model without reasoning levels takes no reasoning_effort.
func TestSupportedParametersRows(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.ProviderConfig{
			{Name: "local", Type: "openai", APIBase: "http://127.0.0.1:1"},
			{Name: "codex", Type: "codex", APIBase: "http://127.0.0.1:1"},
			{Name: "claude", Type: "anthropic", APIBase: "http://127.0.0.1:1"},
		},
		Models: []config.ModelEntry{
			{Model: "local/qwen3-1.7b"},
			{Model: "local/llama-3"},
			{Model: "codex/gpt-5.5", MaxTokens: 8192},
			{Model: "claude/claude-sonnet-4-6"},
		},
	}
	srv := New(cfg, nil, slog.New(slog.DiscardHandler), t.TempDir())
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()

	res, err := http.Get(ts.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	raw := make([]byte, 0, 8192)
	buf := make([]byte, 4096)
	for {
		n, err := res.Body.Read(buf)
		raw = append(raw, buf[:n]...)
		if err != nil {
			break
		}
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("GET /v1/models -> %d: %s", res.StatusCode, raw)
	}
	paramsOf := func(id string) map[string]bool {
		row := gjson.GetBytes(raw, `data.#(id=="`+id+`")`)
		if !row.Exists() {
			t.Fatalf("no row for %q: %s", id, raw)
		}
		out := map[string]bool{}
		for _, p := range row.Get("supported_parameters").Array() {
			out[p.String()] = true
		}
		return out
	}
	for _, tc := range []struct {
		id      string
		present []string
		absent  []string
	}{
		{"agent", []string{"attachments", "metadata", "stream_options"}, []string{"inline_files", "max_tokens", "max_output_tokens", "temperature", "tools", "reasoning", "reasoning_effort"}},
		{"local/qwen3-1.7b", []string{"max_tokens", "max_completion_tokens", "max_output_tokens", "temperature", "top_p", "tools", "tool_choice", "reasoning", "reasoning_effort", "metadata", "stream_options", "instructions"}, []string{"attachments", "store", "inline_files"}},
		{"local/llama-3", nil, []string{"reasoning", "reasoning_effort", "inline_files"}},
		{"codex/gpt-5.5", []string{"max_tokens", "max_completion_tokens", "max_output_tokens", "reasoning", "reasoning_effort", "tools", "instructions"}, []string{"temperature", "top_p", "inline_files"}},
		{"claude/claude-sonnet-4-6", []string{"max_tokens", "max_output_tokens", "temperature"}, []string{"top_p", "inline_files"}},
	} {
		params := paramsOf(tc.id)
		for _, want := range tc.present {
			if !params[want] {
				t.Errorf("%s: supported_parameters lacks %q", tc.id, want)
			}
		}
		for _, banned := range tc.absent {
			if params[banned] {
				t.Errorf("%s: supported_parameters claims %q which the endpoint refuses", tc.id, banned)
			}
		}
	}
	// A codex backend takes no output cap, so its configured max_tokens is
	// documented unsent and the row advertises no bound.
	codex := gjson.GetBytes(raw, `data.#(id=="codex/gpt-5.5")`)
	if got := codex.Get("max_output_tokens"); got.Exists() {
		t.Fatalf("a codex model advertises a bound its backend takes none of: %s", codex.Raw)
	}
	// A profile whose resolved model is multimodal keeps inline_files (the
	// fixture's models[0] is text-only, which is what dropped it above).
	cfg2 := &config.Config{
		Providers: cfg.Providers,
		Models:    append(slices.Clone(cfg.Models), config.ModelEntry{Model: "local/vision", Multimodal: true}),
		Agent:     config.Agent{Model: "local/vision"},
	}
	ts2 := httptest.NewServer(New(cfg2, nil, slog.New(slog.DiscardHandler), t.TempDir()).Handler())
	defer ts2.Close()
	res2, err := http.Get(ts2.URL + "/v1/models")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res2.Body.Close() }()
	var body2 struct {
		Data []struct {
			ID                  string   `json:"id"`
			SupportedParameters []string `json:"supported_parameters"`
		} `json:"data"`
	}
	if err := json.NewDecoder(res2.Body).Decode(&body2); err != nil {
		t.Fatal(err)
	}
	for _, row := range body2.Data {
		if row.ID == "agent" && !slices.Contains(row.SupportedParameters, "inline_files") {
			t.Fatalf("a profile resolved to a multimodal model dropped inline_files: %+v", row)
		}
	}
}

// TestMaxOutputTokensClamp clamps a request cap above the model's configured
// bound down to it: the upstream sees max_tokens of models[].max_tokens, not
// the bigger value the client asked for (issue #579).
func TestMaxOutputTokensClamp(t *testing.T) {
	state := &openAIPassthroughState{}
	if err := state.reset(); err != nil {
		t.Fatal(err)
	}
	state.outputCap = 4096
	defer state.close()
	if err := state.startServer(); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"max_tokens", "max_completion_tokens"} {
		status, raw := state.postRaw(t, "/v1/chat/completions", `{"model":"local/qwen3-1.7b","messages":[{"role":"user","content":"hi"}],"stream":false,"`+field+`":8192}`)
		if status != http.StatusOK {
			t.Fatalf("%s -> %d: %s", field, status, raw)
		}
		if got := gjson.Get(state.backend.last(), "max_tokens").Int(); got != 4096 {
			t.Fatalf("%s: upstream max_tokens = %d, want the clamped 4096: %s", field, got, state.backend.last())
		}
	}
	// A request under the bound is kept as asked.
	status, raw := state.postRaw(t, "/v1/chat/completions", `{"model":"local/qwen3-1.7b","messages":[{"role":"user","content":"hi"}],"stream":false,"max_tokens":512}`)
	if status != http.StatusOK {
		t.Fatalf("under-cap request -> %d: %s", status, raw)
	}
	if got := gjson.Get(state.backend.last(), "max_tokens").Int(); got != 512 {
		t.Fatalf("under-cap request clamped to %d, want 512: %s", got, state.backend.last())
	}
}

// TestMaxOutputTokensListed checks the row advertises the configured bound
// (and omits it for a model without one, and for a codex model whose backend
// takes no output cap).
func TestMaxOutputTokensListed(t *testing.T) {
	state := &openAIPassthroughState{}
	if err := state.reset(); err != nil {
		t.Fatal(err)
	}
	state.outputCap = 4096
	defer state.close()
	if err := state.startServer(); err != nil {
		t.Fatal(err)
	}
	status, raw := state.getRaw(t, "/v1/models")
	if status != http.StatusOK {
		t.Fatalf("GET /v1/models -> %d: %s", status, raw)
	}
	row := gjson.Get(raw, `data.#(id=="local/qwen3-1.7b")`)
	if got := row.Get("max_output_tokens").Int(); got != 4096 {
		t.Fatalf("max_output_tokens = %d, want 4096: %s", got, row.Raw)
	}
	other := gjson.Get(raw, `data.#(id=="local/qwen3-text")`)
	if got := other.Get("max_output_tokens"); got.Exists() {
		t.Fatalf("a model without a cap advertises one: %s", other.Raw)
	}
}
