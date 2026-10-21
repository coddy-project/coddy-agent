package main

// Godog harness for features/acp_provider_e2e.feature: the real coddy acp
// (runACP, the test binary started in its place) between an editor this test
// plays and scripted ACP agents (internal/llm/acptest, the test binary again)
// behind its acp rows. A coddy acp can also be the agent of an acp row, which
// is how the chain of two Coddys runs.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/llm/acptest"
)

// coddyACPSentinel is the first positional argument that turns the test
// binary into coddy acp; the arguments after it are coddy acp's own.
const coddyACPSentinel = "coddy-acp-helper"

// TestHelperCoddyACP runs coddy acp when the suite started the test binary
// with coddyACPSentinel; the ordinary run skips it.
func TestHelperCoddyACP(t *testing.T) {
	args := flag.Args()
	if len(args) == 0 || args[0] != coddyACPSentinel {
		t.Skip("helper process")
	}
	if err := runACP(args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(0)
}

// TestHelperACPAgent turns the test binary into the scripted ACP agent when
// acptest.Command started it; the ordinary run skips it.
func TestHelperACPAgent(t *testing.T) {
	if !acptest.Main(flag.Args()) {
		t.Skip("helper process")
	}
}

// coddyACPCommand is the program and arguments that start the test binary as
// coddy acp on home.
func coddyACPCommand(home string) (string, []string) {
	return os.Args[0], []string{"-test.run=^TestHelperCoddyACP$", "--", coddyACPSentinel, "--home", home}
}

// acpWire is one JSON-RPC message on the editor's line.
type acpWire struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  json.RawMessage `json:"error,omitempty"`
}

// acpEditor speaks ACP to a coddy acp process the way an editor does: a line
// per message, every notification kept, each permission request answered
// with the first option of the kind the test chose.
type acpEditor struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stderr bytes.Buffer
	done   chan struct{}

	writeMu sync.Mutex
	mu      sync.Mutex
	nextID  int
	pending map[string]chan acpWire
	updates []json.RawMessage
	asked   []json.RawMessage
	// allow is the option kind a permission request is answered with.
	allow string
}

func startACPEditor(home, workspace string) (*acpEditor, error) {
	name, args := coddyACPCommand(home)
	cmd := exec.Command(name, args...)
	cmd.Dir = workspace
	// The person's own home holds skills and rules this suite does not want.
	cmd.Env = append(os.Environ(), "CODDY_HOME="+home, "HOME="+filepath.Join(home, "user"), "USERPROFILE="+filepath.Join(home, "user"))
	e := &acpEditor{cmd: cmd, done: make(chan struct{}), pending: map[string]chan acpWire{}, allow: "allow_once"}
	cmd.Stderr = &syncWriter{w: &e.stderr, mu: &e.mu}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	e.stdin = stdin
	go e.read(stdout)
	if _, err := e.call("initialize", map[string]any{"protocolVersion": 1, "clientCapabilities": map[string]any{}}); err != nil {
		e.close()
		return nil, err
	}
	return e, nil
}

// syncWriter guards the stderr buffer the process writes while a step reads.
type syncWriter struct {
	w  io.Writer
	mu *sync.Mutex
}

func (s *syncWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.w.Write(p)
}

func (e *acpEditor) send(v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	e.writeMu.Lock()
	defer e.writeMu.Unlock()
	_, err = e.stdin.Write(append(raw, '\n'))
	return err
}

func (e *acpEditor) read(stdout io.Reader) {
	defer close(e.done)
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for sc.Scan() {
		var m acpWire
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			continue
		}
		switch {
		case m.Method != "" && len(m.ID) > 0:
			e.answer(m)
		case m.Method == "session/update":
			e.mu.Lock()
			e.updates = append(e.updates, m.Params)
			e.mu.Unlock()
		case m.Method == "":
			e.mu.Lock()
			ch := e.pending[string(m.ID)]
			delete(e.pending, string(m.ID))
			e.mu.Unlock()
			if ch != nil {
				ch <- m
			}
		}
	}
}

