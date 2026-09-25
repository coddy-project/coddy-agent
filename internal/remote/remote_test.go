package remote

// Edge and error cases for the remote client. The happy paths live in
// features/remote_client.feature (godog harness in external/httpserver).

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// collectSender records updates and scripts prompt answers.
type collectSender struct {
	mu      sync.Mutex
	updates []interface{}
	answers [][]string
}

func (c *collectSender) SendSessionUpdate(_ string, u interface{}) error {
	c.mu.Lock()
	c.updates = append(c.updates, u)
	c.mu.Unlock()
	return nil
}

func (c *collectSender) RequestPermission(context.Context, acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	return &acp.PermissionResult{Outcome: "allow", OptionID: "allow"}, nil
}

func (c *collectSender) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return &acp.QuestionResult{Answers: c.answers}, nil
}

func (c *collectSender) texts() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []string
	for _, u := range c.updates {
		if chunk, ok := u.(acp.MessageChunkUpdate); ok {
			out = append(out, chunk.Content.Type+":"+chunk.Content.Text)
		}
	}
	return out
}

// blockingPermissionSender blocks RequestPermission until its context dies,
// mimicking a modal the operator never answers.
type blockingPermissionSender struct {
	collectSender
	started chan struct{}
	once    sync.Once
}

func (b *blockingPermissionSender) RequestPermission(ctx context.Context, _ acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	b.once.Do(func() { close(b.started) })
	<-ctx.Done()
	return &acp.PermissionResult{Outcome: "cancelled", OptionID: "reject"}, nil
}

// ---- readSSE ----

func TestReadSSEParsesNamedEventsMultilineDataAndSkipsNoise(t *testing.T) {
	stream := "id: 1\n" +
		": keepalive\n" +
		"event: tool_call\n" +
		"data: {\"a\":1,\n" +
		"data: \"b\":2}\n" +
		"\n" +
		"data: [DONE]\n\n"
	var frames []sseFrame
	err := readSSE(strings.NewReader(stream), func(f sseFrame) error {
		frames = append(frames, f)
		return nil
	})
	if err != nil {
		t.Fatalf("readSSE: %v", err)
	}
	if len(frames) != 2 {
		t.Fatalf("frames: %#v", frames)
	}
	if frames[0].event != "tool_call" || frames[0].data != "{\"a\":1,\n\"b\":2}" {
		t.Fatalf("first frame: %#v", frames[0])
	}
	if frames[1].event != "" || frames[1].data != "[DONE]" {
		t.Fatalf("second frame: %#v", frames[1])
	}
}

func TestReadSSEDiscardsUnterminatedFramesAtEOF(t *testing.T) {
	// Per the SSE spec an event without its terminating blank line is
	// dropped: a truncated [DONE] must not pass for a clean completion.
	var frames []sseFrame
	err := readSSE(strings.NewReader("data: [DONE]"), func(f sseFrame) error {
		frames = append(frames, f)
		return nil
	})
	if err != nil || len(frames) != 0 {
		t.Fatalf("err=%v frames=%#v", err, frames)
	}
}

// ---- Resolve ----

