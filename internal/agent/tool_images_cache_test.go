package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image/color"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// A picture a read showed the model goes out with every later request of the
// session, so the bytes that carry it must be the same each time, or the
// provider's prompt cache is lost from that step on. Checked on the wire of
// the Anthropic provider: the step that read the picture, then a step that
// did not, then the answer.
func TestPromptCacheToolImagesKeepThePrefix(t *testing.T) {
	var mu sync.Mutex
	var requests []string
	// Each tool step is the call's start and its arguments, which the
	// Messages API streams as input_json_delta fragments.
	steps := [][2]string{
		{`{"type":"tool_use","id":"call_read","name":"read","input":{}}`, `{"path":"shot.png"}`},
		{`{"type":"tool_use","id":"call_glob","name":"glob","input":{}}`, `{"pattern":"*.png"}`},
		{"", ""},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		index := len(requests)
		requests = append(requests, string(body))
		mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		var out strings.Builder
		event := func(kind, data string) { fmt.Fprintf(&out, "event: %s\ndata: %s\n\n", kind, data) }
		event("message_start", `{"type":"message_start","message":{"id":"msg","type":"message","role":"assistant","model":"model","content":[],"usage":{"input_tokens":1,"output_tokens":0}}}`)
		stop := "end_turn"
		if step := steps[min(index, len(steps)-1)]; step[0] != "" {
			stop = "tool_use"
			args, _ := json.Marshal(step[1])
			event("content_block_start", `{"type":"content_block_start","index":0,"content_block":`+step[0]+`}`)
			event("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":`+string(args)+`}}`)
			event("content_block_stop", `{"type":"content_block_stop","index":0}`)
		} else {
			event("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
			event("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Red."}}`)
			event("content_block_stop", `{"type":"content_block_stop","index":0}`)
		}
		event("message_delta", fmt.Sprintf(`{"type":"message_delta","delta":{"stop_reason":%q},"usage":{"output_tokens":1}}`, stop))
		event("message_stop", `{"type":"message_stop"}`)
		_, _ = io.WriteString(w, out.String())
	}))
	defer srv.Close()

	cwd := t.TempDir()
	shot := testPNG(4, 3, color.NRGBA{R: 255, A: 255})
	if err := os.WriteFile(filepath.Join(cwd, "shot.png"), shot, 0o644); err != nil {
		t.Fatal(err)
	}
	stream, guard := true, false
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "fixture", Type: "anthropic", APIBase: srv.URL, APIKey: "fixture-only", Proxy: "none"}},
		Models:    []config.ModelEntry{{Model: "fixture/model", MaxTokens: 100, Stream: &stream, Multimodal: config.BoolPtr(true)}},
		Agent:     config.Agent{Model: "fixture/model", MaxTurns: 6, LoopGuard: &guard},
		Tools:     config.Tools{PermissionMode: config.PermModeBypass},
	}
	st := &session.State{ID: "sess_tool_images_cache", CWD: cwd, SessionDir: t.TempDir(), Mode: session.ModeAgent}
	ag := NewAgent(cfg, st, resumePermissionSender{}, slog.New(slog.DiscardHandler))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if stop, err := ag.Run(ctx, []acp.ContentBlock{{Type: "text", Text: "What colour is shot.png?"}}); err != nil || stop != string(acp.StopReasonEndTurn) {
		t.Fatalf("stop=%s err=%v", stop, err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 3 {
		t.Fatalf("requests = %d, want 3", len(requests))
	}
	_, afterRead := retryCacheRequest(t, requests[1])
	_, afterGlob := retryCacheRequest(t, requests[2])

	// The step that read the picture: its tool result, then the picture.
	want := base64.StdEncoding.EncodeToString(shot)
	var carried bool
	for i, raw := range afterRead {
		if !bytes.Contains(raw, []byte(`"tool_use_id":"call_read"`)) {
			continue
		}
		if i+1 >= len(afterRead) {
			t.Fatal("nothing follows the result of the read")
		}
		var next struct {
			Role    string
			Content []struct {
				Type   string
				Text   string
				Source struct{ Type, MediaType, Data string } `json:"source"`
			}
		}
		if err := json.Unmarshal(afterRead[i+1], &next); err != nil {
			t.Fatal(err)
		}
		if next.Role != "user" {
			t.Fatalf("the read's result is followed by a %s message", next.Role)
		}
		for _, c := range next.Content {
			if c.Type == "image" && c.Source.Data == want {
				carried = true
			}
		}
		if !carried || !strings.Contains(string(afterRead[i+1]), "shot.png") {
			t.Fatalf("the message after the read's result does not carry the picture: %s", afterRead[i+1])
		}
	}
	if !carried {
		t.Fatal("the request after the read has no result for it")
	}

	// The next request replays everything the previous one sent, byte for byte.
	if len(afterGlob) <= len(afterRead) {
		t.Fatalf("the history did not grow: %d then %d messages", len(afterRead), len(afterGlob))
	}
	for j, before := range afterRead {
		if !bytes.Equal(before, afterGlob[j]) {
			t.Fatalf("message %d changed between requests:\nbefore: %.300s\nafter:  %.300s", j, before, afterGlob[j])
		}
	}
}