// answer replies to a request of the agent: a permission request with the
// first option of the chosen kind, anything else as unknown.
func (e *acpEditor) answer(m acpWire) {
	if m.Method != "session/request_permission" {
		_ = e.send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "error": map[string]any{"code": -32601, "message": "method not found"}})
		return
	}
	e.mu.Lock()
	e.asked = append(e.asked, m.Params)
	kind := e.allow
	e.mu.Unlock()
	var p struct {
		Options []struct {
			OptionID string `json:"optionId"`
			Kind     string `json:"kind"`
		} `json:"options"`
	}
	_ = json.Unmarshal(m.Params, &p)
	outcome := map[string]any{"outcome": "cancelled"}
	for _, o := range p.Options {
		if o.Kind == kind {
			outcome = map[string]any{"outcome": "selected", "optionId": o.OptionID}
			break
		}
	}
	_ = e.send(map[string]any{"jsonrpc": "2.0", "id": m.ID, "result": map[string]any{"outcome": outcome}})
}

func (e *acpEditor) call(method string, params any) (json.RawMessage, error) {
	e.mu.Lock()
	e.nextID++
	n := e.nextID
	id := fmt.Sprintf("%d", n)
	ch := make(chan acpWire, 1)
	e.pending[id] = ch
	e.mu.Unlock()
	if err := e.send(map[string]any{"jsonrpc": "2.0", "id": n, "method": method, "params": params}); err != nil {
		return nil, err
	}
	select {
	case m := <-ch:
		if len(m.Error) > 0 {
			return nil, fmt.Errorf("%s: %s", method, m.Error)
		}
		return m.Result, nil
	case <-e.done:
		return nil, fmt.Errorf("%s: coddy acp exited: %s", method, e.stderrTail())
	case <-time.After(90 * time.Second):
		return nil, fmt.Errorf("%s: no answer in 90 s: %s", method, e.stderrTail())
	}
}

func (e *acpEditor) stderrTail() string {
	e.mu.Lock()
	defer e.mu.Unlock()
	s := e.stderr.String()
	if len(s) > 2000 {
		s = s[len(s)-2000:]
	}
	return s
}

// newSession opens a session in workspace on model and returns its id and
// the model ids its model option offers.
func (e *acpEditor) newSession(workspace, model string) (string, []string, error) {
	raw, err := e.call("session/new", map[string]any{"cwd": workspace, "mcpServers": []any{}})
	if err != nil {
		return "", nil, err
	}
	var res struct {
		SessionID     string `json:"sessionId"`
		ConfigOptions []struct {
			ID      string `json:"id"`
			Options []struct {
				Value string `json:"value"`
			} `json:"options"`
		} `json:"configOptions"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return "", nil, err
	}
	var models []string
	for _, o := range res.ConfigOptions {
		if o.ID == "model" {
			for _, v := range o.Options {
				models = append(models, v.Value)
			}
		}
	}
	if _, err := e.call("session/set_config_option", map[string]any{"sessionId": res.SessionID, "configId": "model", "value": model}); err != nil {
		return "", nil, err
	}
	return res.SessionID, models, nil
}

// waitFor waits for a session/update of kind about the session.
func (e *acpEditor) waitFor(sessionID, kind string) error {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		e.mu.Lock()
		for _, raw := range e.updates {
			var p struct {
				SessionID string `json:"sessionId"`
				Update    struct {
					SessionUpdate string `json:"sessionUpdate"`
				} `json:"update"`
			}
			if json.Unmarshal(raw, &p) == nil && p.SessionID == sessionID && p.Update.SessionUpdate == kind {
				e.mu.Unlock()
				return nil
			}
		}
		e.mu.Unlock()
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("no %s for %s", kind, sessionID)
}

// prompt asks text in the session and returns the stop reason.
func (e *acpEditor) prompt(sessionID, text string) (string, error) {
	raw, err := e.call("session/prompt", map[string]any{"sessionId": sessionID, "prompt": []any{map[string]any{"type": "text", "text": text}}})
	if err != nil {
		return "", err
	}
	var res struct {
		StopReason string `json:"stopReason"`
	}
	err = json.Unmarshal(raw, &res)
	return res.StopReason, err
}

// streamed returns the answer text and the reasoning the session's updates
// carried.
func (e *acpEditor) streamed(sessionID string) (text, reasoning string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	var t, r strings.Builder
	for _, raw := range e.updates {
		var p struct {
			SessionID string `json:"sessionId"`
			Update    struct {
				SessionUpdate string `json:"sessionUpdate"`
				Content       struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"update"`
		}
		if json.Unmarshal(raw, &p) != nil || p.SessionID != sessionID {
			continue
		}
		switch p.Update.SessionUpdate {
		case "agent_message_chunk":
			if p.Update.Content.Type == "reasoning" {
				r.WriteString(p.Update.Content.Text)
			} else {
				t.WriteString(p.Update.Content.Text)
			}
		case "agent_thought_chunk":
			r.WriteString(p.Update.Content.Text)
		}
	}
	return t.String(), r.String()
}