func TestResolveMapsNamesAddressesAndTokens(t *testing.T) {
	cfg := &config.Config{HTTPServer: config.HTTPServerConfig{Remotes: []config.HTTPRemote{
		{Name: "nas02", URL: "http://nas02:19980/"},
		{Name: "broken", URL: ""},
	}}}
	t.Setenv(TokenEnvVar, "env-token")

	opts, err := Resolve(cfg, "nas02", "")
	if err != nil || opts == nil {
		t.Fatalf("named remote: %v %v", opts, err)
	}
	if opts.BaseURL != "http://nas02:19980" || opts.Token != "env-token" {
		t.Fatalf("named remote resolved to %+v", opts)
	}

	opts, err = Resolve(cfg, "box.example:19980", "flag-token")
	if err != nil || opts.BaseURL != "http://box.example:19980" || opts.Token != "flag-token" {
		t.Fatalf("bare host: %+v %v", opts, err)
	}

	opts, err = Resolve(cfg, "https://box.example/", "")
	if err != nil || opts.BaseURL != "https://box.example" {
		t.Fatalf("https url: %+v %v", opts, err)
	}

	if opts, err = Resolve(cfg, "", "ignored"); err != nil || opts != nil {
		t.Fatalf("empty remote must resolve to local mode, got %+v %v", opts, err)
	}
	if _, err = Resolve(cfg, "broken", ""); err == nil {
		t.Fatal("configured remote without url must fail")
	}
	if _, err = Resolve(cfg, "ftp://box", ""); err == nil {
		t.Fatal("non-http scheme must fail")
	}
	if _, err = Resolve(cfg, "http://box:1/?access_token=x", ""); err == nil {
		t.Fatal("query strings must be rejected")
	}
	if _, err = Resolve(cfg, "http://user:pass@box:1", ""); err == nil {
		t.Fatal("credentials in the URL must be rejected")
	}

	opts, err = Resolve(cfg, "box.example:19980", "t")
	if err != nil || !opts.Insecure {
		t.Fatalf("plain http off-box must flag Insecure, got %+v %v", opts, err)
	}
	opts, err = Resolve(cfg, "127.0.0.1:19980", "t")
	if err != nil || opts.Insecure {
		t.Fatalf("loopback must not flag Insecure, got %+v %v", opts, err)
	}
	opts, err = Resolve(cfg, "https://box.example:19980", "t")
	if err != nil || opts.Insecure {
		t.Fatalf("https must not flag Insecure, got %+v %v", opts, err)
	}
}

// ---- session ids ----

func TestNewRemoteSessionIDsAreValidFolderIDs(t *testing.T) {
	seen := map[string]bool{}
	for range 32 {
		id := session.NewSessionID()
		if err := session.ValidateFolderSessionID(id); err != nil {
			t.Fatalf("generated id %q invalid: %v", id, err)
		}
		if seen[id] {
			t.Fatalf("duplicate id %q", id)
		}
		seen[id] = true
	}
}

// ---- prompt error surfaces ----

func promptOnce(t *testing.T, srv *httptest.Server, sender acp.UpdateSender) (*acp.SessionPromptResult, error) {
	t.Helper()
	h, err := NewHandler(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	h.SetServer(sender)
	return h.HandleSessionPromptWithSender(context.Background(), acp.SessionPromptParams{
		SessionID: "sess_test",
		Prompt:    []acp.ContentBlock{{Type: "text", Text: "hi"}},
	}, sender, nil)
}

func TestPromptSurfacesUnauthorizedAsAFriendlyError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="coddy"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()
	_, err := promptOnce(t, srv, &collectSender{})
	if err == nil || !strings.Contains(err.Error(), "unauthorized (check --remote-token") {
		t.Fatalf("err = %v", err)
	}
}

func TestPromptSurfacesBusySessionsAndErrorFrames(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":{"message":"session busy: another agent turn is in progress"}}`))
	}))
	defer srv.Close()
	_, err := promptOnce(t, srv, &collectSender{})
	if err == nil || !strings.Contains(err.Error(), "busy") {
		t.Fatalf("409 err = %v", err)
	}

	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"error\":{\"message\":\"model exploded\"}}\n\ndata: [DONE]\n\n"))
	}))
	defer srv2.Close()
	_, err = promptOnce(t, srv2, &collectSender{})
	if err == nil || !strings.Contains(err.Error(), "model exploded") {
		t.Fatalf("error frame err = %v", err)
	}
}

func TestPromptRejectsStreamsThatEndWithoutDone(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":{\"content\":\"part\"}}]}\n\n"))
	}))
	defer srv.Close()
	sender := &collectSender{}
	_, err := promptOnce(t, srv, sender)
	if err == nil || !strings.Contains(err.Error(), "before [DONE]") {
		t.Fatalf("err = %v", err)
	}
	if texts := sender.texts(); len(texts) != 1 || texts[0] != "text:part" {
		t.Fatalf("partial text should still have streamed, got %v", texts)
	}
}

func TestPromptStreamsReasoningDeltasAsReasoningChunks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":{\"reasoning_content\":\"thinking\"}}]}\n\n" +
			"data: {\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":{\"content\":\"answer\"}}]}\n\n" +
			"data: [DONE]\n\n"))
	}))
	defer srv.Close()
	sender := &collectSender{}
	res, err := promptOnce(t, srv, sender)
	if err != nil || res.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	texts := sender.texts()
	if len(texts) != 2 || texts[0] != "reasoning:thinking" || texts[1] != "text:answer" {
		t.Fatalf("texts = %v", texts)
	}
}

