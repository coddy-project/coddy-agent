package session_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/cucumber/godog"
)

// Environment of the lifecycle helper process. It is a separate helper from
// the one the ACP integration suite drives so the two can grow their turns
// independently.
const (
	testLifecycleHelperEnv    = "CODDY_TEST_ACP_LIFECYCLE_HELPER"
	testLifecycleWorkspaceEnv = "CODDY_TEST_LIFECYCLE_WORKSPACE"
	testLifecycleRootEnv      = "CODDY_TEST_LIFECYCLE_SESSIONS_ROOT"
	// testLifecycleRenameEnv names a file holding the title a running helper
	// pins on its next turn. A rename is out-of-band by nature - no ACP
	// request makes one - so the harness hands it over the way a surface that
	// is not this client would: through something the agent reads.
	testLifecycleRenameEnv = "CODDY_TEST_LIFECYCLE_RENAME_FILE"
)

type acpLifecycleState struct {
	root        string
	sessionsDir string
	cmd         *exec.Cmd
	stdin       io.WriteCloser
	lines       chan []byte
	readErrs    chan error
	sessionID   string
	extraDir    string
	renameTo    string
	// elicitation is the params of the elicitation/create request the last
	// question went out as.
	elicitation map[string]interface{}
	// wire holds the messages one request produced, in arrival order.
	wire []map[string]interface{}
	// lastResult is the result object of the response that closed that request.
	lastResult map[string]interface{}
}

func (s *acpLifecycleState) reset() error {
	s.close()
	root, err := os.MkdirTemp("", "coddy-acp-lifecycle-")
	if err != nil {
		return err
	}
	s.root = root
	s.sessionsDir = filepath.Join(root, "sessions")
	s.sessionID = ""
	s.extraDir = ""
	s.renameTo = ""
	s.elicitation = nil
	s.wire = nil
	s.lastResult = nil
	return nil
}

func (s *acpLifecycleState) close() {
	if s.stdin != nil {
		_ = s.stdin.Close()
		s.stdin = nil
	}
	if s.cmd != nil {
		_ = s.cmd.Process.Kill()
		_, _ = s.cmd.Process.Wait()
		s.cmd = nil
	}
	s.lines = nil
	s.readErrs = nil
}

func (s *acpLifecycleState) start() error {
	if s.cmd != nil {
		return nil
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestACPLifecycleHelperProcess$")
	cmd.Env = append(os.Environ(),
		testLifecycleHelperEnv+"=1",
		testLifecycleWorkspaceEnv+"="+s.root,
		testLifecycleRootEnv+"="+s.sessionsDir,
		testLifecycleRenameEnv+"="+s.renameFile(),
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	s.cmd = cmd
	s.stdin = stdin
	// The reader goroutine works on its own channel variables: close() clears
	// the fields from the scenario goroutine, and a goroutine reading the
	// fields to send would race with that (and block forever once they are
	// nil). The channels are buffered, so the goroutine drains into them and
	// exits even after nobody reads anymore.
	lines := make(chan []byte, 256)
	readErrs := make(chan error, 1)
	s.lines = lines
	s.readErrs = readErrs
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 4*1024*1024), 4*1024*1024)
		for scanner.Scan() {
			lines <- append([]byte(nil), scanner.Bytes()...)
		}
		readErrs <- scanner.Err()
	}()
	return nil
}

// request sends one JSON-RPC request and collects every message until the
// response carrying its id arrives. Notifications that precede that response
// are kept, because that is where a title change lands inside a turn.
func (s *acpLifecycleState) request(id int, method string, params map[string]interface{}) error {
	if err := s.start(); err != nil {
		return err
	}
	if params == nil {
		params = map[string]interface{}{}
	}
	if err := writeJSONLine(s.stdin, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}); err != nil {
		return err
	}
	wire, result, err := s.readUntilResponse(id)
	if err != nil {
		return err
	}
	s.wire = wire
	s.lastResult = result
	return nil
}

