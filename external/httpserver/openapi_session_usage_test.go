//go:build http

package httpserver

import (
	"strings"
	"testing"
)

func TestOpenAPISessionListTokenUsage(t *testing.T) {
	paths := openAPISpec()["paths"].(map[string]interface{})
	get := paths["/coddy/sessions"].(map[string]interface{})["get"].(map[string]interface{})
	responses := get["responses"].(map[string]interface{})
	response := responses["200"].(map[string]interface{})
	content := response["content"].(map[string]interface{})["application/json"].(map[string]interface{})
	schema := content["schema"].(map[string]interface{})
	properties := schema["properties"].(map[string]interface{})
	items := properties["sessions"].(map[string]interface{})["items"].(map[string]interface{})
	fields := items["properties"].(map[string]interface{})
	usage := fields["tokenUsage"].(map[string]interface{})
	if usage["type"] != "object" || usage["nullable"] != true {
		t.Fatalf("tokenUsage schema = %#v; want nullable object", usage)
	}
	counters := usage["properties"].(map[string]interface{})
	for _, name := range []string{"inputTokens", "outputTokens", "totalTokens"} {
		if counters[name].(map[string]interface{})["type"] != "integer" {
			t.Errorf("%s is not an integer", name)
		}
	}
	if !strings.Contains(usage["description"].(string), "explicitly unknown") {
		t.Errorf("tokenUsage description does not explain unknown usage: %v", usage["description"])
	}
}