// editorToolRow is a tool row as an editor draws it from tool_call and
// tool_call_update.
type editorToolRow struct {
	id, title, status, output string
}

// toolRows returns the tool rows the session's updates drew, from update
// from on, in the order they were announced.
func (e *acpEditor) toolRows(sessionID string, from int) []editorToolRow {
	e.mu.Lock()
	defer e.mu.Unlock()
	var order []string
	rows := map[string]*editorToolRow{}
	for _, raw := range e.updates[min(from, len(e.updates)):] {
		var p struct {
			SessionID string `json:"sessionId"`
			Update    struct {
				SessionUpdate string `json:"sessionUpdate"`
				ToolCallID    string `json:"toolCallId"`
				Title         string `json:"title"`
				Status        string `json:"status"`
				Content       []struct {
					Content struct {
						Text string `json:"text"`
					} `json:"content"`
				} `json:"content"`
			} `json:"update"`
		}
		if json.Unmarshal(raw, &p) != nil || p.SessionID != sessionID {
			continue
		}
		u := p.Update
		switch u.SessionUpdate {
		case "tool_call":
			if rows[u.ToolCallID] == nil {
				order = append(order, u.ToolCallID)
				rows[u.ToolCallID] = &editorToolRow{id: u.ToolCallID}
			}
			rows[u.ToolCallID].title, rows[u.ToolCallID].status = u.Title, u.Status
		case "tool_call_update":
			r := rows[u.ToolCallID]
			if r == nil {
				continue
			}
			r.status = u.Status
			if u.Status != "in_progress" && len(u.Content) > 0 {
				r.output = u.Content[0].Content.Text
			}
		}
	}
	out := make([]editorToolRow, 0, len(order))
	for _, id := range order {
		out = append(out, *rows[id])
	}
	return out
}

// close lets go of coddy acp the way an editor does: its stdin ends, and it
// stops its agents on the way out.
func (e *acpEditor) close() {
	if e == nil || e.cmd == nil {
		return
	}
	_ = e.stdin.Close()
	exited := make(chan struct{})
	go func() {
		_ = e.cmd.Wait()
		close(exited)
	}()
	select {
	case <-exited:
	case <-time.After(15 * time.Second):
		_ = e.cmd.Process.Kill()
		<-exited
	}
	e.cmd = nil
}

// acpRow is one acp row of a test home.
type acpRow struct {
	name    string
	command string
	args    []string
	modes   string
}

// writeACPHome writes a config.yaml with the rows, the model ids and
// agent.model, readable by its owner only.
func writeACPHome(home string, rows []acpRow, models []string) error {
	if err := os.MkdirAll(filepath.Join(home, "user"), 0o755); err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("providers:\n")
	for _, r := range rows {
		quoted := make([]string, len(r.args))
		for i, a := range r.args {
			quoted[i] = fmt.Sprintf("%q", a)
		}
		fmt.Fprintf(&b, "  - name: %s\n    type: acp\n    acp:\n      command: %q\n      args: [%s]\n", r.name, r.command, strings.Join(quoted, ", "))
		if r.modes != "" {
			fmt.Fprintf(&b, "      modes: %s\n", r.modes)
		}
	}
	b.WriteString("models:\n")
	for _, m := range models {
		fmt.Fprintf(&b, "  - model: %s\n", m)
	}
	fmt.Fprintf(&b, "agent:\n  model: %s\nlogger:\n  level: warn\n", models[0])
	return os.WriteFile(filepath.Join(home, "config.yaml"), []byte(b.String()), 0o600)
}

