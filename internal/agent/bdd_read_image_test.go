package agent

// Godog harness for features/read_image.feature: drives the real Agent through
// a scripted provider that reads image files over a real temp workspace, then
// asserts the shape of the next LLM request, the persisted transcript and the
// tool_call_update frames the surfaces preview the pictures from.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func tcReadPath(id, path string) llm.ToolCall {
	b, _ := json.Marshal(map[string]string{"path": path})
	return llm.ToolCall{ID: id, Name: "read", InputJSON: string(b)}
}

// testPNG encodes a small real PNG, filled with one colour so two files differ.
func testPNG(w, h int, fill color.Color) []byte {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, fill)
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

// statusRecorder keeps every tool_call_update the agent publishes.
type statusRecorder struct {
	resumePermissionSender
	mu      sync.Mutex
	updates []acp.ToolCallStatusUpdate
}

func (r *statusRecorder) SendSessionUpdate(_ string, update interface{}) error {
	if u, ok := update.(acp.ToolCallStatusUpdate); ok {
		r.mu.Lock()
		r.updates = append(r.updates, u)
		r.mu.Unlock()
	}
	return nil
}

func (r *statusRecorder) final(toolCallID string) *acp.ToolCallStatusUpdate {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := len(r.updates) - 1; i >= 0; i-- {
		if u := r.updates[i]; u.ToolCallID == toolCallID && u.Status == "completed" {
			return &u
		}
	}
	return nil
}

type readImageFeatureState struct {
	cwd        string
	multimodal bool
	st         *session.State
	ag         *Agent
	provider   *evScriptProvider
	sender     *statusRecorder
}

func (s *readImageFeatureState) reset() error {
	s.close()
	dir, err := os.MkdirTemp("", "coddy-bdd-read-image-*")
	if err != nil {
		return err
	}
	s.cwd = dir
	s.multimodal = false
	s.provider = &evScriptProvider{}
	s.sender = &statusRecorder{}
	return os.MkdirAll(filepath.Join(dir, ".session"), 0o755)
}

func (s *readImageFeatureState) close() {
	if s.cwd != "" {
		_ = os.RemoveAll(s.cwd)
		s.cwd = ""
	}
	s.st = nil
	s.ag = nil
}

func (s *readImageFeatureState) modelReadsImages() error {
	s.multimodal = true
	return nil
}

func (s *readImageFeatureState) twoImages(a, b string) error {
	if err := os.WriteFile(filepath.Join(s.cwd, a), testPNG(4, 3, color.NRGBA{R: 200, A: 255}), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(s.cwd, b), testPNG(4, 3, color.NRGBA{G: 200, A: 255}), 0o644)
}

func (s *readImageFeatureState) agent() *Agent {
	if s.ag != nil {
		return s.ag
	}
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100, MaxContextTokens: 128000, Multimodal: config.BoolPtr(s.multimodal)}},
		Agent:     config.Agent{Model: "fake/model"},
		Tools:     config.Tools{PermissionMode: config.PermModeBypass},
	}
	s.st = &session.State{ID: "sess_bdd_read_image", CWD: s.cwd, Mode: session.ModeAgent, SessionDir: filepath.Join(s.cwd, ".session")}
	s.ag = NewAgent(cfg, s.st, s.sender, nil)
	s.ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return s.provider, nil }
	return s.ag
}

func (s *readImageFeatureState) readTwoInOneStep(a, b string) error {
	ag := s.agent()
	s.provider.steps = append(s.provider.steps,
		evStep{calls: []llm.ToolCall{tcReadPath("r1", a), tcReadPath("r2", b)}},
		evStep{text: "answer"})
	_, err := ag.Run(context.Background(), []acp.ContentBlock{{Type: "text", Text: "look"}})
	return err
}

func (s *readImageFeatureState) lastRequest() []llm.Message {
	seen := s.provider.streamSeen
	if len(seen) == 0 {
		return nil
	}
	return seen[len(seen)-1]
}

// requestTail is the part of the last request after the prompt: what the
// step added, without the turn context the send boundary appends to every
// request.
func (s *readImageFeatureState) requestTail() ([]llm.Message, error) {
	req := s.lastRequest()
	if n := len(req); n > 0 && req[n-1].Role == llm.RoleUser && strings.Contains(req[n-1].Content, turnContextOpenTag) {
		req = req[:n-1]
	}
	for i, m := range req {
		if m.Role == llm.RoleAssistant && len(m.ToolCalls) > 0 {
			return req[i:], nil
		}
	}
	return nil, fmt.Errorf("the last request has no assistant tool-call message")
}

func (s *readImageFeatureState) requestShape() error {
	tail, err := s.requestTail()
	if err != nil {
		return err
	}
	roles := make([]string, 0, len(tail))
	for _, m := range tail {
		roles = append(roles, string(m.Role))
	}
	want := []string{string(llm.RoleAssistant), string(llm.RoleTool), string(llm.RoleTool), string(llm.RoleUser)}
	if strings.Join(roles, ",") != strings.Join(want, ",") {
		return fmt.Errorf("request tail roles = %v, want %v", roles, want)
	}
	if tail[1].ToolCallID != "r1" || tail[2].ToolCallID != "r2" {
		return fmt.Errorf("tool results answer %q and %q, want r1 and r2", tail[1].ToolCallID, tail[2].ToolCallID)
	}
	for _, m := range tail[1:3] {
		if len(m.ImageParts) > 0 {
			return fmt.Errorf("the provider was sent a tool result carrying %d image(s)", len(m.ImageParts))
		}
	}
	return nil
}