func TestQuestionEventsRoundTripThroughTheAnswerEndpoint(t *testing.T) {
	var answered struct {
		RequestID string     `json:"requestId"`
		Answers   [][]string `json:"answers"`
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/responses", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: question\n" +
			"data: {\"sessionId\":\"sess_test\",\"requestId\":\"q_1\",\"questions\":[{\"question\":\"Pick\",\"options\":[{\"label\":\"A\"}]}]}\n\n" +
			"data: [DONE]\n\n"))
	})
	mux.HandleFunc("POST /coddy/sessions/{id}/question", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&answered)
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	sender := &collectSender{answers: [][]string{{"A"}}}
	if _, err := promptOnce(t, srv, sender); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if answered.RequestID != "q_1" || len(answered.Answers) != 1 || answered.Answers[0][0] != "A" {
		t.Fatalf("answer payload = %+v", answered)
	}
}

func TestCancelAbortsATurnBlockedOnAPermissionModal(t *testing.T) {
	// The stream delivers a permission event and then nothing: the reader is
	// blocked inside RequestPermission. session/cancel must abort the turn
	// context, unblock the round-trip, and report the cancelled stop reason.
	cancelled := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/responses", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: permission\n" +
			"data: {\"sessionId\":\"sess_test\",\"toolCall\":{\"toolCallId\":\"perm-1\",\"status\":\"pending\"},\"options\":[{\"optionId\":\"allow\",\"name\":\"Allow\",\"kind\":\"allow_once\"}]}\n\n"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	mux.HandleFunc("POST /coddy/sessions/{id}/cancel", func(w http.ResponseWriter, _ *http.Request) {
		close(cancelled)
		_, _ = w.Write([]byte(`{"object":"coddy.session_cancelled","id":"sess_test"}`))
	})
	mux.HandleFunc("POST /coddy/sessions/{id}/permission", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	h, err := NewHandler(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	sender := &blockingPermissionSender{started: make(chan struct{})}
	h.SetServer(sender)
	done := make(chan struct{})
	var res *acp.SessionPromptResult
	go func() {
		defer close(done)
		res, _ = h.HandleSessionPromptWithSender(context.Background(), acp.SessionPromptParams{
			SessionID: "sess_test",
			Prompt:    []acp.ContentBlock{{Type: "text", Text: "hi"}},
		}, sender, nil)
	}()
	select {
	case <-sender.started:
	case <-time.After(3 * time.Second):
		t.Fatal("permission round-trip never started")
	}
	h.HandleSessionCancel(acp.SessionCancelParams{SessionID: "sess_test"})
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not unblock the permission round-trip")
	}
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("the server never received the cancel request")
	}
	if res == nil || res.StopReason != acp.StopReasonCancelled {
		t.Fatalf("res = %+v", res)
	}
}

func TestServerStopReasonAndCancelledMetaWin(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: coddy_meta\ndata: {\"metadata\":{\"model\":\"m\",\"stop_reason\":\"max_turns\",\"stop_notice\":\"Stopped after 3 steps.\"}}\n\n" +
			"data: [DONE]\n\n"))
	}))
	defer srv.Close()
	sender := &collectSender{}
	res, err := promptOnce(t, srv, sender)
	if err != nil || res.StopReason != acp.StopReasonMaxTurns {
		t.Fatalf("res=%+v err=%v", res, err)
	}
	// The server's words for why the turn stopped reach the remote console.
	if res.StopNotice != "Stopped after 3 steps." {
		t.Fatalf("StopNotice = %q", res.StopNotice)
	}
}

func TestAStaleCancelDoesNotPoisonTheNextTurn(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/cancel") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"object\":\"chat.completion.chunk\",\"choices\":[{\"delta\":{\"content\":\"fine\"}}]}\n\ndata: [DONE]\n\n"))
	}))
	defer srv.Close()
	h, err := NewHandler(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	sender := &collectSender{}
	h.SetServer(sender)
	// Cancel while nothing runs, then run a clean turn.
	h.HandleSessionCancel(acp.SessionCancelParams{SessionID: "sess_test"})
	res, err := h.HandleSessionPromptWithSender(context.Background(), acp.SessionPromptParams{
		SessionID: "sess_test",
		Prompt:    []acp.ContentBlock{{Type: "text", Text: "hi"}},
	}, sender, nil)
	if err != nil || res.StopReason != acp.StopReasonEndTurn {
		t.Fatalf("res=%+v err=%v", res, err)
	}
}