// readUntilRequest collects messages until the agent sends a request of the
// given method, which is returned apart from what came before it.
func (s *acpLifecycleState) readUntilRequest(method string) ([]map[string]interface{}, map[string]interface{}, error) {
	collected := make([]map[string]interface{}, 0, 8)
	deadline := time.NewTimer(4 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case line := <-s.lines:
			var msg map[string]interface{}
			if err := json.Unmarshal(line, &msg); err != nil {
				return collected, nil, err
			}
			if got, _ := msg["method"].(string); got == method {
				if _, hasID := msg["id"]; hasID {
					return collected, msg, nil
				}
			}
			collected = append(collected, msg)
		case err := <-s.readErrs:
			return collected, nil, fmt.Errorf("helper stdout closed: %v", err)
		case <-deadline.C:
			return collected, nil, fmt.Errorf("no %s request in %#v", method, collected)
		}
	}
}

func (s *acpLifecycleState) readUntilResponse(id int) ([]map[string]interface{}, map[string]interface{}, error) {
	collected := make([]map[string]interface{}, 0, 8)
	deadline := time.NewTimer(4 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case line := <-s.lines:
			var msg map[string]interface{}
			if err := json.Unmarshal(line, &msg); err != nil {
				return collected, nil, err
			}
			collected = append(collected, msg)
			if got, _ := msg["id"].(float64); got == float64(id) {
				if errMsg, ok := msg["error"]; ok {
					return collected, nil, fmt.Errorf("request %s failed: %v", msg["method"], errMsg)
				}
				result, _ := msg["result"].(map[string]interface{})
				return collected, result, nil
			}
		case err := <-s.readErrs:
			return collected, nil, fmt.Errorf("helper stdout closed: %v", err)
		case <-deadline.C:
			return collected, nil, fmt.Errorf("no response to request %d in %#v", id, collected)
		}
	}
}

// sendPrompt drives one turn. The helper's runner records what the user said
// and stops, so the title derived from the first message appears exactly as it
// would after a real turn.
func (s *acpLifecycleState) sendPrompt(text string) error {
	return s.request(20, "session/prompt", map[string]interface{}{
		"sessionId": s.sessionID,
		"prompt":    []map[string]interface{}{{"type": "text", "text": text}},
	})
}

// ---- steps ----

func (s *acpLifecycleState) clientHasActiveSession() error {
	if err := s.start(); err != nil {
		return err
	}
	return s.createSession(nil)
}

func (s *acpLifecycleState) createSession(additionalDirectories []string) error {
	params := map[string]interface{}{"cwd": s.root}
	if len(additionalDirectories) > 0 {
		params["additionalDirectories"] = additionalDirectories
	}
	if err := s.request(1, "session/new", params); err != nil {
		return err
	}
	id, _ := s.lastResult["sessionId"].(string)
	if id == "" {
		return fmt.Errorf("session/new response carried no sessionId: %#v", s.lastResult)
	}
	s.sessionID = id
	return nil
}

func (s *acpLifecycleState) clientSendsPromptReading(text string) error {
	return s.sendPrompt(text)
}

func (s *acpLifecycleState) clientIsToldSessionTitleIs(want string) error {
	// The last announcement is the title the session settled on: a turn that
	// names and then renames a conversation announces both.
	var last map[string]interface{}
	for _, msg := range s.wire {
		params, _ := msg["params"].(map[string]interface{})
		update, _ := params["update"].(map[string]interface{})
		if update["sessionUpdate"] == acp.UpdateTypeSessionInfoUpdate {
			last = update
		}
	}
	if last == nil {
		return fmt.Errorf("no session_info_update in %#v", s.wire)
	}
	got, _ := last["title"].(string)
	if got != want {
		return fmt.Errorf("session_info_update title = %q, want %q", got, want)
	}
	return nil
}

// renameFile is where the harness hands a rename to a running helper.
func (s *acpLifecycleState) renameFile() string {
	return filepath.Join(s.root, "rename-title")
}

func (s *acpLifecycleState) sessionIsRenamedTo(title string) error {
	if s.sessionID == "" {
		return fmt.Errorf("no active session to rename")
	}
	s.renameTo = title
	if err := os.WriteFile(s.renameFile(), []byte(title), 0o644); err != nil {
		return err
	}
	// The pin lands on the turn that follows the rename, which is when a
	// session that was not being watched would pick it up anyway.
	return s.sendPrompt("rename")
}

func (s *acpLifecycleState) aSessionWithFirstPromptWasCreated() error {
	if err := s.createSession(nil); err != nil {
		return err
	}
	return s.sendPrompt("hello")
}

