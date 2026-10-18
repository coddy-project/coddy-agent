package llm

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"
)

// The prompt cache of the providers rests on a byte-stable prefix, and the
// local agent already keeps it stable. A remote model must not disturb it: what
// the remote's provider receives has to be what a local provider of the same
// type would have received (plan 4.6).

// richHistory is a history that exercises every field a provider reads and
// every field that stays local.
func richHistory() ([]Message, []ToolDefinition) {
	tools := []ToolDefinition{
		{Name: "get_weather", Description: "Weather of a city", InputSchema: map[string]any{
			"type": "object", "required": []any{"city"},
			"properties": map[string]any{"city": map[string]any{"type": "string"}, "unit": map[string]any{"type": "string", "enum": []any{"c", "f"}}},
		}},
		{Name: "read_file", Description: "Read a file", InputSchema: map[string]any{"type": "object", "properties": map[string]any{"path": map[string]any{"type": "string"}}}},
	}
	msgs := []Message{
		{Role: RoleSystem, Content: "You are the local harness.\nRules: be brief.", CreatedAt: "2026-10-07T10:00:00Z"},
		{Role: RoleUser, Content: "Weather in Paris? And look at this.", CreatedAt: "2026-10-07T10:00:01Z",
			ImageParts: []ImagePart{
				{DataURL: "data:image/png;base64,iVBORw0KGgo=", MIMEType: "image/png", Name: "square.png", FilePath: "/home/me/.coddy/s/assets/square.png", ThumbnailPath: "/home/me/.coddy/s/t.png", Size: 8},
				{DataURL: "data:text/plain;base64,aGVsbG8gd29ybGQ=", MIMEType: "text/plain", Name: "note.txt", FilePath: "/home/me/note.txt", Size: 11},
			}},
		{Role: RoleAssistant, Content: "Let me check.", Reasoning: "The user wants the weather.", ReasoningSignature: "sig-abc/+=", ReasoningDurationMs: 1234,
			ToolCalls: []ToolCall{{ID: "call_weather_1", Name: "get_weather", InputJSON: `{"city":"Paris","unit":"c"}`}}, Model: "remote/coder", CreatedAt: "2026-10-07T10:00:02Z"},
		{Role: RoleTool, Content: "18°C, sunny", ToolCallID: "call_weather_1", Rules: "", CreatedAt: "2026-10-07T10:00:03Z",
			Artifacts: []Artifact{{ID: "a1", Name: "report.txt", SourcePath: "/home/me/report.txt"}}},
		{Role: RoleAssistant, Content: "It is 18°C in Paris.", Model: "remote/coder", CompactionSummary: false},
		{Role: RoleUser, Content: "Thanks, and now Rome?", BackgroundWake: &BackgroundWake{Tasks: []BackgroundWakeTask{{ID: "bg_1", Status: "succeeded"}}}},
	}
	return msgs, tools
}

// clearLocalOnly is what a local provider of any type effectively reads of a
// message: the local-only fields do not exist for it.
func clearLocalOnly(msgs []Message) []Message {
	out := make([]Message, 0, len(msgs))
	for _, m := range msgs {
		m.Artifacts, m.ReasoningDurationMs, m.Model, m.CreatedAt = nil, 0, "", ""
		m.PlanDocument, m.CompactionSummary, m.BackgroundWake, m.Rules = nil, false, nil, ""
		var parts []ImagePart
		for _, ip := range m.ImageParts {
			ip.FilePath, ip.ThumbnailPath, ip.Size = "", "", 0
			parts = append(parts, ip)
		}
		m.ImageParts = parts
		out = append(out, m)
	}
	return out
}

// throughTheHop is the path a call takes to the remote's provider: sealed on
// the way out of the remote earlier, projected onto the wire and back, and the
// signatures opened for the model the alias points at now.
func throughTheHop(t *testing.T, msgs []Message, tools []ToolDefinition, historyTag, currentTag string) ([]Message, []ToolDefinition) {
	t.Helper()
	// The local harness stores what the remote sent: sealed signatures.
	stored := append([]Message(nil), msgs...)
	for i := range stored {
		stored[i].ReasoningSignature = SealReasoningSignature(stored[i].ReasoningSignature, historyTag)
	}
	req := viaJSON(t, WireRequest{Protocol: CoddyProtocol, Model: "coder", Messages: WireMessagesFromLLM(stored), Tools: WireToolsFromLLM(tools)})
	got := WireMessagesToLLM(req.Messages)
	OpenMessageSignatures(got, currentTag)
	return got, WireToolsToLLM(req.Tools)
}

func TestCoddyProjectionEqualityWhatTheRemoteProviderReceivesIsWhatALocalOneWould(t *testing.T) {
	msgs, tools := richHistory()
	key := []byte("remote-secret")
	tag := ModelTag(key, "anthropic/claude-x")

	gotMsgs, gotTools := throughTheHop(t, msgs, tools, tag, tag)

	if want := clearLocalOnly(msgs); !reflect.DeepEqual(gotMsgs, want) {
		t.Fatalf("messages differ\n got: %+v\nwant: %+v", gotMsgs, want)
	}
	if !reflect.DeepEqual(gotTools, tools) {
		t.Fatalf("tools differ\n got: %+v\nwant: %+v", gotTools, tools)
	}
	if gotMsgs[2].ReasoningSignature != "sig-abc/+=" {
		t.Fatalf("the remote model receives a valid signature unchanged, got %q", gotMsgs[2].ReasoningSignature)
	}
}

