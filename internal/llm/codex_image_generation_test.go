package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCodexNativeImageGeneration(t *testing.T) {
	for _, tc := range []struct {
		name     string
		enabled  bool
		complete bool
		reject   bool
	}{
		{"default", false, false, false},
		{"stream", true, false, false},
		{"complete", true, true, false},
		{"rejected", true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var request map[string]any
			png := generatedPNG(t)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/responses" || !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer ") || r.Header.Get("chatgpt-account-id") != "acct-1" || r.Header.Get("originator") != "codex_cli_rs" {
					t.Errorf("unexpected request path or authentication headers: %s %v", r.URL.Path, r.Header)
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				if tc.reject {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = fmt.Fprint(w, `{"error":{"message":"image tool rejected"}}`)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				if tc.enabled {
					_, _ = fmt.Fprint(w, imageEvent(png))
				}
				sseCompleted(w)
			}))
			defer srv.Close()
			p := newCodexTestProvider(t, srv.URL)
			p.httpClient = srv.Client()
			ctx := context.Background()
			if tc.enabled {
				ctx = WithImageGeneration(ctx)
			}
			tools := []ToolDefinition{{Name: "lookup", InputSchema: map[string]any{"type": "object"}}}
			var chunks []StreamChunk
			var result *Response
			var err error
			if tc.complete {
				result, err = p.Complete(ctx, []Message{{Role: RoleUser, Content: "draw"}}, tools)
			} else {
				result, err = p.Stream(ctx, []Message{{Role: RoleUser, Content: "draw"}}, tools, func(c StreamChunk) { chunks = append(chunks, c) })
			}
			if tc.reject {
				if err == nil || !strings.Contains(err.Error(), "image tool rejected") {
					t.Fatalf("rejection: %v", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if _, ok := request["max_output_tokens"]; ok {
				t.Error("Codex request includes max_output_tokens")
			}
			if request["stream"] != true {
				t.Errorf("stream = %v", request["stream"])
			}
			entries, ok := request["tools"].([]any)
			if !ok {
				t.Fatalf("tools = %#v", request["tools"])
			}
			var images, functions int
			for _, entry := range entries {
				tool, ok := entry.(map[string]any)
				if !ok {
					t.Fatalf("tool = %#v", entry)
				}
				switch tool["type"] {
				case "image_generation":
					images++
					if len(tool) != 1 {
						t.Errorf("image tool = %#v", tool)
					}
				case "function":
					if tool["name"] == "lookup" {
						functions++
					}
				}
			}
			want := 0
			if tc.enabled {
				want = 1
			}
			if images != want || functions != 1 {
				t.Errorf("tools = %#v", entries)
			}
			if tc.reject {
				return
			}
			if len(result.GeneratedImages) != want {
				t.Fatalf("images = %+v", result.GeneratedImages)
			}
			if tc.enabled && (result.GeneratedImages[0].ID != "ig_1" || result.GeneratedImages[0].MIMEType != "image/png") {
				t.Errorf("image = %+v", result.GeneratedImages[0])
			}
			if tc.enabled && !tc.complete {
				found := false
				for _, c := range chunks {
					if c.GeneratedImage != nil && c.GeneratedImage.ID == "ig_1" {
						found = true
					}
				}
				if !found {
					t.Error("no image streamed")
				}
			}
		})
	}
}
