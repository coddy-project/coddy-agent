//go:build http

package httpserver

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func TestCatalogImageOutputJSON(t *testing.T) {
	yes, no := true, false
	for _, tc := range []struct {
		value *bool
		want  string
	}{{nil, `{"context_window":100}`}, {&no, `{"context_window":100,"image_output":false}`}, {&yes, `{"context_window":100,"image_output":true}`}} {
		b, err := json.Marshal(catalogLimits{ContextWindow: 100, ImageOutput: tc.value})
		if err != nil || strings.TrimSpace(string(b)) != tc.want {
			t.Errorf("got %s, %v; want %s", b, err, tc.want)
		}
	}
}

func TestCatalogModalitiesWithDifferentProviderWindow(t *testing.T) {
	listed := withCatalogLimits([]llm.ModelEntry{{ID: "gpt-5", ContextWindow: 1}})
	if listed[0].Catalog == nil || listed[0].Catalog.ImageOutput == nil {
		t.Fatalf("missing catalog modalities: %+v", listed[0])
	}
	b, err := json.Marshal(listed[0].Catalog)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "context_window") || strings.Contains(string(b), "max_tokens") {
		t.Fatalf("incompatible limits leaked: %s", b)
	}
}

func TestCatalogImageModelWithoutWindow(t *testing.T) {
	listed := withCatalogLimits([]llm.ModelEntry{{ID: "gpt-image-1.5", ContextWindow: 12345}})
	catalog := listed[0].Catalog
	if catalog == nil || catalog.ImageOutput == nil || !*catalog.ImageOutput || catalog.ContextWindow != 0 || catalog.MaxTokens != 0 {
		t.Fatalf("catalog = %+v", catalog)
	}
}
