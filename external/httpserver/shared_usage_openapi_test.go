//go:build http

package httpserver

import (
	"net/http"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"gopkg.in/yaml.v3"
)

func servedOpenAPI(t *testing.T) (paths, schemas map[string]any) {
	t.Helper()
	fx := newSharedFixture(t)
	resp := fx.get("/openapi.yaml", sharedTestMainToken)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("openapi.yaml: %d", resp.StatusCode)
	}
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(bodyString(t, resp)), &doc); err != nil {
		t.Fatal(err)
	}
	return doc["paths"].(map[string]any), doc["components"].(map[string]any)["schemas"].(map[string]any)
}

func schemaKeys(t *testing.T, schemas map[string]any, name string) []string {
	t.Helper()
	schema, ok := schemas[name].(map[string]any)
	if !ok {
		t.Fatalf("schema %s is missing", name)
	}
	var keys []string
	for k := range schema["properties"].(map[string]any) {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

// The usage route and its two schemas are described, and the schemas carry the
// keys of the wire types and no others: a field added to either side without the
// other is a drift of the contract clients are generated from.
func TestOpenAPIDescribesTheUsageRouteOfASharedModel(t *testing.T) {
	paths, schemas := servedOpenAPI(t)

	get := paths["/coddy/llm/models/{alias}/usage"].(map[string]any)["get"].(map[string]any)
	responses := get["responses"].(map[string]any)
	for _, code := range []string{"200", "401", "403", "404"} {
		if _, found := responses[code]; !found {
			t.Errorf("the usage route does not document %s", code)
		}
	}
	if strings.Contains(strings.ToLower(get["summary"].(string)), "reserved") || strings.Contains(get["description"].(string), "Reserved for the second phase") {
		t.Error("the usage route is still described as reserved")
	}
	desc := get["description"].(string)
	for _, want := range []string{"allowlist", "unknown_model", "supported", "never forced", "stale", "model_blocked"} {
		if !strings.Contains(desc, want) {
			t.Errorf("the usage description does not mention %q", want)
		}
	}
	ok200 := responses["200"].(map[string]any)["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
	if ok200["$ref"] != "#/components/schemas/CoddyLLMUsage" {
		t.Errorf("the 200 answer is %v", ok200)
	}

	if got, want := schemaKeys(t, schemas, "CoddyLLMUsage"), jsonNames(reflect.TypeOf(llm.WireUsage{})); !slices.Equal(got, want) {
		t.Errorf("CoddyLLMUsage properties %v, WireUsage keys %v", got, want)
	}
	if got, want := schemaKeys(t, schemas, "CoddyLLMUsageWindow"), jsonNames(reflect.TypeOf(llm.WireUsageWindow{})); !slices.Equal(got, want) {
		t.Errorf("CoddyLLMUsageWindow properties %v, WireUsageWindow keys %v", got, want)
	}
	// The blocker enum is the projection's fixed set plus "other".
	blockers := schemas["CoddyLLMUsage"].(map[string]any)["properties"].(map[string]any)["blockers"].(map[string]any)["items"].(map[string]any)["enum"].([]any)
	var enum []string
	for _, b := range blockers {
		enum = append(enum, b.(string))
	}
	var fixed []string
	for id := range sharedBlockerIDs {
		fixed = append(fixed, id)
	}
	fixed = append(fixed, sharedBlockerOther)
	slices.Sort(fixed)
	slices.Sort(enum)
	if !slices.Equal(enum, fixed) {
		t.Errorf("blocker enum %v, projection set %v", enum, fixed)
	}
}

// The provider usage route documents the alias parameter of a coddy row, its 400,
// and the model of the snapshot.
func TestOpenAPIDescribesTheModelOfAProviderUsageRead(t *testing.T) {
	paths, schemas := servedOpenAPI(t)
	get := paths["/coddy/providers/{name}/usage"].(map[string]any)["get"].(map[string]any)
	found := false
	for _, p := range get["parameters"].([]any) {
		if m, ok := p.(map[string]any); ok && m["name"] == "model" && m["in"] == "query" {
			found = true
		}
	}
	if !found {
		t.Error("the provider usage route does not document ?model=")
	}
	responses := get["responses"].(map[string]any)
	for _, code := range []string{"200", "400", "404"} {
		if _, ok := responses[code]; !ok {
			t.Errorf("the provider usage route does not document %s", code)
		}
	}
	for _, name := range []string{"ProviderUsage", "ProviderUsageAnswer"} {
		if !slices.Contains(schemaKeys(t, schemas, name), "model") {
			t.Errorf("schema %s has no model", name)
		}
	}
}
