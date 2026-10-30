package agent

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"image"
	"image/png"
	"log/slog"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type interruptQueueProvider struct {
	*scriptedProvider
	started chan struct{}
	partial string
	first   bool
}

func (p *interruptQueueProvider) Stream(ctx context.Context, messages []llm.Message, defs []llm.ToolDefinition, onChunk func(llm.StreamChunk)) (*llm.Response, error) {
	if !p.first {
		p.first = true
		close(p.started)
		if p.partial != "" {
			onChunk(llm.StreamChunk{TextDelta: p.partial})
		}
		<-ctx.Done()
		return &llm.Response{Content: p.partial}, ctx.Err()
	}
	return p.scriptedProvider.Stream(ctx, messages, defs, onChunk)
}

func TestQueuedInterruptAgentAppendsSeparateRows(t *testing.T) {
	for _, partial := range []string{"", "partial answer"} {
		t.Run("partial="+partial, func(t *testing.T) {
			f := &queueFeatureState{}
			if err := f.reset(); err != nil {
				t.Fatal(err)
			}
			defer f.close()
			if err := f.buildSession(nil); err != nil {
				t.Fatal(err)
			}
			p := &interruptQueueProvider{scriptedProvider: f.provider, started: make(chan struct{}), partial: partial}
			runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
				a := NewAgent(f.cfg, st, snd, slog.Default())
				a.SetProviderFactory(func(llm.ProviderInput) (llm.Provider, error) { return p, nil })
				return a.Run(ctx, prompt)
			}
			m := session.NewManager(f.cfg, f.client, runner, slog.Default(), f.cwd, f.store)
			res, err := m.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: f.cwd})
			if err != nil {
				t.Fatal(err)
			}
			st := m.SessionByID(res.SessionID)
			done := make(chan error, 1)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			go func() {
				_, err := m.HandleSessionPrompt(ctx, acp.SessionPromptParams{SessionID: res.SessionID, Prompt: []acp.ContentBlock{{Type: "text", Text: "original"}}})
				done <- err
			}()
			select {
			case <-p.started:
			case <-time.After(5 * time.Second):
				t.Fatal("provider never started")
			}
			var buf bytes.Buffer
			if err := png.Encode(&buf, image.NewNRGBA(image.Rect(0, 0, 1, 1))); err != nil {
				t.Fatal(err)
			}
			picture := []acp.ImagePartRef{{Name: "picture.png", DataURL: "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())}}
			texts := []string{"same", "same", "", "/compact", "/goal do not execute this as a command"}
			for i, text := range texts {
				mode := session.QueueModeSteer
				if i%2 == 0 {
					mode = session.QueueModeAfterTurn
				}
				var images []acp.ImagePartRef
				if i == 1 || i == 2 {
					images = picture
				}
				if _, _, err := m.EnqueueTurnMessageWithMode(res.SessionID, text, mode, images); err != nil {
					t.Fatal(err)
				}
			}
			if !m.TryInterruptQueuedTurn(res.SessionID) {
				t.Fatal("interrupt refused")
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("turn did not finish")
			}
			var users []llm.Message
			messages := st.GetMessages()
			for _, msg := range messages {
				if msg.Role == llm.RoleUser {
					users = append(users, msg)
				}
			}
			if len(users) != len(texts)+1 {
				t.Fatalf("user rows = %d, want %d: %+v", len(users), len(texts)+1, users)
			}
			if users[0].Content != "original" {
				t.Fatalf("opening prompt = %q", users[0].Content)
			}
			for i, text := range texts {
				msg := users[i+1]
				if !msg.FollowUp || !strings.HasPrefix(msg.Content, text) {
					t.Errorf("row %d = %+v", i, msg)
				}
				if (i == 1 || i == 2) && (len(msg.ImageParts) != 1 || msg.ImageParts[0].FilePath == "") {
					t.Errorf("row %d lost its image: %+v", i, msg)
				}
			}
			if st.GetGoal().Active() {
				t.Fatal("queued /goal was executed")
			}
			if partial != "" && (len(messages) < 2 || messages[1].Role != llm.RoleAssistant || messages[1].Content != partial) {
				t.Fatalf("partial answer did not precede queued input: %+v", messages)
			}
			p.mu.Lock()
			requests := append([][]llm.Message(nil), p.requests...)
			p.mu.Unlock()
			if len(requests) != 1 {
				t.Fatalf("continuation provider calls = %d", len(requests))
			}
			var sent []llm.Message
			for _, msg := range requests[0] {
				if msg.Role == llm.RoleUser && !msg.Ephemeral {
					sent = append(sent, msg)
				}
			}
			if len(sent) != len(users) {
				t.Fatalf("provider user rows = %d, transcript rows = %d", len(sent), len(users))
			}
			for i := range sent {
				if sent[i].Content != users[i].Content {
					t.Errorf("provider row %d differs from stored row", i)
				}
			}
		})
	}
}