func TestAFailedPermissionAnswerFailsTheTurnInsteadOfDeadlocking(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/responses", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("event: permission\n" +
			"data: {\"sessionId\":\"sess_test\",\"toolCall\":{\"toolCallId\":\"perm-1\",\"status\":\"pending\"},\"options\":[]}\n\n"))
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	mux.HandleFunc("POST /coddy/sessions/{id}/permission", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":{"message":"boom"}}`, http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	sender := &collectSender{}
	_, err := promptOnce(t, srv, sender)
	if err == nil || !strings.Contains(err.Error(), "permission answer") {
		t.Fatalf("err = %v", err)
	}
}

// An editor's embedded resource reaches the server as an attachment whose
// literal body is the resource text, never as text inside the input: the
// server resolves the "@" references of what was typed and must not scan a
// file's contents for them. A resource_link names something on the editor's
// machine and travels as the link it is.
func TestResourceBlocksReachTheRemoteAsAttachments(t *testing.T) {
	var got struct {
		Input       string `json:"input"`
		Attachments []struct {
			Path   string `json:"path"`
			Source struct {
				Literal string `json:"literal"`
			} `json:"source"`
		} `json:"attachments"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer srv.Close()
	h, err := NewHandler(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	sender := &collectSender{}
	h.SetServer(sender)
	_, err = h.HandleSessionPromptWithSender(context.Background(), acp.SessionPromptParams{
		SessionID: "sess_test",
		Prompt: []acp.ContentBlock{
			{Type: "text", Text: "explain this"},
			{Type: "resource", Resource: &acp.Resource{URI: "file:///a.go", Text: "package a // @/etc/passwd"}},
			{Type: "resource_link", URI: "file:///b/", Name: "b"},
		},
	}, sender, nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got.Input, "package a") || !strings.HasPrefix(got.Input, "explain this") || !strings.Contains(got.Input, "[b](file:///b/)") {
		t.Fatalf("input %q", got.Input)
	}
	if len(got.Attachments) != 1 || got.Attachments[0].Path != "file:///a.go" || got.Attachments[0].Source.Literal != "package a // @/etc/passwd" {
		t.Fatalf("attachments %+v", got.Attachments)
	}
}

func TestPreferredReopenPropagatesServerFailures(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/messages") {
			http.Error(w, `{"error":{"message":"disk on fire"}}`, http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"remote/alpha","default":true,"owned_by":"r"}]}`))
	}))
	defer srv.Close()
	h, err := NewHandler(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	h.SetServer(&collectSender{})
	h.SetPreferredSessionID("sess_existing")
	if _, err := h.HandleSessionNew(context.Background(), acp.SessionNewParams{}); err == nil || !strings.Contains(err.Error(), "disk on fire") {
		t.Fatalf("err = %v", err)
	}
}