func (s *acpLifecycleState) clientDeletesTheCreatedSession() error {
	return s.request(30, "session/delete", map[string]interface{}{"sessionId": s.sessionID})
}

func (s *acpLifecycleState) sessionListNoLongerIncludesTheCreatedSession() error {
	if err := s.request(31, "session/list", map[string]interface{}{}); err != nil {
		return err
	}
	for _, row := range s.listedSessions() {
		if row["sessionId"] == s.sessionID {
			return fmt.Errorf("deleted session %s is still listed: %#v", s.sessionID, row)
		}
	}
	return nil
}

func (s *acpLifecycleState) aSessionWithAdditionalDirectoryWasCreated() error {
	s.extraDir = filepath.Join(s.root, "extra")
	if err := os.MkdirAll(s.extraDir, 0o755); err != nil {
		return err
	}
	if err := s.createSession([]string{s.extraDir}); err != nil {
		return err
	}
	// A conversation nobody wrote in is left out of session/list, so the
	// listing the scenario reads needs a message in it.
	return s.sendPrompt("hello")
}

func (s *acpLifecycleState) clientListsSessionsOfWorkspace() error {
	return s.request(40, "session/list", map[string]interface{}{"cwd": s.root})
}

func (s *acpLifecycleState) sessionListReportsTheAdditionalDirectory() error {
	for _, row := range s.listedSessions() {
		if row["sessionId"] != s.sessionID {
			continue
		}
		dirs, _ := row["additionalDirectories"].([]interface{})
		for _, d := range dirs {
			if d == s.extraDir {
				return nil
			}
		}
		return fmt.Errorf("session list reports additionalDirectories %#v, want %q", dirs, s.extraDir)
	}
	return fmt.Errorf("session %s not listed: %#v", s.sessionID, s.wire)
}

func (s *acpLifecycleState) clientResumesTheCreatedSession() error {
	// A resume is what an editor does after the agent went away and came back,
	// so the session has to be cold for the scenario to mean anything: a live
	// one is already in memory and would answer without reading its bundle.
	s.close()
	if err := s.start(); err != nil {
		return err
	}
	return s.request(50, "session/resume", map[string]interface{}{
		"sessionId": s.sessionID,
		"cwd":       s.root,
	})
}

func (s *acpLifecycleState) resumeIsNotPrecededByAReplayedTranscript() error {
	for _, msg := range s.wire {
		if _, isResponse := msg["result"]; isResponse {
			continue
		}
		params, _ := msg["params"].(map[string]interface{})
		update, _ := params["update"].(map[string]interface{})
		kind, _ := update["sessionUpdate"].(string)
		switch kind {
		case acp.UpdateTypeUserMessageChunk, acp.UpdateTypeAgentMessageChunk:
			return fmt.Errorf("session/resume replayed the transcript: %s in %#v", kind, msg)
		}
	}
	return nil
}

func (s *acpLifecycleState) resumeNamesAMode() error {
	if s.lastResult == nil {
		return fmt.Errorf("no session/resume result")
	}
	modes, _ := s.lastResult["modes"].(map[string]interface{})
	if id, _ := modes["currentModeId"].(string); id != "" {
		return nil
	}
	return fmt.Errorf("session/resume response names no mode: %#v", s.lastResult)
}

func (s *acpLifecycleState) formClientHasActiveSession() error {
	if err := s.start(); err != nil {
		return err
	}
	if err := s.request(0, "initialize", map[string]interface{}{
		"protocolVersion": acp.ProtocolVersion,
		"clientCapabilities": map[string]interface{}{
			"elicitation": map[string]interface{}{"form": map[string]interface{}{}},
		},
	}); err != nil {
		return err
	}
	return s.createSession(nil)
}