type acpE2EState struct {
	t      *testing.T
	root   string
	home   string
	inner  string
	ws     string
	logs   map[string]string
	editor *acpEditor
	sid    string
	models []string
	// loaded is how many updates the editor had once session/load replayed
	// the conversation.
	loaded int
	// questions maps the sessions of the concurrent scenario to what each
	// was asked.
	questions map[string]string
	restore   []func()
}

func (s *acpE2EState) reset() error {
	s.close()
	root, err := os.MkdirTemp("", "coddy-acp-e2e-*")
	if err != nil {
		return err
	}
	*s = acpE2EState{t: s.t, root: root, home: filepath.Join(root, "home"), inner: filepath.Join(root, "inner"), ws: filepath.Join(root, "ws"), logs: map[string]string{}}
	if err := os.MkdirAll(s.ws, 0o755); err != nil {
		return err
	}
	// A run inside an agent Coddy started still starts this chain at the
	// top.
	prev, had := os.LookupEnv(llm.ACPDepthEnv)
	_ = os.Unsetenv(llm.ACPDepthEnv)
	s.restore = append(s.restore, func() {
		if had {
			_ = os.Setenv(llm.ACPDepthEnv, prev)
		}
	})
	return nil
}

func (s *acpE2EState) close() {
	s.editor.close()
	s.editor = nil
	llm.StopACPAgents()
	for i := len(s.restore) - 1; i >= 0; i-- {
		s.restore[i]()
	}
	s.restore = nil
	if s.root != "" {
		_ = os.RemoveAll(s.root)
		s.root = ""
	}
}

// fakeRow is an acp row named name running script, its log kept by name.
func (s *acpE2EState) fakeRow(name string, script acptest.Script) acpRow {
	script.Log = filepath.Join(s.root, name+".log")
	s.logs[name] = script.Log
	cmd, args := acptest.Command(s.t, script)
	return acpRow{name: name, command: cmd, args: args, modes: "{plan: read, ask: read}"}
}

var echoTurn = acptest.Turn{Thoughts: []string{"looking"}, Tools: []acptest.Tool{{ID: "t1", Title: "Read README.md", Kind: "read", Output: "# Readme"}}, Echo: true}

var acpAgentModes = []string{"default", "read"}

func (s *acpE2EState) twoRows() error {
	alpha := s.fakeRow("alpha", acptest.Script{Models: []string{"small", "large"}, Modes: acpAgentModes, Close: true, Turns: []acptest.Turn{echoTurn}})
	beta := s.fakeRow("beta", acptest.Script{Modes: acpAgentModes, Close: true, Turns: []acptest.Turn{echoTurn}})
	return writeACPHome(s.home, []acpRow{alpha, beta}, []string{"alpha/default", "alpha/large", "beta/default"})
}

func (s *acpE2EState) twoRowsWithBarrier(n int) error {
	turn := acptest.Turn{Echo: true, Barrier: n}
	alpha := s.fakeRow("alpha", acptest.Script{Modes: acpAgentModes, Close: true, Turns: []acptest.Turn{turn}})
	beta := s.fakeRow("beta", acptest.Script{Modes: acpAgentModes, Close: true, Turns: []acptest.Turn{turn}})
	return writeACPHome(s.home, []acpRow{alpha, beta}, []string{"alpha/default", "beta/default"})
}

func askingTurn(title string) acptest.Turn {
	return acptest.Turn{Permission: &acptest.Permission{
		Title: title, Kind: "edit", Content: "the change",
		Options: []acptest.Option{{ID: "ok", Name: "Allow", Kind: "allow_once"}, {ID: "no", Name: "Reject", Kind: "reject_once"}},
	}, Echo: true}
}

func (s *acpE2EState) rowThatAsks(row, title string) error {
	r := s.fakeRow(row, acptest.Script{Modes: acpAgentModes, Close: true, Turns: []acptest.Turn{askingTurn(title)}})
	return writeACPHome(s.home, []acpRow{r}, []string{row + "/default"})
}