func TestQueuedInterruptAgentKeepsTheUnreadAdmittedPrompt(t *testing.T) {
	f := &queueFeatureState{}
	if err := f.reset(); err != nil {
		t.Fatal(err)
	}
	defer f.close()
	if err := f.buildSession(nil); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	first := true
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		if first {
			first = false
			close(started)
			<-ctx.Done()
		}
		a := NewAgent(f.cfg, st, snd, slog.Default())
		a.SetProviderFactory(func(llm.ProviderInput) (llm.Provider, error) { return f.provider, nil })
		return a.Run(ctx, prompt)
	}
	m := session.NewManager(f.cfg, f.client, runner, slog.Default(), f.cwd, f.store)
	res, err := m.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: f.cwd})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := m.HandleSessionPrompt(ctx, acp.SessionPromptParams{SessionID: res.SessionID, Prompt: []acp.ContentBlock{{Type: "text", Text: "original"}}})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("runner did not start")
	}
	if _, _, err := m.EnqueueTurnMessage(res.SessionID, "follow up"); err != nil {
		t.Fatal(err)
	}
	if !m.TryInterruptQueuedTurn(res.SessionID) {
		t.Fatal("interrupt refused")
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("turn did not finish")
	}
	var users []string
	for _, msg := range m.SessionByID(res.SessionID).GetMessages() {
		if msg.Role == llm.RoleUser {
			users = append(users, msg.Content)
		}
	}
	if len(users) != 2 || users[0] != "original" || users[1] != "follow up" {
		t.Fatalf("admitted prompt was lost or replayed: %q", users)
	}
}

// The notifications are checkpoints inside the waits, not merely inside the
// failed provider call. The long timers are always cancelled by the test.
type queuedInterruptWaitSender struct {
	acp.UpdateSender
	waiting chan struct{}
	quota   bool
	once    sync.Once
}

func (s *queuedInterruptWaitSender) SendSessionUpdate(id string, update interface{}) error {
	if usage, ok := update.(acp.ProviderUsageUpdate); ok && s.quota && usage.Resuming {
		s.once.Do(func() { close(s.waiting) })
	}
	return s.UpdateSender.SendSessionUpdate(id, update)
}

func (s *queuedInterruptWaitSender) ProviderRecovery(error, time.Duration, int, int) {
	if !s.quota {
		s.once.Do(func() { close(s.waiting) })
	}
}