// agentAsksAndClientPicks drives a turn whose agent asks one closed question
// and answers the form the way an editor's user would: by picking a choice.
func (s *acpLifecycleState) agentAsksAndClientPicks(question, first, second, pick string) error {
	if err := writeJSONLine(s.stdin, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      60,
		"method":  "session/prompt",
		"params": map[string]interface{}{
			"sessionId": s.sessionID,
			"prompt": []map[string]interface{}{{
				"type": "text",
				"text": lifecycleAskPrefix + strings.Join([]string{question, first, second}, "|"),
			}},
		},
	}); err != nil {
		return err
	}
	before, req, err := s.readUntilRequest("elicitation/create")
	if err != nil {
		return err
	}
	s.elicitation, _ = req["params"].(map[string]interface{})
	if err := writeJSONLine(s.stdin, map[string]interface{}{
		"jsonrpc": "2.0",
		"id":      req["id"],
		"result": map[string]interface{}{
			"action":  "accept",
			"content": map[string]interface{}{"q1": pick},
		},
	}); err != nil {
		return err
	}
	after, result, err := s.readUntilResponse(60)
	if err != nil {
		return err
	}
	s.wire = append(before, after...)
	s.lastResult = result
	return nil
}

func (s *acpLifecycleState) clientWasAskedThroughElicitation(question, first, second string) error {
	if s.elicitation == nil {
		return fmt.Errorf("no elicitation/create request reached the client")
	}
	if s.elicitation["mode"] != acp.ElicitationModeForm {
		return fmt.Errorf("elicitation mode = %v, want form", s.elicitation["mode"])
	}
	if s.elicitation["message"] != question {
		return fmt.Errorf("elicitation message = %v, want %q", s.elicitation["message"], question)
	}
	schema, _ := s.elicitation["requestedSchema"].(map[string]interface{})
	props, _ := schema["properties"].(map[string]interface{})
	field, _ := props["q1"].(map[string]interface{})
	choices, _ := field["enum"].([]interface{})
	if field["type"] != "string" || len(choices) != 2 || choices[0] != first || choices[1] != second {
		return fmt.Errorf("form field q1 = %#v, want a string picker of %q and %q", field, first, second)
	}
	return nil
}

func (s *acpLifecycleState) turnHearsTheAnswer(want string) error {
	for _, msg := range s.wire {
		params, _ := msg["params"].(map[string]interface{})
		update, _ := params["update"].(map[string]interface{})
		if update["sessionUpdate"] != acp.UpdateTypeAgentMessageChunk {
			continue
		}
		content, _ := update["content"].(map[string]interface{})
		if content["text"] == lifecycleAnswerPrefix+want {
			return nil
		}
	}
	return fmt.Errorf("the turn never heard %q: %#v", want, s.wire)
}

// listedSessions is every session/list row of the last listing.
func (s *acpLifecycleState) listedSessions() []map[string]interface{} {
	if s.lastResult == nil {
		return nil
	}
	raw, _ := s.lastResult["sessions"].([]interface{})
	out := make([]map[string]interface{}, 0, len(raw))
	for _, item := range raw {
		if row, ok := item.(map[string]interface{}); ok {
			out = append(out, row)
		}
	}
	return out
}

func initializeACPLifecycleScenario(sc *godog.ScenarioContext) {
	s := &acpLifecycleState{}
	sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
		return ctx, s.reset()
	})
	sc.After(func(ctx context.Context, _ *godog.Scenario, _ error) (context.Context, error) {
		s.close()
		return ctx, nil
	})

	sc.Step(`^an ACP client has an active session$`, s.clientHasActiveSession)
	sc.Step(`^the client sends a prompt reading "([^"]+)"$`, s.clientSendsPromptReading)
	sc.Step(`^the client is told the session title is "([^"]+)"$`, s.clientIsToldSessionTitleIs)
	sc.Step(`^the session is renamed to "([^"]+)"$`, s.sessionIsRenamedTo)
	sc.Step(`^a session with a first prompt was created for the workspace$`, s.aSessionWithFirstPromptWasCreated)
	sc.Step(`^the client deletes the created session$`, s.clientDeletesTheCreatedSession)
	sc.Step(`^the session list no longer includes the created session$`, s.sessionListNoLongerIncludesTheCreatedSession)
	sc.Step(`^a session with the workspace as its additional directory was created$`, s.aSessionWithAdditionalDirectoryWasCreated)
	sc.Step(`^an ACP client lists the sessions of that workspace$`, s.clientListsSessionsOfWorkspace)
	sc.Step(`^the session list reports the created session's additional directory$`, s.sessionListReportsTheAdditionalDirectory)
	sc.Step(`^the client resumes the created session$`, s.clientResumesTheCreatedSession)
	sc.Step(`^the resume response is not preceded by a replayed transcript$`, s.resumeIsNotPrecededByAReplayedTranscript)
	sc.Step(`^the resume response names a mode$`, s.resumeNamesAMode)
	sc.Step(`^an ACP client that renders form elicitation has an active session$`, s.formClientHasActiveSession)
	sc.Step(`^the agent asks "([^"]+)" offering "([^"]+)" and "([^"]+)" and the client picks "([^"]+)"$`, s.agentAsksAndClientPicks)
	sc.Step(`^the client was asked "([^"]+)" through elicitation/create with a picker of "([^"]+)" and "([^"]+)"$`, s.clientWasAskedThroughElicitation)
	sc.Step(`^the turn hears the answer "([^"]+)"$`, s.turnHearsTheAnswer)
}