func (s *acpE2EState) rowThatWaitsToBeStopped(row, text string) error {
	r := s.fakeRow(row, acptest.Script{Modes: acpAgentModes, Close: true, Turns: []acptest.Turn{{Text: []string{text}, WaitCancel: true}, {Echo: true}}})
	return writeACPHome(s.home, []acpRow{r}, []string{row + "/default"})
}

// nested writes the inner home, whose row "inner" runs the fake agent of
// turn, and the home of coddy acp, whose row runs coddy acp on the inner
// home.
func (s *acpE2EState) nested(row string, turn acptest.Turn) error {
	inner := s.fakeRow("inner", acptest.Script{Models: []string{"m1"}, Modes: acpAgentModes, Close: true, Turns: []acptest.Turn{turn}})
	if err := writeACPHome(s.inner, []acpRow{inner}, []string{"inner/default", "inner/large"}); err != nil {
		return err
	}
	cmd, args := coddyACPCommand(s.inner)
	return writeACPHome(s.home, []acpRow{{name: row, command: cmd, args: args, modes: "{plan: plan, ask: ask}"}}, []string{row + "/default"})
}

func (s *acpE2EState) nestedEcho(row, innerRow string) error {
	if innerRow != "inner" {
		return fmt.Errorf("the inner row is named inner, not %q", innerRow)
	}
	return s.nested(row, acptest.Turn{Thoughts: []string{"looking"}, Tools: []acptest.Tool{{ID: "x1", Title: "Grep TODO", Kind: "search", Output: "main.go:3: TODO"}}, Echo: true, Env: []string{llm.ACPDepthEnv}})
}

func (s *acpE2EState) nestedAsking(row, title string) error {
	return s.nested(row, askingTurn(title))
}

func (s *acpE2EState) startEditor() error {
	if s.editor != nil {
		return nil
	}
	e, err := startACPEditor(s.home, s.ws)
	if err != nil {
		return err
	}
	s.editor = e
	return nil
}

func (s *acpE2EState) openAndAsk(model, text string) error {
	if err := s.startEditor(); err != nil {
		return err
	}
	sid, models, err := s.editor.newSession(s.ws, model)
	if err != nil {
		return err
	}
	s.sid, s.models = sid, models
	reason, err := s.editor.prompt(sid, text)
	if err != nil {
		return err
	}
	if reason != "end_turn" {
		return fmt.Errorf("stop reason %q", reason)
	}
	return nil
}

func (s *acpE2EState) openAskAndAllow(model, text string) error {
	return s.openAndAsk(model, text)
}

func (s *acpE2EState) sees(want string) error {
	text, _ := s.editor.streamed(s.sid)
	if !strings.HasPrefix(text, want) {
		return fmt.Errorf("answer = %q, want it to open with %q", text, want)
	}
	return nil
}

func (s *acpE2EState) seesTool(title, output string) error {
	_, reasoning := s.editor.streamed(s.sid)
	if strings.Contains(reasoning, title) {
		return fmt.Errorf("the agent's tool went into the reasoning: %q", reasoning)
	}
	rows := s.editor.toolRows(s.sid, 0)
	for _, r := range rows {
		if r.title == title {
			if r.status != "completed" || r.output != output {
				return fmt.Errorf("row %q = %+v, want completed with %q", title, r, output)
			}
			return nil
		}
	}
	return fmt.Errorf("no tool row %q among %+v", title, rows)
}

func (s *acpE2EState) shownAgainOnLoad(title string) error {
	s.editor.mu.Lock()
	replay := append([]json.RawMessage(nil), s.editor.updates[:s.loaded]...)
	s.editor.mu.Unlock()
	replayed := (&acpEditor{updates: replay}).toolRows(s.sid, 0)
	for _, r := range replayed {
		if r.title == title && r.status == "completed" && r.output != "" {
			return nil
		}
	}
	return fmt.Errorf("the loaded session replayed no row %q: %+v", title, replayed)
}

