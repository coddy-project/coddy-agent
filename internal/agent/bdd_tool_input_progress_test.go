package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/cucumber/godog"
)

type draftProvider struct {
	outputTokens  int
	path          string
	sender        *progressSender
	afterFragment func() error
	done          bool
}

func (p *draftProvider) Complete(context.Context, []llm.Message, []llm.ToolDefinition) (*llm.Response, error) {
	return nil, fmt.Errorf("stream required")
}
func (p *draftProvider) Stream(_ context.Context, _ []llm.Message, _ []llm.ToolDefinition, emit func(llm.StreamChunk)) (*llm.Response, error) {
	if p.done {
		return &llm.Response{Content: "done", StopReason: "end_turn"}, nil
	}
	p.done = true
	path, _ := json.Marshal(p.path)
	prefix := `{"path":` + string(path) + `,"content":"hello\n`
	tc := llm.ToolCall{ID: "write1", Name: "write"}
	emit(llm.StreamChunk{ToolCallNamed: &tc})
	tc.InputJSON = prefix
	emit(llm.StreamChunk{ToolCallDelta: &tc})
	if _, err := os.Stat(p.path); !os.IsNotExist(err) {
		return nil, fmt.Errorf("file exists before arguments completed: %v", err)
	}
	p.sender.mu.Lock()
	progress := false
	for _, u := range p.sender.updates {
		if s, ok := u.(acp.ToolCallStatusUpdate); ok && s.Status == "pending" && s.Meta != nil {
			progress = true
		}
	}
	p.sender.mu.Unlock()
	if !progress {
		return nil, fmt.Errorf("no progress before the closing argument fragment")
	}
	if p.afterFragment != nil {
		if err := p.afterFragment(); err != nil {
			return nil, err
		}
	}
	tc.InputJSON = `world"}`
	emit(llm.StreamChunk{ToolCallDelta: &tc})
	tc.InputJSON = prefix + tc.InputJSON
	emit(llm.StreamChunk{ToolCall: &tc})
	return &llm.Response{ToolCalls: []llm.ToolCall{tc}, StopReason: "tool_use", OutputTokens: p.outputTokens}, nil
}

type draftPermissionSender struct {
	*progressSender
	denied   bool
	requests int
}

func (s *draftPermissionSender) RequestPermission(_ context.Context, _ acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	s.requests++
	if s.denied {
		return &acp.PermissionResult{Outcome: "cancelled"}, nil
	}
	return &acp.PermissionResult{Outcome: "allow", OptionID: "allow"}, nil
}

func runDraftTurn(t *testing.T, interrupt, denied bool, outputTokens int) (string, *progressSender, error) {
	t.Helper()
	cwd := t.TempDir()
	path := filepath.Join(cwd, "hello.txt")
	sender := &progressSender{}
	provider := &draftProvider{path: path, sender: sender, outputTokens: outputTokens}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if interrupt {
		provider.afterFragment = func() error { cancel(); return context.Canceled }
	}
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{Name: "fake", Type: "openai", APIKey: "test"}},
		Models:    []config.ModelEntry{{Model: "fake/model", MaxTokens: 100, MaxContextTokens: 128000}},
		Agent:     config.Agent{Model: "fake/model"},
	}
	cfg.Agent.ApplyDefaults()
	cfg.Prompts.ApplyDefaults()
	state := &session.State{ID: "draft", CWD: cwd, Mode: session.ModeAgent, SessionDir: t.TempDir()}
	permissionSender := &draftPermissionSender{progressSender: sender, denied: denied}
	ag := NewAgent(cfg, state, permissionSender, nil)
	ag.providerFactory = func(llm.ProviderInput) (llm.Provider, error) { return provider, nil }
	_, err := ag.Run(ctx, []acp.ContentBlock{{Type: "text", Text: "write a file"}})
	if !interrupt && permissionSender.requests != 1 {
		t.Errorf("permission requests = %d, want one", permissionSender.requests)
	}
	return path, sender, err
}

func TestDraftCancellationDoesNotWrite(t *testing.T) {
	path, _, _ := runDraftTurn(t, true, false, 77)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("cancelled draft wrote a file: %v", err)
	}
}

func TestToolInputProgressFeature(t *testing.T) {
	var path string
	var sender *progressSender
	suite := godog.TestSuite{Name: "tool_input_progress", Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/tool_input_progress.feature"}, Strict: true, TestingT: t}, ScenarioInitializer: func(sc *godog.ScenarioContext) {
		sc.Step(`^a file tool streams its arguments before executing$`, func() error { var err error; path, sender, err = runDraftTurn(t, false, false, 77); return err })
		sc.Step(`^the completed file contains the full draft$`, func() error {
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if string(b) != "hello\nworld" {
				return fmt.Errorf("wrong file: %q", b)
			}
			return nil
		})
		sc.Step(`^the final token count uses provider usage without double counting$`, func() error {
			updates, _ := sender.progress()
			last := updates[len(updates)-1]
			if last.OutputTokens != 77 {
				return fmt.Errorf("wrong tokens: %+v", last)
			}
			return nil
		})
	}}
	if suite.Run() != 0 {
		t.Fatal("tool input progress feature failed")
	}
}

// Preview updates survive the JSON round trip used by remote consoles.
func TestToolInputProgressWireFormat(t *testing.T) {
	p := newToolInputProgress("write")
	p.add(`{"content":"hello`)
	b, err := json.Marshal(p.update("x", p.last, true))
	if err != nil {
		t.Fatal(err)
	}
	var u acp.ToolCallStatusUpdate
	if err = json.Unmarshal(b, &u); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"toolInputProgress"`) || u.Status != "pending" {
		t.Fatalf("wire progress: %s", b)
	}
}

func TestDraftPermissionDenialDoesNotWrite(t *testing.T) {
	path, _, _ := runDraftTurn(t, false, true, 77)
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("denied tool wrote a file: %v", err)
	}
}

func TestToolInputProgressWithoutReportedUsageCountsArgumentsOnce(t *testing.T) {
	path, sender, err := runDraftTurn(t, false, false, 0)
	if err != nil {
		t.Fatal(err)
	}
	encodedPath, _ := json.Marshal(path)
	args := `{"path":` + string(encodedPath) + `,"content":"hello\nworld"}`
	want := estimateTokensFromRunes(len("write" + args))
	updates, _ := sender.progress()
	last := updates[len(updates)-1]
	if last.OutputTokens != want || !last.Estimated {
		t.Fatalf("progress=%+v want %d estimated tokens", last, want)
	}
}