// The helper reads a prompt that starts with lifecycleAskPrefix as a question
// to put to the client: the question and its two options, separated by "|".
// It reports what the client answered as an agent message that starts with
// lifecycleAnswerPrefix.
const (
	lifecycleAskPrefix    = "ask:"
	lifecycleAnswerPrefix = "answer:"
)

func TestACPSessionLifecycleFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "acp-session-lifecycle",
		ScenarioInitializer: initializeACPLifecycleScenario,
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/acp_session_lifecycle.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("ACP session lifecycle feature suite failed")
	}
}

// TestACPLifecycleHelperProcess is the agent the lifecycle suite drives: a
// real acp server over stdio, on a store under a throwaway root, whose turns
// are recorded instead of run. A prompt becomes one user message, which is
// where the title derived from it comes from; LIFECYCLE_RENAME pins a title
// the way session_describe or an operator rename does.
func TestACPLifecycleHelperProcess(t *testing.T) {
	if os.Getenv(testLifecycleHelperEnv) != "1" {
		return
	}
	cfg := testConfig()
	cfg.Paths.Home = filepath.Join(os.Getenv(testLifecycleWorkspaceEnv), "home")
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	store := &session.FileStore{Root: os.Getenv(testLifecycleRootEnv)}

	runner := func(ctx context.Context, st *session.State, prompt []acp.ContentBlock, sender acp.UpdateSender) (string, error) {
		// The pin comes first so the title the turn announces is the one the
		// rename asked for, not the one the message below would derive first.
		if raw, err := os.ReadFile(os.Getenv(testLifecycleRenameEnv)); err == nil {
			if title := strings.TrimSpace(string(raw)); title != "" {
				st.SetTitlePinned(title)
			}
		}
		var b strings.Builder
		for _, block := range prompt {
			b.WriteString(block.Text)
		}
		text := strings.TrimSpace(b.String())
		st.AddMessage(llm.Message{Role: llm.RoleUser, Content: text})
		if spec, ok := strings.CutPrefix(text, lifecycleAskPrefix); ok {
			// The question tool's own request, as the agent hands it to the
			// ACP adapter: a closed question with two options.
			parts := strings.Split(spec, "|")
			if len(parts) != 3 {
				return "", fmt.Errorf("ask prompt %q: want question|option|option", text)
			}
			res, err := sender.RequestQuestion(ctx, acp.QuestionRequestParams{
				SessionID:  st.GetID(),
				ToolCallID: "call_question",
				Questions: []acp.QuestionPrompt{{
					Question: parts[0],
					Options:  []acp.QuestionOption{{Label: parts[1]}, {Label: parts[2]}},
					Custom:   false,
				}},
			})
			if err != nil {
				return "", err
			}
			answer := ""
			if res != nil && len(res.Answers) > 0 {
				answer = strings.Join(res.Answers[0], ",")
			}
			if err := sender.SendSessionUpdate(st.GetID(), acp.MessageChunkUpdate{
				SessionUpdate: acp.UpdateTypeAgentMessageChunk,
				Content:       acp.ContentBlock{Type: "text", Text: lifecycleAnswerPrefix + answer},
			}); err != nil {
				return "", err
			}
		}
		return string(acp.StopReasonEndTurn), nil
	}

	mgr := session.NewManager(cfg, nil, runner, log, os.Getenv(testLifecycleWorkspaceEnv), store)
	server := acp.NewServer(mgr, log)
	mgr.SetServer(server)
	if err := server.Run(context.Background(), os.Stdin); err != nil {
		t.Fatal(err)
	}
}