func splitList(s string) []string {
	var out []string
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func (s *acpE2EState) offersModels(list string) error {
	for _, want := range splitList(list) {
		found := false
		for _, m := range s.models {
			found = found || m == want
		}
		if !found {
			return fmt.Errorf("model option = %v, want %q in it", s.models, want)
		}
	}
	return nil
}

func (s *acpE2EState) editorAsked(title string) error {
	s.editor.mu.Lock()
	defer s.editor.mu.Unlock()
	if len(s.editor.asked) != 1 {
		return fmt.Errorf("the editor was asked %d times", len(s.editor.asked))
	}
	var p struct {
		ToolCall struct {
			Title string `json:"title"`
		} `json:"toolCall"`
	}
	if err := json.Unmarshal(s.editor.asked[0], &p); err != nil {
		return err
	}
	if p.ToolCall.Title != title {
		return fmt.Errorf("asked about %q, want %q", p.ToolCall.Title, title)
	}
	return nil
}

func (s *acpE2EState) agentHeardAllow() error {
	text, _ := s.editor.streamed(s.sid)
	if !strings.Contains(text, "permission: ok") {
		return fmt.Errorf("answer = %q, want the agent's allow option", text)
	}
	return nil
}

// askAtOnce opens n sessions on each model and asks all of them before any
// answer is in.
func (s *acpE2EState) askAtOnce(n int, modelA string, m int, modelB string) error {
	if err := s.startEditor(); err != nil {
		return err
	}
	s.questions = map[string]string{}
	type job struct{ sid, q string }
	var jobs []job
	for _, spec := range []struct {
		model string
		n     int
	}{{modelA, n}, {modelB, m}} {
		for i := 1; i <= spec.n; i++ {
			sid, _, err := s.editor.newSession(s.ws, spec.model)
			if err != nil {
				return err
			}
			q := fmt.Sprintf("question %d for %s", i, spec.model)
			s.questions[sid] = q
			jobs = append(jobs, job{sid, q})
		}
	}
	errs := make(chan error, len(jobs))
	for _, j := range jobs {
		go func(j job) {
			reason, err := s.editor.prompt(j.sid, j.q)
			if err == nil && reason != "end_turn" {
				err = fmt.Errorf("stop reason %q", reason)
			}
			errs <- err
		}(j)
	}
	for range jobs {
		if err := <-errs; err != nil {
			return err
		}
	}
	return nil
}

func (s *acpE2EState) everySessionOwnAnswer() error {
	for sid, q := range s.questions {
		text, _ := s.editor.streamed(sid)
		if !strings.HasPrefix(text, "echo: "+q) {
			return fmt.Errorf("session asked %q answered %q", q, text)
		}
		if !strings.Contains(text, "barrier: ") || strings.Contains(text, "barrier: timeout") {
			return fmt.Errorf("session asked %q did not meet the others: %q", q, text)
		}
	}
	return nil
}

func (s *acpE2EState) oneProcessPerRow(n int) error {
	for _, row := range []string{"alpha", "beta"} {
		pids := map[int]bool{}
		sessions := map[string]bool{}
		for _, r := range acptest.Methods(acptest.ReadLog(s.t, s.logs[row]), "session/prompt") {
			pids[r.PID] = true
			var p struct {
				SessionID string `json:"sessionId"`
			}
			_ = json.Unmarshal(r.Params, &p)
			sessions[p.SessionID] = true
		}
		if len(pids) != 1 || len(sessions) != n {
			return fmt.Errorf("row %s: %d processes, %d sessions; want 1 and %d", row, len(pids), len(sessions), n)
		}
	}
	return nil
}

func (s *acpE2EState) restartLoadAndAsk(text string) error {
	s.editor.close()
	s.editor = nil
	if err := s.startEditor(); err != nil {
		return err
	}
	if _, err := s.editor.call("session/load", map[string]any{"sessionId": s.sid, "cwd": s.ws, "mcpServers": []any{}}); err != nil {
		return err
	}
	// The replay is done once the slash command catalog that closes a loaded
	// session arrived; what came before it is the replay alone.
	if err := s.editor.waitFor(s.sid, "available_commands_update"); err != nil {
		return err
	}
	s.editor.mu.Lock()
	s.loaded = len(s.editor.updates)
	s.editor.mu.Unlock()
	reason, err := s.editor.prompt(s.sid, text)
	if err != nil {
		return err
	}
	if reason != "end_turn" {
		return fmt.Errorf("stop reason %q", reason)
	}
	return nil
}

// promptsOf returns the text blocks of each session/prompt a row's agents
// received, with the process and session of each.
func (s *acpE2EState) promptsOf(row string) (texts [][]string, pids []int, sessions []string) {
	for _, r := range acptest.Methods(acptest.ReadLog(s.t, s.logs[row]), "session/prompt") {
		var p struct {
			SessionID string `json:"sessionId"`
			Prompt    []struct {
				Text string `json:"text"`
			} `json:"prompt"`
		}
		_ = json.Unmarshal(r.Params, &p)
		var blocks []string
		for _, b := range p.Prompt {
			blocks = append(blocks, b.Text)
		}
		texts = append(texts, blocks)
		pids = append(pids, r.PID)
		sessions = append(sessions, p.SessionID)
	}
	return texts, pids, sessions
}

func (s *acpE2EState) newSessionGotConversation(first, second string) error {
	texts, pids, _ := s.promptsOf("alpha")
	if len(texts) != 2 || pids[0] == pids[1] {
		return fmt.Errorf("prompts %d from processes %v; want two, one per coddy acp", len(texts), pids)
	}
	joined := strings.Join(texts[1], "\n")
	iFirst, iSecond := strings.Index(joined, "## User\n"+first), strings.Index(joined, second)
	if iFirst < 0 || iSecond < iFirst {
		return fmt.Errorf("the new session's first prompt = %q", joined)
	}
	return nil
}

func (s *acpE2EState) askAndStop(model, text, partial string) error {
	if err := s.startEditor(); err != nil {
		return err
	}
	sid, _, err := s.editor.newSession(s.ws, model)
	if err != nil {
		return err
	}
	s.sid = sid
	result := make(chan error, 1)
	go func() {
		reason, err := s.editor.prompt(sid, text)
		if err == nil && reason != "cancelled" {
			err = fmt.Errorf("stop reason %q, want cancelled", reason)
		}
		result <- err
	}()
	deadline := time.Now().Add(60 * time.Second)
	for {
		if got, _ := s.editor.streamed(sid); strings.Contains(got, partial) {
			break
		}
		if time.Now().After(deadline) {
			return errors.New("the partial answer never arrived")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := s.editor.send(map[string]any{"jsonrpc": "2.0", "method": "session/cancel", "params": map[string]any{"sessionId": sid}}); err != nil {
		return err
	}
	return <-result
}

func (s *acpE2EState) editorAsks(text string) error {
	reason, err := s.editor.prompt(s.sid, text)
	if err != nil {
		return err
	}
	if reason != "end_turn" {
		return fmt.Errorf("stop reason %q", reason)
	}
	return nil
}

func (s *acpE2EState) nextPromptCarriedOnly(text string) error {
	texts, _, sessions := s.promptsOf("slow")
	if len(texts) != 2 || sessions[0] != sessions[1] {
		return fmt.Errorf("prompts %d on sessions %v; want two on one", len(texts), sessions)
	}
	if len(texts[1]) == 0 || texts[1][0] != text {
		return fmt.Errorf("the next prompt opened with %q, want %q alone", texts[1], text)
	}
	return nil
}

func (s *acpE2EState) innerTwoLevelsDown() error {
	text, _ := s.editor.streamed(s.sid)
	if !strings.Contains(text, llm.ACPDepthEnv+"=2") {
		return fmt.Errorf("answer = %q, want the inner agent at depth 2", text)
	}
	return nil
}

func (s *acpE2EState) rowListsModels(row, list string) error {
	cmd, args := coddyACPCommand(s.inner)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	defer llm.StopACPAgents()
	listed, err := llm.ListModels(ctx, llm.ProviderInput{Name: row, Type: "acp", ACP: &config.ACPProviderConfig{Command: cmd, Args: args}})
	if err != nil {
		return err
	}
	var ids []string
	for _, m := range listed {
		ids = append(ids, m.ID)
	}
	want := splitList(list)
	sort.Strings(want)
	got := append([]string(nil), ids...)
	sort.Strings(got)
	if strings.Join(got, ",") != strings.Join(want, ",") {
		return fmt.Errorf("models = %v, want %v", ids, want)
	}
	return nil
}

func initializeACPProviderE2EScenario(t *testing.T) func(*godog.ScenarioContext) {
	return func(sc *godog.ScenarioContext) {
		s := &acpE2EState{t: t}
		sc.Before(func(ctx context.Context, _ *godog.Scenario) (context.Context, error) {
			return ctx, s.reset()
		})
		sc.After(func(ctx context.Context, _ *godog.Scenario, err error) (context.Context, error) {
			if err != nil && s.editor != nil {
				t.Logf("coddy acp stderr:\n%s", s.editor.stderrTail())
			}
			s.close()
			return ctx, nil
		})
		sc.Step(`^coddy acp whose acp rows "alpha" and "beta" run scripted agents$`, s.twoRows)
		sc.Step(`^coddy acp whose acp rows "alpha" and "beta" run agents that answer only (\d+) prompts at once$`, s.twoRowsWithBarrier)
		sc.Step(`^coddy acp whose acp row "([^"]*)" runs an agent that asks to "([^"]*)"$`, s.rowThatAsks)
		sc.Step(`^coddy acp whose acp row "([^"]*)" runs an agent that writes "([^"]*)" and waits to be stopped$`, s.rowThatWaitsToBeStopped)
		sc.Step(`^coddy acp whose acp row "([^"]*)" runs another coddy acp serving the acp row "([^"]*)"$`, s.nestedEcho)
		sc.Step(`^coddy acp whose acp row "([^"]*)" runs another coddy acp serving an agent that asks to "([^"]*)"$`, s.nestedAsking)
		sc.Step(`^an editor opens a session on "([^"]*)" and asks "([^"]*)"$`, s.openAndAsk)
		sc.Step(`^an editor opens a session on "([^"]*)", asks "([^"]*)" and allows what it is asked$`, s.openAskAndAllow)
		sc.Step(`^an editor opens a session on "([^"]*)", asks "([^"]*)" and stops it once "([^"]*)" arrives$`, s.askAndStop)
		sc.Step(`^an editor asks (\d+) sessions on "([^"]*)" and (\d+) on "([^"]*)" at once$`, s.askAtOnce)
		sc.Step(`^coddy acp starts again and the editor loads the session and asks "([^"]*)"$`, s.restartLoadAndAsk)
		sc.Step(`^the editor asks "([^"]*)"$`, s.editorAsks)
		sc.Step(`^the editor sees the answer "([^"]*)"$`, s.sees)
		sc.Step(`^the editor sees the agent's tool "([^"]*)" as a row with the output "([^"]*)"$`, s.seesTool)
		sc.Step(`^the editor was shown the agent's tool "([^"]*)" again when it loaded the session$`, s.shownAgainOnLoad)
		sc.Step(`^the session offers the models "([^"]*)"$`, s.offersModels)
		sc.Step(`^the editor was asked about "([^"]*)"$`, s.editorAsked)
		sc.Step(`^the agent heard its allow option$`, s.agentHeardAllow)
		sc.Step(`^every session gets the answer to its own question$`, s.everySessionOwnAnswer)
		sc.Step(`^each row ran one agent process for its (\d+) sessions$`, s.oneProcessPerRow)
		sc.Step(`^the agent's new session got the conversation with "([^"]*)" before "([^"]*)"$`, s.newSessionGotConversation)
		sc.Step(`^the agent's next prompt carried only "([^"]*)"$`, s.nextPromptCarriedOnly)
		sc.Step(`^the inner agent ran two levels down$`, s.innerTwoLevelsDown)
		sc.Step(`^the row "([^"]*)" lists the models "([^"]*)"$`, s.rowListsModels)
	}
}

func TestACPProviderE2EFeature(t *testing.T) {
	suite := godog.TestSuite{
		Name:                "acp_provider_e2e",
		ScenarioInitializer: initializeACPProviderE2EScenario(t),
		Options: &godog.Options{
			Format:   "pretty",
			Paths:    []string{"../../features/acp_provider_e2e.feature"},
			TestingT: t,
			Strict:   true,
		},
	}
	if suite.Run() != 0 {
		t.Fatal("acp_provider_e2e feature failed")
	}
}