func TestQueuedInterruptAgentDuringProviderWait(t *testing.T) {
	for _, wait := range []string{"quota_reset", "provider_recovery"} {
		for _, action := range []string{"queue_interrupt", "external_cancel"} {
			t.Run(wait+"/"+action, func(t *testing.T) {
				f := &queueFeatureState{}
				if err := f.reset(); err != nil {
					t.Fatal(err)
				}
				defer f.close()
				if err := f.buildSession(nil); err != nil {
					t.Fatal(err)
				}
				retries, maxWaitMS := 1, int(2*time.Hour/time.Millisecond)
				f.cfg.Agent.LLMRetryMax = &retries
				f.cfg.Agent.LLMRetryBaseMS = 60000
				f.cfg.Agent.WaitForLimitReset = true
				f.cfg.Agent.WaitForLimitResetMaxMS = &maxWaitMS
				var failure error = syscall.ECONNRESET
				partial := "partial before failure"
				if wait == "quota_reset" {
					failure, partial = quotaReset(time.Hour), ""
				} else if !llm.IsTransientProviderError(failure) {
					t.Fatal("fixture is not a transient provider error")
				}
				f.provider.fail = func(call int) (string, error) {
					if call == 1 {
						return partial, failure
					}
					return "", nil
				}
				sender := &queuedInterruptWaitSender{UpdateSender: f.client, waiting: make(chan struct{}), quota: wait == "quota_reset"}
				runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, _ acp.UpdateSender) (string, error) {
					a := NewAgent(f.cfg, st, sender, slog.Default())
					a.SetProviderFactory(func(llm.ProviderInput) (llm.Provider, error) { return f.provider, nil })
					return a.Run(ctx, prompt)
				}
				m := session.NewManager(f.cfg, sender, runner, slog.Default(), f.cwd, f.store)
				created, err := m.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: f.cwd})
				if err != nil {
					t.Fatal(err)
				}
				sid := created.SessionID
				st := m.SessionByID(sid)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				type outcome struct {
					result *acp.SessionPromptResult
					err    error
				}
				done := make(chan outcome, 1)
				go func() {
					result, err := m.HandleSessionPrompt(ctx, acp.SessionPromptParams{SessionID: sid, Prompt: []acp.ContentBlock{{Type: "text", Text: "original"}}})
					done <- outcome{result, err}
				}()
				select {
				case <-sender.waiting:
				case <-time.After(5 * time.Second):
					t.Fatal("agent never entered the provider wait")
				}
				if action == "queue_interrupt" {
					if _, _, err := m.EnqueueTurnMessage(sid, "correction"); err != nil {
						t.Fatal(err)
					}
					if !m.TryInterruptQueuedTurn(sid) {
						t.Fatal("queue interrupt refused during provider wait")
					}
					if st.IsUserCancelledTurn() {
						t.Fatal("child interrupt became full user cancellation")
					}
				} else {
					cancel()
				}
				var got outcome
				select {
				case got = <-done:
				case <-time.After(5 * time.Second):
					t.Fatal("interrupted provider wait did not settle")
				}
				f.provider.mu.Lock()
				calls := f.provider.calls
				f.provider.mu.Unlock()
				if action == "external_cancel" {
					if !errors.Is(got.err, failure) || calls != 1 {
						t.Fatalf("external cancellation lost provider failure: calls=%d result=%+v err=%v", calls, got.result, got.err)
					}
					return
				}
				if got.err != nil || got.result == nil || got.result.StopReason != acp.StopReasonEndTurn || calls != 2 {
					t.Fatalf("accepted interrupt did not continue: calls=%d result=%+v err=%v", calls, got.result, got.err)
				}
				messages := st.GetMessages()
				var users []llm.Message
				for _, msg := range messages {
					if msg.Role == llm.RoleUser {
						users = append(users, msg)
					}
				}
				if len(users) != 2 || users[0].Content != "original" || users[1].Content != "correction" || !users[1].FollowUp {
					t.Fatalf("queued correction was lost or replayed: %+v", users)
				}
				if partial != "" && (len(messages) < 2 || messages[1].Role != llm.RoleAssistant || messages[1].Content != partial) {
					t.Fatalf("recovery lost its partial answer: %+v", messages)
				}
				if len(messages) == 0 || messages[len(messages)-1].Role != llm.RoleAssistant || messages[len(messages)-1].Content != "done" {
					t.Fatalf("fresh run did not answer the correction: %+v", messages)
				}
			})
		}
	}
}