// The server marks the row a session with no model of its own runs on, and
// that is the model the client shows and checks reasoning levels against - not
// the first backend it lists.
func TestRemoteDefaultModelIsTheRowTheServerMarks(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"object":"list","data":[
			{"id":"agent","owned_by":"coddy"},
			{"id":"remote/alpha","owned_by":"remote"},
			{"id":"remote/beta","owned_by":"remote","default":true}]}`))
	}))
	defer srv.Close()
	h, err := NewHandler(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if err := h.ensureModels(context.Background()); err != nil {
		t.Fatal(err)
	}
	if h.defModel != "remote/beta" {
		t.Fatalf("default model = %q, want the row the server marks", h.defModel)
	}
}

// ---- config options ----

func TestSetConfigOptionValidatesModelsAndCarriesThePermissionMode(t *testing.T) {
	var input string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/models", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"object":"list","data":[
			{"id":"agent","owned_by":"coddy"},
			{"id":"remote/alpha","default":true,"owned_by":"remote"}]}`))
	})
	// The session does not exist on the server until its first prompt.
	mux.HandleFunc("PATCH /coddy/sessions/{id}", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":{"message":"session not found"}}`, http.StatusNotFound)
	})
	mux.HandleFunc("POST /v1/responses", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input string `json:"input"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		input = body.Input
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	h, err := NewHandler(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	h.SetServer(&collectSender{})
	if _, err := h.HandleSessionSetConfigOption(context.Background(), acp.SessionSetConfigOptionParams{
		SessionID: "sess_x", ConfigID: "model", Value: "remote/unknown",
	}); err == nil {
		t.Fatal("unknown model must fail")
	}
	// The server's setter decides; a session the server has not created yet
	// holds the change and sends it as a command with its first prompt.
	if _, err := h.HandleSessionSetConfigOption(context.Background(), acp.SessionSetConfigOptionParams{
		SessionID: "sess_x", ConfigID: "permission_mode", Value: "bypass",
	}); err != nil {
		t.Fatalf("permission mode: %v", err)
	}
	sender := &collectSender{}
	if _, err := h.HandleSessionPromptWithSender(context.Background(), acp.SessionPromptParams{
		SessionID: "sess_x", Prompt: []acp.ContentBlock{{Type: "text", Text: "go"}},
	}, sender, nil); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if input != "/permissions bypass\ngo" {
		t.Fatalf("first prompt input = %q, want the held command before it", input)
	}
	res, err := h.HandleSessionSetConfigOption(context.Background(), acp.SessionSetConfigOptionParams{
		SessionID: "sess_x", ConfigID: "model", Value: "remote/alpha",
	})
	if err != nil {
		t.Fatalf("known model: %v", err)
	}
	found := false
	for _, opt := range res.ConfigOptions {
		if opt.ID == "model" && opt.CurrentValue == "remote/alpha" {
			found = true
		}
	}
	if !found {
		t.Fatalf("model option not updated: %+v", res.ConfigOptions)
	}
}

func TestRemoteReasoningConfigOptionPersistsAndRestores(t *testing.T) {
	const sessionID = "sess_x"
	var patches []map[string]string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/models", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"object":"list","data":[
			{"id":"agent","owned_by":"coddy"},
			{"id":"remote/terra","default":true,"owned_by":"remote","reasoning_levels":["minimal","low","medium","high"],"reasoning_default":"medium"}]}`))
	})
	mux.HandleFunc("GET /coddy/sessions/{id}/messages", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"messages":[],"selectedModelId":"remote/terra","selectedReasoning":"low"}`))
	})
	mux.HandleFunc("PATCH /coddy/sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
		var patch map[string]string
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			t.Errorf("decode patch: %v", err)
		}
		patches = append(patches, patch)
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	h, err := NewHandler(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	h.SetServer(&collectSender{})
	h.SetPreferredSessionID(sessionID)
	newResult, err := h.HandleSessionNew(context.Background(), acp.SessionNewParams{})
	if err != nil {
		t.Fatalf("HandleSessionNew: %v", err)
	}
	assertRemoteReasoningOption(t, newResult.ConfigOptions, "low")

	setResult, err := h.HandleSessionSetConfigOption(context.Background(), acp.SessionSetConfigOptionParams{
		SessionID: sessionID, ConfigID: "reasoning", Value: " high ",
	})
	if err != nil {
		t.Fatalf("set reasoning: %v", err)
	}
	assertRemoteReasoningOption(t, setResult.ConfigOptions, "high")
	if len(patches) != 1 || patches[0]["selectedReasoning"] != "high" || len(patches[0]) != 1 {
		t.Fatalf("patches = %#v, want selectedReasoning high only", patches)
	}
	cleared, err := h.HandleSessionSetConfigOption(context.Background(), acp.SessionSetConfigOptionParams{
		SessionID: sessionID, ConfigID: "reasoning", Value: "  ",
	})
	if err != nil {
		t.Fatalf("clear reasoning: %v", err)
	}
	assertRemoteReasoningOption(t, cleared.ConfigOptions, "medium")
	if len(patches) != 2 || patches[1]["selectedReasoning"] != "" || len(patches[1]) != 1 {
		t.Fatalf("clear patches = %#v, want selectedReasoning empty only", patches)
	}

	if _, err := h.HandleSessionSetConfigOption(context.Background(), acp.SessionSetConfigOptionParams{
		SessionID: sessionID, ConfigID: "reasoning", Value: "ultra",
	}); err == nil || !strings.Contains(err.Error(), "unknown reasoning value") {
		t.Fatalf("invalid reasoning error = %v", err)
	}
	if len(patches) != 2 {
		t.Fatalf("invalid reasoning PATCHed: %#v", patches)
	}

	loaded, err := h.HandleSessionLoad(context.Background(), acp.SessionLoadParams{SessionID: sessionID})
	if err != nil {
		t.Fatalf("HandleSessionLoad: %v", err)
	}
	assertRemoteReasoningOption(t, loaded.ConfigOptions, "low")
}

func TestRemoteReasoningConfigOptionClampsAfterModelSwitch(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/models", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"object":"list","data":[
			{"id":"remote/alpha","default":true,"owned_by":"remote","reasoning_levels":["minimal","high"],"reasoning_default":"minimal"},
			{"id":"remote/beta","owned_by":"remote","reasoning_levels":["minimal","low"],"reasoning_default":"minimal"}]}`))
	})
	mux.HandleFunc("PATCH /coddy/sessions/{id}", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	h, err := NewHandler(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.HandleSessionSetConfigOption(context.Background(), acp.SessionSetConfigOptionParams{
		SessionID: "sess_x", ConfigID: "reasoning", Value: "high",
	}); err != nil {
		t.Fatalf("set reasoning: %v", err)
	}
	result, err := h.HandleSessionSetConfigOption(context.Background(), acp.SessionSetConfigOptionParams{
		SessionID: "sess_x", ConfigID: "model", Value: "remote/beta",
	})
	if err != nil {
		t.Fatalf("switch model: %v", err)
	}
	assertRemoteReasoningOptionLevels(t, result.ConfigOptions, "minimal", []string{"minimal", "low"})
}

func TestRemoteReasoningConfigOptionRetainsSelectionBeforeFirstPrompt(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/models", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"remote/terra","default":true,"owned_by":"remote","reasoning_levels":["low","high"],"reasoning_default":"low"}]}`))
	})
	mux.HandleFunc("PATCH /coddy/sessions/{id}", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":{"message":"session not found"}}`, http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	h, err := NewHandler(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	sender := &collectSender{}
	h.SetServer(sender)
	result, err := h.HandleSessionSetConfigOption(context.Background(), acp.SessionSetConfigOptionParams{
		SessionID: "sess_fresh", ConfigID: "reasoning", Value: "high",
	})
	if err != nil {
		t.Fatalf("set reasoning before first prompt: %v", err)
	}
	assertRemoteReasoningOptionLevels(t, result.ConfigOptions, "high", []string{"low", "high"})
	assertRemoteReasoningOptionLevels(t, h.configOptions(h.session("sess_fresh")), "high", []string{"low", "high"})

	sender.mu.Lock()
	defer sender.mu.Unlock()
	for _, update := range sender.updates {
		if optionUpdate, ok := update.(acp.ConfigOptionUpdate); ok {
			assertRemoteReasoningOptionLevels(t, optionUpdate.ConfigOptions, "high", []string{"low", "high"})
			return
		}
	}
	t.Fatalf("config option update missing: %#v", sender.updates)
}