func TestCoddyProjectionDropsASignatureOfAnotherModelAndNothingElse(t *testing.T) {
	msgs, tools := richHistory()
	key := []byte("remote-secret")
	older := ModelTag(key, "anthropic/claude-old")
	current := ModelTag(key, "anthropic/claude-new")

	gotMsgs, _ := throughTheHop(t, msgs, tools, older, current)

	want := clearLocalOnly(msgs)
	want[2].ReasoningSignature = ""
	if !reflect.DeepEqual(gotMsgs, want) {
		t.Fatalf("only the signature goes\n got: %+v\nwant: %+v", gotMsgs, want)
	}
}

func TestCoddyProjectionOfAResponseSignatureRoundTripsThroughTheEnvelope(t *testing.T) {
	tag := ModelTag([]byte("k"), "m")
	resp := &Response{Content: "x", Reasoning: "r", ReasoningSignature: "raw-sig", StopReason: "end_turn"}
	sealed := SealResponseSignature(resp, tag)
	if resp.ReasoningSignature != "raw-sig" {
		t.Fatal("the provider's response was changed")
	}
	if sealed.ReasoningSignature == "raw-sig" || sealed.Content != "x" || sealed.StopReason != "end_turn" {
		t.Fatalf("sealed %+v", sealed)
	}
	back := viaJSON(t, WireFinalFromResponse(sealed)).ToResponse()
	msgs := []Message{{Role: RoleAssistant, Reasoning: back.Reasoning, ReasoningSignature: back.ReasoningSignature}}
	OpenMessageSignatures(msgs, tag)
	if msgs[0].ReasoningSignature != "raw-sig" {
		t.Fatalf("signature %q", msgs[0].ReasoningSignature)
	}
	if SealResponseSignature(nil, tag) != nil {
		t.Fatal("nil in, nil out")
	}
}

// upstreamBody captures the request body a provider sends upstream.
type upstreamBody struct {
	mu   sync.Mutex
	body []byte
}

func (u *upstreamBody) server(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		u.mu.Lock()
		if u.body == nil {
			u.body = b
		}
		u.mu.Unlock()
		http.Error(w, "stop here", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (u *upstreamBody) captured(t *testing.T) []byte {
	t.Helper()
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.body == nil {
		t.Fatal("the provider sent nothing upstream")
	}
	return u.body
}

// Body equality: the body the remote's provider sends upstream through a hop
// is, byte for byte, the body a local provider of the same type sends.
func TestCoddyProjectionBodyEqualityForTheProvidersThatCanBeCompared(t *testing.T) {
	msgs, tools := richHistory()
	tag := ModelTag([]byte("remote-secret"), "model")
	hopMsgs, hopTools := throughTheHop(t, msgs, tools, tag, tag)

	providers := map[string]func(t *testing.T, baseURL string) Provider{
		"openai": func(t *testing.T, base string) Provider {
			return newOpenAIProvider("gpt-x", "k", base, mustProviderClient(t), 4096, 0.3, "")
		},
		"neuraldeep": func(t *testing.T, base string) Provider {
			return newOpenAIProvider("hub-model", "k", base, mustProviderClient(t), 0, 0, "high")
		},
		"anthropic": func(t *testing.T, base string) Provider {
			return newAnthropicProvider("claude-x", "k", base, mustProviderClient(t), 8192, 0, "")
		},
		"anthropic with thinking": func(t *testing.T, base string) Provider {
			return newAnthropicProvider("claude-x", "k", base, mustProviderClient(t), 16384, 0, "high")
		},
		"codex": func(t *testing.T, base string) Provider {
			return newCodexTestProviderWith(t, base)
		},
	}
	for name, build := range providers {
		t.Run(name, func(t *testing.T) {
			var local, remote upstreamBody
			lp := build(t, local.server(t).URL)
			rp := build(t, remote.server(t).URL)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			_, _ = lp.Stream(ctx, msgs, tools, func(StreamChunk) {})
			_, _ = rp.Stream(ctx, hopMsgs, hopTools, func(StreamChunk) {})
			if l, r := local.captured(t), remote.captured(t); string(l) != string(r) {
				t.Fatalf("the upstream body changed on its way through the hop\nlocal:  %s\nremote: %s", l, r)
			}
		})
	}
}

// newCodexTestProviderWith is newCodexTestProvider with the HTTP client the
// providers get from NewProvider.
func newCodexTestProviderWith(t *testing.T, baseURL string) Provider {
	t.Helper()
	dir := t.TempDir()
	path := writeCodexAuth(t, dir, codexAuthFile{
		AuthMode: codexAuthModeChatGPT,
		Tokens:   codexTokens{AccessToken: makeJWT(time.Now().Add(time.Hour)), RefreshToken: "rt", AccountID: "acct-1"},
	})
	return newCodexProvider("gpt-5.6", path, true, baseURL, mustProviderClient(t), 0, "high")
}
