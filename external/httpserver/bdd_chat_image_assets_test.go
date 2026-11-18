//go:build http

package httpserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/agent"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/cucumber/godog"
)

type chatImageStand struct {
	server   *httptest.Server
	srv      *Server
	store    *session.FileStore
	png      []byte
	requests atomic.Int32
	stream   string
	asset    string
}

func newChatImageStand(t *testing.T, failed bool, terminalGate ...<-chan struct{}) *chatImageStand {
	t.Helper()
	stand := &chatImageStand{png: pngBytes(t)}
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Errorf("unexpected provider path %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		stand.requests.Add(1)
		var request struct {
			Tools []struct {
				Type string `json:"type"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		enabled := false
		for _, tool := range request.Tools {
			if tool.Type == "image_generation" {
				enabled = true
			}
		}
		if !enabled {
			t.Error("native image tool missing")
		}
		w.Header().Set("Content-Type", "text/event-stream")
		item := map[string]any{"type": "response.output_item.done", "item": map[string]any{"id": "ig_1", "type": "image_generation_call", "status": "completed", "result": base64.StdEncoding.EncodeToString(stand.png)}}
		data, _ := json.Marshal(item)
		_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
		if len(terminalGate) > 0 {
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			select {
			case <-terminalGate[0]:
			case <-r.Context().Done():
				return
			}
		}

		if failed {
			_, _ = fmt.Fprint(w, "data: {\"type\":\"error\",\"code\":\"server_error\",\"message\":\"overloaded\"}\n\n")
			return
		}
		_, _ = fmt.Fprint(w, "data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n")
	}))
	t.Cleanup(backend.Close)
	root := t.TempDir()
	cfg := &config.Config{
		Paths:     config.Paths{Home: filepath.Join(root, "home"), CWD: root},
		Providers: []config.ProviderConfig{{Name: "local", Type: "openai", APIBase: backend.URL, APIKey: "test", UsageLimitsPanel: new(bool)}},
		Models:    []config.ModelEntry{{Model: "local/gpt-test", MaxContextTokens: 128000, MaxTokens: 1000, ImageGeneration: true}},
		Agent:     config.Agent{Model: "local/gpt-test"},
	}
	cfg.Tools.PermissionMode = config.PermModeBypass
	log := slog.Default()
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		return agent.NewAgent(cfg, st, snd, log).Run(ctx, prompt)
	}
	stand.store = &session.FileStore{Root: filepath.Join(root, "sessions")}
	mgr := session.NewManager(cfg, noopSender{}, runner, log, root, stand.store)
	stand.srv = New(cfg, mgr, log, root)
	stand.server = httptest.NewServer(stand.srv.Handler())
	t.Cleanup(func() { stand.server.Close(); stand.srv.Drain() })
	return stand
}

func (s *chatImageStand) post(t *testing.T) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPost, s.server.URL+"/v1/responses", strings.NewReader(`{"model":"agent","input":"Draw a square","stream":true,"metadata":{"image_generation":true}}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Coddy-Session-ID", "sess_generated_image")
	res, err := s.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(res.Body)
	_ = res.Body.Close()
	if err != nil || res.StatusCode != http.StatusOK {
		t.Fatalf("post: %d %v %s", res.StatusCode, err, data)
	}
	s.stream = string(data)
}

func (s *chatImageStand) checkReload(t *testing.T) {
	t.Helper()
	snap, err := s.store.ReadSnapshot("sess_generated_image")
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Messages) != 2 || string(snap.Messages[1].Role) != "assistant" || len(snap.Messages[1].ImageParts) != 1 {
		t.Fatalf("assistant asset not persisted: %+v", snap.Messages)
	}
	res, err := s.server.Client().Get(s.server.URL + "/coddy/sessions/sess_generated_image/messages")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	var body struct {
		Messages []struct {
			Role   string              `json:"role"`
			Images []session.ToolImage `json:"images"`
		} `json:"messages"`
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if len(body.Messages) != 2 || body.Messages[1].Role != "assistant" || len(body.Messages[1].Images) != 1 {
		t.Fatalf("reload missing image: %+v", body)
	}
	img := body.Messages[1].Images[0]
	if img.URL == "" || img.PreviewURL == "" || img.MIMEType != "image/png" {
		t.Fatalf("image descriptor: %+v", img)
	}
	s.asset = img.URL
}

func TestChatImageAssetsFeature(t *testing.T) {
	var s *chatImageStand
	suite := godog.TestSuite{Name: "chat_image_assets", Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/chat_image_assets.feature"}, TestingT: t}, ScenarioInitializer: func(sc *godog.ScenarioContext) {
		sc.Step(`^a persisted chat with a native image Responses provider$`, func() { s = newChatImageStand(t, false) })
		sc.Step(`^the chat requests an image with explicit generation enabled$`, func() { s.post(t) })
		sc.Step(`^the chat streams an assistant image asset$`, func() error {
			if !strings.Contains(s.stream, "event: assistant_images") || !strings.Contains(s.stream, `"messageId":"msg_1"`) {
				return fmt.Errorf("missing live image: %s", s.stream)
			}
			if s.requests.Load() != 1 {
				return fmt.Errorf("image-only answer was replayed %d times", s.requests.Load())
			}
			return nil
		})
		sc.Step(`^the reloaded assistant message owns the generated image$`, func() { s.checkReload(t) })
		sc.Step(`^the session asset route returns the original image$`, func() {
			res, err := s.server.Client().Get(s.server.URL + s.asset)
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(res.Body)
			_ = res.Body.Close()
			if err != nil || res.StatusCode != 200 || !bytes.Equal(data, s.png) {
				t.Fatalf("asset: status %d err %v", res.StatusCode, err)
			}
		})
	}}
	if suite.Run() != 0 {
		t.Fatal("feature failed")
	}
}

func TestGeneratedImagePersistsAfterProviderFailureWithoutReplay(t *testing.T) {
	s := newChatImageStand(t, true)
	s.post(t)
	s.checkReload(t)
	if s.requests.Load() != 1 {
		t.Fatalf("generated image request retried %d times", s.requests.Load())
	}
	if !strings.Contains(s.stream, "assistant_images") || !strings.Contains(s.stream, `"type":"server_error"`) {
		t.Fatalf("image or error missing: %s", s.stream)
	}
}

func TestImageGenerationMetadataRequiresBoolean(t *testing.T) {
	for _, value := range []string{`"true"`, `1`, `null`, `{}`} {
		if coerceMetadataJSON(json.RawMessage(`{"image_generation":`+value+`}`)) == nil {
			t.Errorf("accepted %s", value)
		}
	}
	for _, value := range []string{"true", "false"} {
		if err := coerceMetadataJSON(json.RawMessage(`{"image_generation":` + value + `}`)); err != nil {
			t.Error(err)
		}
	}
}

func TestNativeImageProviderOptIn(t *testing.T) {
	for _, tc := range []struct {
		provider string
		flag     bool
		metadata string
		allowed  bool
	}{
		{"codex", true, `{"image_generation":true}`, true},
		{"codex", false, `{"image_generation":true}`, false},
		{"openai", true, `{"image_generation":true}`, true},
		{"openai", false, `{"image_generation":true}`, false},
		{"anthropic", true, `{"image_generation":true}`, false},
		{"codex", false, `{"image_generation":false}`, true},
		{"codex", false, `{}`, true},
	} {
		t.Run(fmt.Sprintf("%s/%t/%s", tc.provider, tc.flag, tc.metadata), func(t *testing.T) {
			cfg := &config.Config{Providers: []config.ProviderConfig{{Name: "test", Type: tc.provider}}, Models: []config.ModelEntry{{Model: "test/model", ImageGeneration: tc.flag}}, Agent: config.Agent{Model: "test/model"}}
			err := nativeImageProviderError(cfg, &session.State{}, json.RawMessage(tc.metadata))
			if (err == nil) != tc.allowed {
				t.Fatalf("allowed = %t, error = %v", tc.allowed, err)
			}
		})
	}
}

func TestNativeImagesRefuseUnsupportedAndDirectProviders(t *testing.T) {
	for _, providerType := range []string{"anthropic", "opencode", "neuraldeep"} {
		t.Run(providerType, func(t *testing.T) {
			cfg := &config.Config{Providers: []config.ProviderConfig{{Name: "test", Type: providerType}}, Models: []config.ModelEntry{{Model: "test/model", ImageGeneration: true}}, Agent: config.Agent{Model: "test/model"}}
			if err := nativeImageProviderError(cfg, &session.State{}, json.RawMessage(`{"image_generation":true}`)); err == nil {
				t.Fatal("unsupported provider accepted")
			}
		})
	}
	s := newChatImageStand(t, false)
	for _, path := range []string{"/v1/responses", "/v1/chat/completions"} {
		body := `{"model":"local/gpt-test","input":"draw","metadata":{"image_generation":true}}`
		if strings.HasSuffix(path, "completions") {
			body = `{"model":"local/gpt-test","messages":[{"role":"user","content":"draw"}],"metadata":{"image_generation":true}}`
		}
		req, err := http.NewRequest(http.MethodPost, s.server.URL+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Content-Type", "application/json")
		response, err := s.server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if response.StatusCode != 400 || !strings.Contains(string(data), "metadata.image_generation") {
			t.Fatalf("direct %s accepted: %d %s", path, response.StatusCode, data)
		}
	}
	if s.requests.Load() != 0 {
		t.Fatal("refused requests reached the provider")
	}
}

func TestGeneratedImageIsLiveBeforeProviderTerminal(t *testing.T) {
	gate := make(chan struct{})
	var once sync.Once
	release := func() { once.Do(func() { close(gate) }) }
	defer release()
	s := newChatImageStand(t, false, gate)
	t.Cleanup(release)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.server.URL+"/v1/responses", strings.NewReader(`{"model":"agent","input":"draw","stream":true,"metadata":{"image_generation":true}}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	response, err := s.server.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	scanner := bufio.NewScanner(response.Body)
	for scanner.Scan() {
		if scanner.Text() == "event: assistant_images" {
			release()
			_, _ = io.Copy(io.Discard, response.Body)
			return
		}
	}
	t.Fatalf("no live image before terminal: %v", scanner.Err())
}