func TestRemotePromptSendsPrePromptReasoningSelection(t *testing.T) {
	var request responsesRequest
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/models", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"remote/terra","default":true,"owned_by":"remote","reasoning_levels":["low","high"],"reasoning_default":"low"}]}`))
	})
	mux.HandleFunc("PATCH /coddy/sessions/{id}", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":{"message":"session not found"}}`, http.StatusNotFound)
	})
	mux.HandleFunc("POST /v1/responses", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("decode responses request: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	h, err := NewHandler(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	sender := &collectSender{}
	h.SetServer(sender)
	if _, err := h.HandleSessionSetConfigOption(context.Background(), acp.SessionSetConfigOptionParams{
		SessionID: "sess_fresh", ConfigID: "reasoning", Value: "high",
	}); err != nil {
		t.Fatalf("set reasoning before first prompt: %v", err)
	}
	if _, err := h.HandleSessionPromptWithSender(context.Background(), acp.SessionPromptParams{
		SessionID: "sess_fresh",
		Prompt:    []acp.ContentBlock{{Type: "text", Text: "hi"}},
	}, sender, nil); err != nil {
		t.Fatalf("first prompt: %v", err)
	}
	if request.Metadata["reasoning"] != "high" {
		t.Fatalf("response metadata = %#v, want reasoning high", request.Metadata)
	}
}

func assertRemoteReasoningOption(t *testing.T, options []acp.ConfigOption, current string) {
	t.Helper()
	for _, option := range options {
		if option.ID != "reasoning" {
			continue
		}
		if option.Type != "select" || option.CurrentValue != current {
			t.Fatalf("reasoning option = %+v, want select with current %q", option, current)
		}
		want := []string{"minimal", "low", "medium", "high"}
		if len(option.Options) != len(want) {
			t.Fatalf("reasoning options = %+v, want %v", option.Options, want)
		}
		for i, value := range want {
			if option.Options[i].Value != value {
				t.Fatalf("reasoning option %d = %q, want %q", i, option.Options[i].Value, value)
			}
		}
		return
	}
	t.Fatalf("reasoning config option missing: %+v", options)
}

func TestRemoteReasoningPatchFailureDoesNotUpdateLocalState(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/models", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"remote/terra","default":true,"owned_by":"remote","reasoning_levels":["low","high"],"reasoning_default":"low"}]}`))
	})
	mux.HandleFunc("PATCH /coddy/sessions/{id}", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":{"message":"disk on fire"}}`, http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	h, err := NewHandler(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.HandleSessionSetConfigOption(context.Background(), acp.SessionSetConfigOptionParams{
		SessionID: "sess_x", ConfigID: "reasoning", Value: "high",
	}); err == nil || !strings.Contains(err.Error(), "disk on fire") {
		t.Fatalf("reasoning PATCH error = %v", err)
	}
	assertRemoteReasoningOptionLevels(t, h.configOptions(h.session("sess_x")), "low", []string{"low", "high"})
}

