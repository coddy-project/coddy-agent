//go:build http

package httpserver

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"gopkg.in/yaml.v3"
)

// The served spec describes both routes, the error object and the event stream,
// and every reference of the new schemas resolves.
func TestOpenAPIDescribesTheSharedModelRoutes(t *testing.T) {
	fx := newSharedFixture(t)
	resp := fx.get("/openapi.yaml", sharedTestMainToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("openapi.yaml: %d", resp.StatusCode)
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(bodyString(t, resp)), &doc); err != nil {
		t.Fatal(err)
	}
	paths := doc["paths"].(map[string]any)
	schemas := doc["components"].(map[string]any)["schemas"].(map[string]any)

	completions := paths[llm.CoddyCompletionsPath].(map[string]any)["post"].(map[string]any)
	responses := completions["responses"].(map[string]any)
	ok200 := responses["200"].(map[string]any)["content"].(map[string]any)
	if _, found := ok200["text/event-stream"]; !found {
		t.Fatalf("the completions route is not documented as an event stream: %v", ok200)
	}
	for _, code := range []string{"400", "401", "403", "404", "408", "413", "429", "502"} {
		if _, found := responses[code]; !found {
			t.Errorf("the completions route does not document %s", code)
		}
	}
	if _, found := responses["429"].(map[string]any)["headers"].(map[string]any)["Retry-After"]; !found {
		t.Error("the 429 does not document Retry-After")
	}
	desc := completions["description"].(string)
	for _, want := range []string{"stateless", "text/event-stream", "max_streams", "heartbeat", "Expect: 100-continue", "allow_insecure", "exactly one terminal"} {
		if !strings.Contains(desc, want) {
			t.Errorf("the completions description does not mention %q", want)
		}
	}
	if _, found := paths[llm.CoddyModelsPath].(map[string]any)["get"]; !found {
		t.Fatal("the listing is not documented")
	}
	if _, found := paths["/coddy/llm/models/{alias}/usage"]; !found {
		t.Fatal("the usage route is not documented")
	}
	alive, found := paths["/coddy/llm/calls/{id}/alive"].(map[string]any)["post"].(map[string]any)
	if !found {
		t.Fatal("the ping of the application probe is not documented")
	}
	for _, code := range []string{"204", "401", "403", "404"} {
		if _, ok := alive["responses"].(map[string]any)[code]; !ok {
			t.Errorf("the ping does not document %s", code)
		}
	}
	if !strings.Contains(completions["description"].(string), "X-Coddy-Probe") {
		t.Error("the completions description does not mention the probe header")
	}
	if _, ok := responses["200"].(map[string]any)["headers"].(map[string]any)["X-Coddy-Probe"]; !ok {
		t.Error("the 200 does not document the confirmation header")
	}
	if !strings.Contains(bodyString(t, fx.get("/openapi.yaml", sharedTestMainToken)), "- mtls") {
		t.Error("the counters' class enum lacks mtls (the class of a client certificate, httpserver.shared_models.cert_names)")
	}
	if _, found := paths["/coddy/shared-models/stats"]; !found {
		t.Fatal("the stats route is not documented")
	}
	if _, found := schemas["CoddySharedStats"]; !found {
		t.Fatal("schema CoddySharedStats is missing")
	}
	if !strings.Contains(schemas["CoddyLLMError"].(map[string]any)["description"].(string), "rate_window") {
		t.Fatal("the error object does not document the window code")
	}

	for _, name := range []string{"CoddyLLMError", "CoddyLLMModelList", "CoddyLLMModelRow", "CoddyLLMRequest", "CoddyLLMMessage", "CoddyLLMChunk", "CoddyLLMFinal", "CoddyLLMToolCall"} {
		if _, found := schemas[name]; !found {
			t.Errorf("schema %s is missing", name)
		}
	}
	// Every $ref in the document resolves.
	raw, _ := json.Marshal(doc)
	for _, ref := range collectRefs(string(raw)) {
		name := strings.TrimPrefix(ref, "#/components/schemas/")
		if _, found := schemas[name]; !found {
			t.Errorf("dangling reference %s", ref)
		}
	}
	// The error object names its kinds.
	props := schemas["CoddyLLMError"].(map[string]any)["properties"].(map[string]any)
	kinds := props["kind"].(map[string]any)["enum"].([]any)
	if len(kinds) != 6 {
		t.Errorf("kinds: %v", kinds)
	}
}

func collectRefs(doc string) []string {
	var out []string
	const marker = `"$ref":"`
	for {
		i := strings.Index(doc, marker)
		if i < 0 {
			return out
		}
		doc = doc[i+len(marker):]
		j := strings.Index(doc, `"`)
		out = append(out, doc[:j])
		doc = doc[j:]
	}
}