func imageNames(m llm.Message) []string {
	names := make([]string, 0, len(m.ImageParts))
	for _, p := range m.ImageParts {
		names = append(names, p.Name)
	}
	return names
}

func (s *readImageFeatureState) userMessageCarries(a, b string) error {
	tail, err := s.requestTail()
	if err != nil {
		return err
	}
	user := tail[len(tail)-1]
	if got := imageNames(user); strings.Join(got, ",") != a+","+b {
		return fmt.Errorf("user message images = %v, want [%s %s]", got, a, b)
	}
	for _, p := range user.ImageParts {
		if !strings.HasPrefix(p.DataURL, "data:image/png;base64,") {
			return fmt.Errorf("image %s is not a PNG data URL: %.40q", p.Name, p.DataURL)
		}
	}
	for _, name := range []string{a, b} {
		if !strings.Contains(user.Content, name) {
			return fmt.Errorf("the user message text %q does not name %s", user.Content, name)
		}
	}
	return nil
}

func (s *readImageFeatureState) transcriptKeepsImagesOnResults() error {
	want := map[string]string{"r1": "before.png", "r2": "after.png"}
	for _, m := range s.st.GetMessages() {
		name, ok := want[m.ToolCallID]
		if m.Role != llm.RoleTool || !ok {
			continue
		}
		if got := imageNames(m); len(got) != 1 || got[0] != name {
			return fmt.Errorf("the result of %s keeps images %v, want [%s]", m.ToolCallID, got, name)
		}
		delete(want, m.ToolCallID)
	}
	if len(want) > 0 {
		return fmt.Errorf("no persisted result for %v", want)
	}
	return nil
}

func (s *readImageFeatureState) transcriptHasOnlyThePrompt() error {
	var users []string
	for _, m := range s.st.GetMessages() {
		if m.Role == llm.RoleUser {
			users = append(users, m.Content)
		}
	}
	if len(users) != 1 || users[0] != "look" {
		return fmt.Errorf("persisted user messages = %q, want only the prompt", users)
	}
	return nil
}

func (s *readImageFeatureState) surfacesToldAboutImages() error {
	assets := session.AssetsPath(filepath.Join(s.cwd, ".session"))
	for id, name := range map[string]string{"r1": "before.png", "r2": "after.png"} {
		u := s.sender.final(id)
		if u == nil {
			return fmt.Errorf("no completed tool_call_update for %s", id)
		}
		images := session.ToolImagesFromMeta(u.Meta)
		if len(images) != 1 {
			return fmt.Errorf("the update of %s names %d images, want 1: %v", id, len(images), u.Meta)
		}
		img := images[0]
		if img.Name != name || img.MIMEType != "image/png" {
			return fmt.Errorf("the update of %s names %s (%s), want %s (image/png)", id, img.Name, img.MIMEType, name)
		}
		saved, err := os.ReadFile(filepath.Join(assets, img.Asset))
		if err != nil {
			return fmt.Errorf("the asset of %s: %w", id, err)
		}
		original, _ := os.ReadFile(filepath.Join(s.cwd, name))
		if !bytes.Equal(saved, original) {
			return fmt.Errorf("the asset of %s is not the picture the model was shown", id)
		}
		wantURL := session.AssetRoute(s.st.GetID(), img.Asset)
		if img.URL != wantURL || img.PreviewURL != session.AssetThumbnailRoute(s.st.GetID(), img.Asset) {
			return fmt.Errorf("the update of %s gives url %q and preview %q, want %q and its thumbnail", id, img.URL, img.PreviewURL, wantURL)
		}
	}
	return nil
}

func initializeReadImageScenario(sc *godog.ScenarioContext) {
	s := &readImageFeatureState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^a model that reads images$`, s.modelReadsImages)
	sc.Step(`^workspace images "([^"]*)" and "([^"]*)"$`, s.twoImages)
	sc.Step(`^the model reads "([^"]*)" and "([^"]*)" in one step, then answers$`, s.readTwoInOneStep)
	sc.Step(`^the next LLM request has the assistant tool calls, then both tool results, then one user message$`, s.requestShape)
	sc.Step(`^that user message carries the images "([^"]*)" and "([^"]*)" in that order$`, s.userMessageCarries)
	sc.Step(`^the persisted transcript keeps each image on the result of the read that produced it$`, s.transcriptKeepsImagesOnResults)
	sc.Step(`^the persisted transcript has no user message besides the prompt$`, s.transcriptHasOnlyThePrompt)
	sc.Step(`^each read tells the surfaces about its image, saved with the session's assets$`, s.surfacesToldAboutImages)
}

func TestReadImageFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "read-image",
		ScenarioInitializer: initializeReadImageScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/read_image.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("read_image feature suite failed")
	}
}