func assertRemoteReasoningOptionLevels(t *testing.T, options []acp.ConfigOption, current string, levels []string) {
	t.Helper()
	for _, option := range options {
		if option.ID != "reasoning" {
			continue
		}
		if option.CurrentValue != current {
			t.Fatalf("reasoning current = %q, want %q", option.CurrentValue, current)
		}
		if len(option.Options) != len(levels) {
			t.Fatalf("reasoning options = %+v, want %v", option.Options, levels)
		}
		for i, level := range levels {
			if option.Options[i].Value != level {
				t.Fatalf("reasoning option %d = %q, want %q", i, option.Options[i].Value, level)
			}
		}
		return
	}
	t.Fatalf("reasoning config option missing: %+v", options)
}

func TestHandleSessionNewFailsFastWhenTheRemoteIsUnreachable(t *testing.T) {
	h, err := NewHandler(Options{BaseURL: "http://127.0.0.1:1"})
	if err != nil {
		t.Fatal(err)
	}
	h.SetServer(&collectSender{})
	if _, err := h.HandleSessionNew(context.Background(), acp.SessionNewParams{}); err == nil {
		t.Fatal("unreachable remote must fail session/new")
	} else if !strings.Contains(err.Error(), "remote coddy") {
		t.Fatalf("err = %v", err)
	}
}

func TestNewHandlerRequiresABaseURL(t *testing.T) {
	if _, err := NewHandler(Options{}); err == nil {
		t.Fatal("empty base URL must fail")
	}
	h, err := NewHandler(Options{BaseURL: " http://box:1/ "})
	if err != nil {
		t.Fatal(err)
	}
	if h.BaseURL() != "http://box:1" {
		t.Fatalf("base = %q", h.BaseURL())
	}
}

func TestRemoteErrorTruncatesLongOpaqueBodies(t *testing.T) {
	h, _ := NewHandler(Options{BaseURL: "http://box"})
	res := &http.Response{StatusCode: http.StatusBadGateway, Status: "502 Bad Gateway"}
	err := h.remoteError(res, []byte(strings.Repeat("x", 500)))
	if err == nil || len(err.Error()) > 260 {
		t.Fatalf("err = %v (len %d)", err, len(fmt.Sprint(err)))
	}
}

