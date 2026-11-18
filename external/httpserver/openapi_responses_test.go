//go:build http

package httpserver

import (
	"encoding/json"
	"testing"

	"github.com/tidwall/gjson"
)

func TestOpenAPIResponsesDialects(t *testing.T) {
	raw, err := json.Marshal(openAPISpec())
	if err != nil {
		t.Fatal(err)
	}
	schema := gjson.GetBytes(raw, "components.schemas")
	if len(schema.Get("ResponsesCreateRequest.oneOf").Array()) != 2 {
		t.Fatal("Responses request does not distinguish direct models and profiles")
	}
	if schema.Get("CoddyResponsesCreateRequest.properties.input.type").String() != "string" {
		t.Fatal("profile string input schema lost")
	}
	if !schema.Get("OpenAIResponsesCreateRequest.properties.input.oneOf").Exists() {
		t.Fatal("direct typed input missing")
	}
	if schema.Get("OpenAIResponsesCreateResponse.properties.id.pattern").String() != "^resp_" {
		t.Fatal("direct response identity schema missing")
	}
	if !schema.Get("OpenAIResponsesCreateResponse.properties.usage").Exists() {
		t.Fatal("Responses usage schema missing")
	}
	if len(schema.Get("ResponsesCreateResponse.oneOf").Array()) != 2 {
		t.Fatal("Responses output dialects missing")
	}
}

// obj emits "required" only when it names fields: required: null is not valid
// OpenAPI and the input items' optional fields stay out of required.
func TestOpenAPIResponsesSchemaRequiredOmittedWhenEmpty(t *testing.T) {
	raw, err := json.Marshal(openAPISpec())
	if err != nil {
		t.Fatal(err)
	}
	schema := gjson.GetBytes(raw, "components.schemas.OpenAIResponsesCreateRequest")
	if schema.Get("properties.reasoning.required").Exists() {
		t.Fatal("reasoning object emits an empty required list")
	}
	items := gjson.GetBytes(raw, "components.schemas.OpenAIResponsesInputItem.oneOf")
	for i, item := range items.Array() {
		req := item.Get("required")
		if req.Exists() && req.Type != gjson.JSON {
			t.Fatalf("input item %d required is %s", i, req.Raw)
		}
		for _, r := range req.Array() {
			if r.String() == "arguments" || r.String() == "summary" {
				t.Fatalf("input item %d requires an optional field: %s", i, req.Raw)
			}
		}
		for name := range item.Get("properties").Map() {
			if name == "type" || name == "role" || name == "id" || name == "call_id" || name == "status" {
				continue
			}
			if item.Get("type.enum.0").String() == "reasoning" && name == "status" {
				t.Fatalf("reasoning input item carries a needless status property: %s", item.Raw)
			}
		}
	}
	if gjson.GetBytes(raw, "components.schemas.OpenAIResponsesInputItem.oneOf.4.type.enum.0").String() == "reasoning" {
		if gjson.GetBytes(raw, "components.schemas.OpenAIResponsesInputItem.oneOf.4.properties.status").Exists() {
			t.Fatal("reasoning input item still carries status")
		}
	}
}

func TestOpenAPIChatReasoningFieldsNullable(t *testing.T) {
	raw, err := json.Marshal(openAPISpec())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"reasoning", "reasoning_content"} {
		path := "components.schemas.OpenAIMessage.properties." + name + ".nullable"
		if !gjson.GetBytes(raw, path).Bool() {
			t.Errorf("%s must be nullable", path)
		}
	}
}

func TestOpenAPIDirectModelsDocumentIgnoredOutputCaps(t *testing.T) {
	raw, err := json.Marshal(openAPISpec())
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/v1/chat/completions", "/v1/responses"} {
		field := "paths." + path + ".post.responses.200.headers.X-Coddy-Ignored-Params.schema.type"
		if got := gjson.GetBytes(raw, field).String(); got != "string" {
			t.Errorf("%s ignored-params header schema type = %q, want string", path, got)
		}
	}
}
