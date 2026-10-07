//go:build http

package httpserver

import (
	"encoding/json"
	"strings"
	"testing"
)

// A function's parameters are a JSON Schema object: an object is kept, a
// missing or null schema becomes the empty object schema, anything else is
// refused.
func TestParseOpenAIToolsKeepsOnlyObjectSchemas(t *testing.T) {
	tools := func(params string) json.RawMessage {
		return json.RawMessage(`[{"type":"function","function":{"name":"f","parameters":` + params + `}}]`)
	}
	got, err := parseOpenAITools(tools(`{"type":"object","properties":{"a":{"type":"string"}}}`))
	if err != nil || len(got) != 1 {
		t.Fatalf("object schema: %v %v", got, err)
	}
	if schema, ok := got[0].InputSchema.(map[string]interface{}); !ok || schema["type"] != "object" {
		t.Fatalf("schema = %#v", got[0].InputSchema)
	}
	got, err = parseOpenAITools(tools(`null`))
	if err != nil || got[0].InputSchema.(map[string]interface{})["type"] != "object" {
		t.Fatalf("null schema: %#v %v", got, err)
	}
	for _, bad := range []string{`true`, `[1]`, `"x"`, `3`} {
		if _, err := parseOpenAITools(tools(bad)); err == nil || !strings.Contains(err.Error(), "invalid parameters") {
			t.Fatalf("%s: err = %v", bad, err)
		}
	}
}