// A Stop that lands while the run prepares (an MCP dial, a UserPromptSubmit
// hook) cancels the turn, not the message that started it: the operator's prompt
// stays in the transcript, as it does with no queue involved.
func TestStopDuringPreparationKeepsThePrompt(t *testing.T) {
	f := &queueFeatureState{}
	if err := f.reset(); err != nil {
		t.Fatal(err)
	}
	defer f.close()
	if err := f.buildSession(nil); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runner := func(runCtx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		st.SetUserCancelledTurn()
		cancel()
		a := NewAgent(f.cfg, st, snd, slog.Default())
		a.SetProviderFactory(func(llm.ProviderInput) (llm.Provider, error) { return f.provider, nil })
		return a.Run(runCtx, prompt)
	}
	m := session.NewManager(f.cfg, f.client, runner, slog.Default(), f.cwd, f.store)
	res, err := m.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: f.cwd})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.HandleSessionPrompt(ctx, acp.SessionPromptParams{SessionID: res.SessionID, Prompt: []acp.ContentBlock{{Type: "text", Text: "typed before Stop"}}}); err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	var users []string
	for _, msg := range m.SessionByID(res.SessionID).GetMessages() {
		if msg.Role == llm.RoleUser {
			users = append(users, msg.Content)
		}
	}
	if len(users) != 1 || users[0] != "typed before Stop" {
		t.Fatalf("the prompt did not reach the transcript: %q", users)
	}
}

// Stop with a waiting queue during the compaction after a provider's refusal
// continues with the queue, as it does in the loop's other waits, instead of
// failing the turn with the refusal it was recovering from.
func TestQueuedInterruptDuringOverflowRecoveryContinues(t *testing.T) {
	f := &queueFeatureState{}
	if err := f.reset(); err != nil {
		t.Fatal(err)
	}
	defer f.close()
	if err := f.buildSession(nil); err != nil {
		t.Fatal(err)
	}
	p := &recoveryProvider{steps: readSteps(4), refuse: refuseCalls(3), foldFail: context.Canceled}
	fix := newRecoveryFixture(t, p, config.Compaction{})
	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, snd acp.UpdateSender) (string, error) {
		a := NewAgent(fix.ag.cfg, st, snd, slog.Default())
		a.SetProviderFactory(func(llm.ProviderInput) (llm.Provider, error) { return p, nil })
		return a.Run(ctx, prompt)
	}
	m := session.NewManager(fix.ag.cfg, f.client, runner, slog.Default(), fix.cwd, f.store)
	res, err := m.HandleSessionNew(context.Background(), acp.SessionNewParams{CWD: fix.cwd})
	if err != nil {
		t.Fatal(err)
	}
	folded := false
	p.onFold = func() {
		folded = true
		p.onFold = nil
		if _, _, err := m.EnqueueTurnMessage(res.SessionID, "follow up"); err != nil {
			t.Error(err)
		}
		if !m.TryInterruptQueuedTurn(res.SessionID) {
			t.Error("interrupt refused")
		}
	}
	if _, err := m.HandleSessionPrompt(context.Background(), acp.SessionPromptParams{SessionID: res.SessionID, Prompt: []acp.ContentBlock{{Type: "text", Text: recoveryPrompt}}}); err != nil {
		t.Fatalf("Stop with a waiting queue failed the turn: %v", err)
	}
	if !folded {
		t.Fatal("the recovery never reached its compaction")
	}
	var users []string
	for _, msg := range m.SessionByID(res.SessionID).GetMessages() {
		if msg.Role == llm.RoleUser {
			users = append(users, msg.Content)
		}
	}
	if len(users) != 2 || users[1] != "follow up" {
		t.Fatalf("the queue did not continue: %q", users)
	}
}