// A 409 is not always the turn lock: the ask-mode refusal of a plan run uses
// the same status with an actionable message that must reach the operator.
func TestPromptPassesThroughAskModeRefusalOn409(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write([]byte(`{"error":{"message":"plan cannot be run in ask mode: switch to agent mode first"}}`))
	}))
	defer srv.Close()
	_, err := promptOnce(t, srv, &collectSender{})
	if err == nil || !strings.Contains(err.Error(), "ask mode") {
		t.Fatalf("409 refusal was not passed through: %v", err)
	}
	if strings.Contains(err.Error(), "busy") {
		t.Fatalf("409 refusal was mistaken for the turn lock: %v", err)
	}
}

// A permission answer the server refuses with 404 (the prompt was withdrawn:
// a subagent's relayed prompt after the child stopped) or 409 is stale, not
// fatal: the client keeps reading the stream and the turn completes.
func TestAStaleAnswerToAWithdrawnPromptKeepsTheTurnAlive(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusConflict} {
		mux := http.NewServeMux()
		mux.HandleFunc("POST /v1/responses", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: permission\n" +
				`data: {"sessionId":"s1","toolCall":{"toolCallId":"child-1","title":"[subagent explore] Run: run_command","status":"pending"},"options":[{"optionId":"allow","name":"Allow","kind":"allow_once"}]}` + "\n\n" +
				`data: {"choices":[{"delta":{"content":"after the withdrawn prompt"}}]}` + "\n\n" +
				"data: [DONE]\n\n"))
		})
		mux.HandleFunc("POST /coddy/sessions/{id}/permission", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, `{"error":{"message":"no pending permission for this toolCallId"}}`, status)
		})
		srv := httptest.NewServer(mux)
		sender := &collectSender{}
		res, err := promptOnce(t, srv, sender)
		srv.Close()
		if err != nil {
			t.Fatalf("status %d: a stale permission answer failed the turn: %v", status, err)
		}
		if res == nil || res.StopReason != acp.StopReasonEndTurn {
			t.Fatalf("status %d: result = %+v", status, res)
		}
		if got := strings.Join(sender.texts(), ""); !strings.Contains(got, "after the withdrawn prompt") {
			t.Fatalf("status %d: the stream was not read past the stale answer: %q", status, got)
		}
	}
}

// An id the server does not know still starts a fresh remote session: every
// session id has the same shape now, so nothing about one says it belongs to a
// transcript the client may not write to.
func TestRemoteSessionNewAcceptsAnUnknownPreferredID(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/models", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"agent","owned_by":"coddy"},{"id":"remote/alpha","owned_by":"remote"}]}`))
	})
	mux.HandleFunc("GET /coddy/sessions/{id}/messages", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":{"message":"session not found"}}`, http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	h, err := NewHandler(Options{BaseURL: srv.URL, Log: slog.Default()})
	if err != nil {
		t.Fatal(err)
	}
	h.SetServer(&collectSender{})
	h.SetPreferredSessionID("sess_fresh_remote")
	if _, err := h.HandleSessionNew(context.Background(), acp.SessionNewParams{}); err != nil {
		t.Fatalf("an unknown preferred id must be accepted: %v", err)
	}
}

// TestSessionListForwardsTheWorkspaceFilter: an editor lists the sessions of
// the folder it has open, so the cwd it sends has to reach the server instead
// of being dropped (which listed every session on the server).
func TestSessionListForwardsTheWorkspaceFilter(t *testing.T) {
	var gotCWD string
	mux := http.NewServeMux()
	mux.HandleFunc("GET /coddy/sessions", func(w http.ResponseWriter, r *http.Request) {
		gotCWD = r.URL.Query().Get("cwd")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"sessions":[{"id":"sess_ws","cwd":"/work/project","title":"one"}]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	h, err := NewHandler(Options{BaseURL: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	cwd := "/work/project"
	res, err := h.HandleSessionList(context.Background(), acp.SessionListParams{CWD: &cwd})
	if err != nil {
		t.Fatal(err)
	}
	if gotCWD != cwd {
		t.Fatalf("server received cwd=%q, want %q", gotCWD, cwd)
	}
	if len(res.Sessions) != 1 || res.Sessions[0].SessionID != "sess_ws" || res.Sessions[0].CWD != cwd {
		t.Fatalf("sessions = %+v", res.Sessions)
	}
}
