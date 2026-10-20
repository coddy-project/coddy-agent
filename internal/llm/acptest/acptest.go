// Package acptest is a scripted Agent Client Protocol agent for tests of the
// acp provider. It re-executes the current test binary as the agent process,
// so the provider is exercised over real pipes and a real process without a
// model or a network.
//
// A test binary that wants to act as the agent defines a test named
// TestHelperACPAgent that calls Main(flag.Args()); Command returns the program
// and arguments that start that binary with
// "-test.run=^TestHelperACPAgent$ -- acp-agent-helper <script.json>", so the
// ordinary test run (no positional arguments) skips the helper.
//
// The agent appends every message it receives to Script.Log (one JSON object
// per line), which is how a test sees what the provider sent.
package acptest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Sentinel is the first positional argument that turns a test binary into the
// fake agent.
const Sentinel = "acp-agent-helper"

// Script tells the fake agent what to advertise and how to answer.
type Script struct {
	// Name is agentInfo.name.
	Name string `json:"name,omitempty"`
	// Log is the file every received message is appended to.
	Log string `json:"log"`
	// AuthMethods are advertised in the initialize result; RequireAuth names
	// the methodId session/new insists on (-32000 until it was called).
	AuthMethods []string `json:"auth_methods,omitempty"`
	RequireAuth string   `json:"require_auth,omitempty"`
	// Image advertises promptCapabilities.image; Close sessionCapabilities.close.
	Image bool `json:"image,omitempty"`
	Close bool `json:"close,omitempty"`
	// Modes are advertised both as modes.availableModes and as a config option
	// of category mode, the first one current; with LegacyModes only as
	// modes.availableModes, answered by session/set_mode.
	Modes       []string `json:"modes,omitempty"`
	LegacyModes bool     `json:"legacy_modes,omitempty"`
	// Models are advertised as a config option of category model, or, with
	// LegacyModels, as models.availableModels answered by session/set_model.
	Models       []string `json:"models,omitempty"`
	LegacyModels bool     `json:"legacy_models,omitempty"`
	// ThoughtLevels are a config option of category thought_level;
	// RefuseThought is a level of them set_config_option refuses (an agent
	// whose mode does not take it).
	ThoughtLevels []string `json:"thought_levels,omitempty"`
	RefuseThought string   `json:"refuse_thought,omitempty"`
	// Turns answer the prompts in the order they arrive across the process;
	// the last one repeats. No turns: every prompt is echoed.
	Turns []Turn `json:"turns,omitempty"`
	// StartDelayMS holds the initialize answer back.
	StartDelayMS int `json:"start_delay_ms,omitempty"`
	// DeafAfterNew stops reading stdin once a session is open: an agent
	// that hangs without exiting.
	DeafAfterNew bool `json:"deaf_after_new,omitempty"`
	// Banner is a line written to stdout before anything else, not JSON-RPC.
	Banner string `json:"banner,omitempty"`
	// ExitAfterNewError exits right after refusing session/new for want of
	// authentication.
	ExitAfterNewError bool `json:"exit_after_new_error,omitempty"`
	// ModeAfterNew sends a current_mode_update right after the session/new
	// answer.
	ModeAfterNew string `json:"mode_after_new,omitempty"`
}

// Turn is the answer to one session/prompt.
type Turn struct {
	Thoughts []string `json:"thoughts,omitempty"`
	// Status sends status updates of the agent's own (its commands, its
	// usage) before anything else: they say nothing about the prompt.
	Status bool `json:"status,omitempty"`
	// CoddyReasoning is reasoning sent the way Coddy's own ACP server sends
	// it: agent_message_chunk blocks of type "reasoning".
	CoddyReasoning []string `json:"coddy_reasoning,omitempty"`
	// Tools are announced (tool_call) before the text, a failed one updated to
	// failed afterwards.
	Tools []Tool `json:"tools,omitempty"`
	// Permission asks the client before the text; the text then names the
	// option the client chose ("permission: <optionId>" or "permission:
	// cancelled").
	Permission *Permission `json:"permission,omitempty"`
	// ClientRequest sends a request of this method to the client before the
	// text and adds "client: <error message or ok>" to the text.
	ClientRequest string   `json:"client_request,omitempty"`
	Text          []string `json:"text,omitempty"`
	// Echo answers with "echo: " and the prompt's text blocks joined.
	Echo bool `json:"echo,omitempty"`
	// Env adds "<NAME>=<value>" lines for these variables of the agent's
	// environment.
	Env []string `json:"env,omitempty"`
	// WaitCancel holds the answer until session/cancel arrives, then answers
	// cancelled. IgnoreCancel holds it for good: the prompt never ends.
	WaitCancel   bool `json:"wait_cancel,omitempty"`
	IgnoreCancel bool `json:"ignore_cancel,omitempty"`
	// ExitFirst ends the process as soon as the prompt arrives, before any
	// update.
	ExitFirst bool `json:"exit_first,omitempty"`
	// Quiet leaves the permission and client lines out of the text (a demo
	// stand that shows the answer to a person).
	Quiet bool `json:"quiet,omitempty"`
	// Exit ends the process in the middle of the prompt, after the text.
	Exit bool `json:"exit,omitempty"`
	// Error answers the prompt with a JSON-RPC error instead of a result;
	// ErrorAfterTools sends it after the tool calls, an agent that failed
	// once it had acted.
	Error           string `json:"error,omitempty"`
	ErrorAfterTools bool   `json:"error_after_tools,omitempty"`
	StopReason      string `json:"stop_reason,omitempty"`
	// Usage is the result's usage object.
	Usage   map[string]int `json:"usage,omitempty"`
	DelayMS int            `json:"delay_ms,omitempty"`
	// ExitAfterReply exits right after the prompt's answer or error.
	ExitAfterReply bool `json:"exit_after_reply,omitempty"`
	// Chunks sends this many more one-letter text chunks before the answer.
	Chunks int `json:"chunks,omitempty"`
	// Barrier holds the prompt until this many prompts of the process have
	// reached it, then answers with "barrier: <n>"; after BarrierWait (10 s
	// when 0) it gives up and answers "barrier: timeout". Prompts a client
	// sends one after another do not meet: the first ones time out.
	Barrier     int `json:"barrier,omitempty"`
	BarrierWait int `json:"barrier_wait_ms,omitempty"`
}

// Tool is one tool call the fake agent reports.
type Tool struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Fail  bool   `json:"fail,omitempty"`
	// Path is a file in the session's folder the call works on, named the
	// way OpenCode names it: the call is announced with an empty input, and
	// an update in progress carries the file as a location and as filePath.
	Path string `json:"path,omitempty"`
}

// Permission is the request a turn sends the client.
type Permission struct {
	Title   string   `json:"title"`
	Kind    string   `json:"kind,omitempty"`
	Content string   `json:"content,omitempty"`
	Options []Option `json:"options"`
}

// Option is one option of a permission request.
type Option struct {
	ID   string `json:"optionId"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// Command returns the program and arguments that start the current test
// binary as the fake agent running script, after writing the script to a
// temporary file of t.
func Command(t testing.TB, script Script) (string, []string) {
	t.Helper()
	dir := t.TempDir()
	if script.Log == "" {
		script.Log = filepath.Join(dir, "acp.log")
	}
	raw, err := json.Marshal(script)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "script.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return os.Args[0], []string{"-test.run=^TestHelperACPAgent$", "--", Sentinel, path}
}

// Received is one message the fake agent got, as logged.
type Received struct {
	PID    int             `json:"pid"`
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  json.RawMessage `json:"error,omitempty"`
}

// ReadLog returns what the agents of a script received, in order.
func ReadLog(t testing.TB, path string) []Received {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var out []Received
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var r Received
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("acp log line %q: %v", line, err)
		}
		out = append(out, r)
	}
	return out
}

// Methods filters a log down to the messages of one method.
func Methods(log []Received, method string) []Received {
	var out []Received
	for _, r := range log {
		if r.Method == method {
			out = append(out, r)
		}
	}
	return out
}

// Main runs the fake agent when args name it and never returns in that case.
// It returns false when the arguments do not select the helper, so the
// calling test can return.
func Main(args []string) bool {
	if len(args) < 2 || args[0] != Sentinel {
		return false
	}
	raw, err := os.ReadFile(args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "acptest: read script:", err)
		os.Exit(2)
	}
	var s Script
	if err := json.Unmarshal(raw, &s); err != nil {
		fmt.Fprintln(os.Stderr, "acptest: parse script:", err)
		os.Exit(2)
	}
	a := &agent{script: s, out: bufio.NewWriter(os.Stdout), sessions: map[string]*session{}, waiting: map[string]chan json.RawMessage{}}
	if s.Banner != "" {
		_, _ = os.Stdout.WriteString(s.Banner + "\n")
	}
	a.run()
	os.Exit(0)
	return true
}

type session struct {
	id      string
	cwd     string
	mode    string
	model   string
	thought string
	// cancel is closed by session/cancel; each prompt gets a channel of its
	// own (guarded by the agent's mutex), so a cancel ends that prompt only.
	cancel chan struct{}
}

type agent struct {
	script   Script
	mu       sync.Mutex
	out      *bufio.Writer
	sessions map[string]*session
	authed   string
	turn     int
	nextID   int
	// atBarrier counts the prompts of this process that reached a barrier.
	atBarrier int
	// waiting holds the requests this agent sent the client, by id.
	waiting map[string]chan json.RawMessage
}

type message struct {
	ID     json.RawMessage `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  json.RawMessage `json:"error,omitempty"`
}

func (a *agent) log(m message) {
	if a.script.Log == "" {
		return
	}
	rec := Received{PID: os.Getpid(), ID: m.ID, Method: m.Method, Params: m.Params, Result: m.Result, Error: m.Error}
	b, _ := json.Marshal(rec)
	f, err := os.OpenFile(a.script.Log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	_, _ = f.Write(append(b, '\n'))
	_ = f.Close()
}

func (a *agent) send(v interface{}) {
	b, _ := json.Marshal(v)
	a.mu.Lock()
	defer a.mu.Unlock()
	_, _ = a.out.Write(append(b, '\n'))
	_ = a.out.Flush()
}

func (a *agent) reply(id json.RawMessage, result interface{}) {
	a.send(map[string]interface{}{"jsonrpc": "2.0", "id": id, "result": result})
}

func (a *agent) fail(id json.RawMessage, code int, msg string) {
	a.send(map[string]interface{}{"jsonrpc": "2.0", "id": id, "error": map[string]interface{}{"code": code, "message": msg}})
}

func (a *agent) update(sessionID string, u map[string]interface{}) {
	a.send(map[string]interface{}{"jsonrpc": "2.0", "method": "session/update", "params": map[string]interface{}{"sessionId": sessionID, "update": u}})
}

// request sends a request to the client and waits for its answer.
func (a *agent) request(method string, params interface{}) json.RawMessage {
	a.mu.Lock()
	a.nextID++
	id := fmt.Sprintf("srv-%d", a.nextID)
	ch := make(chan json.RawMessage, 1)
	a.waiting[id] = ch
	a.mu.Unlock()
	a.send(map[string]interface{}{"jsonrpc": "2.0", "id": id, "method": method, "params": params})
	return <-ch
}

func (a *agent) run() {
	in := bufio.NewScanner(os.Stdin)
	in.Buffer(make([]byte, 1<<20), 64<<20)
	for in.Scan() {
		line := strings.TrimSpace(in.Text())
		if line == "" {
			continue
		}
		var m message
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			fmt.Fprintln(os.Stderr, "acptest: bad line:", line)
			continue
		}
		a.log(m)
		if m.Method == "" {
			// A response to one of the agent's own requests.
			var id string
			_ = json.Unmarshal(m.ID, &id)
			a.mu.Lock()
			ch := a.waiting[id]
			delete(a.waiting, id)
			a.mu.Unlock()
			if ch != nil {
				body := m.Result
				if len(m.Error) > 0 {
					body, _ = json.Marshal(map[string]json.RawMessage{"error": m.Error})
				}
				ch <- body
			}
			continue
		}
		a.handle(m)
	}
	// Stdin closed: the client is gone, and a prompt still waiting on it
	// (a permission answer, a cancel) would wait forever.
}

func (a *agent) handle(m message) {
	switch m.Method {
	case "initialize":
		if a.script.StartDelayMS > 0 {
			time.Sleep(time.Duration(a.script.StartDelayMS) * time.Millisecond)
		}
		methods := make([]map[string]string, 0, len(a.script.AuthMethods))
		for _, id := range a.script.AuthMethods {
			methods = append(methods, map[string]string{"id": id, "name": id})
		}
		caps := map[string]interface{}{"promptCapabilities": map[string]bool{"image": a.script.Image}}
		if a.script.Close {
			caps["sessionCapabilities"] = map[string]interface{}{"close": map[string]interface{}{}}
		}
		name := a.script.Name
		if name == "" {
			name = "acptest"
		}
		a.reply(m.ID, map[string]interface{}{
			"protocolVersion":   1,
			"agentCapabilities": caps,
			"agentInfo":         map[string]string{"name": name, "version": "0"},
			"authMethods":       methods,
		})
	case "authenticate":
		var p struct {
			MethodID string `json:"methodId"`
		}
		_ = json.Unmarshal(m.Params, &p)
		a.authed = p.MethodID
		a.reply(m.ID, map[string]interface{}{})
	case "session/new":
		if a.script.RequireAuth != "" && a.authed != a.script.RequireAuth {
			a.fail(m.ID, -32000, "Authentication required")
			if a.script.ExitAfterNewError {
				os.Exit(1)
			}
			return
		}
		var np struct {
			CWD string `json:"cwd"`
		}
		_ = json.Unmarshal(m.Params, &np)
		a.mu.Lock()
		a.nextID++
		s := &session{id: fmt.Sprintf("acp-sess-%d", a.nextID), cwd: np.CWD}
		if len(a.script.Modes) > 0 {
			s.mode = a.script.Modes[0]
		}
		if len(a.script.Models) > 0 {
			s.model = a.script.Models[0]
		}
		if len(a.script.ThoughtLevels) > 0 {
			s.thought = a.script.ThoughtLevels[0]
		}
		a.sessions[s.id] = s
		a.mu.Unlock()
		a.reply(m.ID, a.sessionState(s))
		if a.script.ModeAfterNew != "" {
			s.mode = a.script.ModeAfterNew
			a.update(s.id, map[string]interface{}{"sessionUpdate": "current_mode_update", "currentModeId": a.script.ModeAfterNew})
		}
		if a.script.DeafAfterNew {
			select {}
		}
	case "session/close":
		var p struct {
			SessionID string `json:"sessionId"`
		}
		_ = json.Unmarshal(m.Params, &p)
		a.mu.Lock()
		delete(a.sessions, p.SessionID)
		a.mu.Unlock()
		a.reply(m.ID, map[string]interface{}{})
	case "session/set_mode":
		var p struct {
			SessionID string `json:"sessionId"`
			ModeID    string `json:"modeId"`
		}
		_ = json.Unmarshal(m.Params, &p)
		s := a.session(p.SessionID)
		if s == nil || !contains(a.script.Modes, p.ModeID) {
			a.fail(m.ID, -32602, "unknown mode")
			return
		}
		s.mode = p.ModeID
		a.reply(m.ID, map[string]interface{}{})
	case "session/set_model":
		var p struct {
			SessionID string `json:"sessionId"`
			ModelID   string `json:"modelId"`
		}
		_ = json.Unmarshal(m.Params, &p)
		s := a.session(p.SessionID)
		if s == nil || !a.script.LegacyModels || !contains(a.script.Models, p.ModelID) {
			a.fail(m.ID, -32602, "unknown model")
			return
		}
		s.model = p.ModelID
		a.reply(m.ID, map[string]interface{}{})
	case "session/set_config_option":
		var p struct {
			SessionID string `json:"sessionId"`
			ConfigID  string `json:"configId"`
			Value     string `json:"value"`
		}
		_ = json.Unmarshal(m.Params, &p)
		s := a.session(p.SessionID)
		ok := s != nil
		if ok {
			switch p.ConfigID {
			case "mode":
				ok = !a.script.LegacyModes && contains(a.script.Modes, p.Value)
				if ok {
					s.mode = p.Value
				}
			case "model":
				ok = !a.script.LegacyModels && contains(a.script.Models, p.Value)
				if ok {
					s.model = p.Value
				}
			case "thought":
				ok = contains(a.script.ThoughtLevels, p.Value) && p.Value != a.script.RefuseThought
				if ok {
					s.thought = p.Value
				}
			default:
				ok = false
			}
		}
		if !ok {
			a.fail(m.ID, -32602, "unknown config option value")
			return
		}
		a.reply(m.ID, map[string]interface{}{"configOptions": a.configOptions(s)})
	case "session/cancel":
		var p struct {
			SessionID string `json:"sessionId"`
		}
		_ = json.Unmarshal(m.Params, &p)
		a.mu.Lock()
		if s := a.sessions[p.SessionID]; s != nil && s.cancel != nil {
			close(s.cancel)
			s.cancel = nil
		}
		a.mu.Unlock()
	case "session/prompt":
		// The turn and the cancel channel are taken here, in arrival order,
		// not on the prompt's goroutine.
		var p struct {
			SessionID string `json:"sessionId"`
		}
		_ = json.Unmarshal(m.Params, &p)
		t := a.nextTurn()
		cancel := make(chan struct{})
		a.mu.Lock()
		if s := a.sessions[p.SessionID]; s != nil {
			s.cancel = cancel
		}
		a.mu.Unlock()
		go a.prompt(m, t, cancel)
	default:
		if len(m.ID) > 0 {
			a.fail(m.ID, -32601, "method not found")
		}
	}
}

func (a *agent) session(id string) *session {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.sessions[id]
}

func (a *agent) sessionState(s *session) map[string]interface{} {
	out := map[string]interface{}{"sessionId": s.id}
	if len(a.script.Modes) > 0 {
		modes := make([]map[string]string, 0, len(a.script.Modes))
		for _, m := range a.script.Modes {
			modes = append(modes, map[string]string{"id": m, "name": m})
		}
		out["modes"] = map[string]interface{}{"currentModeId": s.mode, "availableModes": modes}
	}
	if a.script.LegacyModels && len(a.script.Models) > 0 {
		models := make([]map[string]string, 0, len(a.script.Models))
		for _, m := range a.script.Models {
			models = append(models, map[string]string{"modelId": m, "name": m})
		}
		out["models"] = map[string]interface{}{"currentModelId": s.model, "availableModels": models}
	}
	if opts := a.configOptions(s); len(opts) > 0 {
		out["configOptions"] = opts
	}
	return out
}

func (a *agent) configOptions(s *session) []map[string]interface{} {
	var out []map[string]interface{}
	selectOf := func(id, category, current string, values []string) map[string]interface{} {
		opts := make([]map[string]string, 0, len(values))
		for _, v := range values {
			opts = append(opts, map[string]string{"value": v, "name": v})
		}
		return map[string]interface{}{"id": id, "name": id, "category": category, "type": "select", "currentValue": current, "options": opts}
	}
	if len(a.script.Modes) > 0 && !a.script.LegacyModes {
		out = append(out, selectOf("mode", "mode", s.mode, a.script.Modes))
	}
	if !a.script.LegacyModels && len(a.script.Models) > 0 {
		out = append(out, selectOf("model", "model", s.model, a.script.Models))
	}
	if len(a.script.ThoughtLevels) > 0 {
		out = append(out, selectOf("thought", "thought_level", s.thought, a.script.ThoughtLevels))
	}
	return out
}

// nextTurn picks the turn of the prompt just logged: the prompts the log
// holds count across every process of the script, so an agent started again
// after one exited goes on with the next turn.
func (a *agent) nextTurn() Turn {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.script.Turns) == 0 {
		return Turn{Echo: true}
	}
	i := a.turn
	if a.script.Log != "" {
		if raw, err := os.ReadFile(a.script.Log); err == nil {
			i = strings.Count(string(raw), `"method":"session/prompt"`) - 1
		}
	} else {
		a.turn++
	}
	if i < 0 {
		i = 0
	}
	if i >= len(a.script.Turns) {
		i = len(a.script.Turns) - 1
	}
	return a.script.Turns[i]
}

func (a *agent) prompt(m message, t Turn, cancelled <-chan struct{}) {
	var p struct {
		SessionID string `json:"sessionId"`
		Prompt    []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"prompt"`
	}
	_ = json.Unmarshal(m.Params, &p)
	s := a.session(p.SessionID)
	if s == nil {
		a.fail(m.ID, -32602, "unknown session")
		return
	}
	if t.ExitFirst {
		os.Exit(3)
	}
	if t.DelayMS > 0 {
		time.Sleep(time.Duration(t.DelayMS) * time.Millisecond)
	}
	if t.Status {
		a.update(s.id, map[string]interface{}{"sessionUpdate": "available_commands_update", "availableCommands": []interface{}{}})
		a.update(s.id, map[string]interface{}{"sessionUpdate": "usage_update", "used": 100, "size": 1000})
	}
	if t.Error != "" && !t.ErrorAfterTools {
		a.fail(m.ID, -32603, t.Error)
		return
	}
	for _, th := range t.Thoughts {
		a.update(s.id, map[string]interface{}{"sessionUpdate": "agent_thought_chunk", "content": map[string]string{"type": "text", "text": th}})
	}
	for _, th := range t.CoddyReasoning {
		a.update(s.id, map[string]interface{}{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "reasoning", "text": th}})
	}
	for _, tool := range t.Tools {
		a.update(s.id, map[string]interface{}{"sessionUpdate": "tool_call", "toolCallId": tool.ID, "title": tool.Title, "kind": "execute", "status": "pending"})
		if tool.Path != "" {
			file := filepath.Join(s.cwd, tool.Path)
			a.update(s.id, map[string]interface{}{"sessionUpdate": "tool_call_update", "toolCallId": tool.ID, "status": "in_progress",
				"locations": []map[string]string{{"path": file}}, "rawInput": map[string]string{"filePath": file}})
		}
		status := "completed"
		if tool.Fail {
			status = "failed"
		}
		a.update(s.id, map[string]interface{}{"sessionUpdate": "tool_call_update", "toolCallId": tool.ID, "status": status})
	}
	if t.Error != "" {
		a.fail(m.ID, -32603, t.Error)
		if t.ExitAfterReply {
			os.Exit(1)
		}
		return
	}
	var lines []string
	if t.Permission != nil {
		answer := a.request("session/request_permission", map[string]interface{}{
			"sessionId": s.id,
			"toolCall": map[string]interface{}{
				"toolCallId": "perm-1", "title": t.Permission.Title, "kind": t.Permission.Kind, "status": "pending",
				"content": []map[string]interface{}{{"type": "content", "content": map[string]string{"type": "text", "text": t.Permission.Content}}},
			},
			"options": t.Permission.Options,
		})
		var res struct {
			Outcome struct {
				Outcome  string `json:"outcome"`
				OptionID string `json:"optionId"`
			} `json:"outcome"`
		}
		_ = json.Unmarshal(answer, &res)
		if res.Outcome.Outcome == "selected" {
			lines = append(lines, "permission: "+res.Outcome.OptionID)
		} else {
			lines = append(lines, "permission: "+res.Outcome.Outcome)
		}
	}
	if t.ClientRequest != "" {
		answer := a.request(t.ClientRequest, map[string]interface{}{"sessionId": s.id})
		var res struct {
			Error *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(answer, &res)
		if res.Error != nil {
			lines = append(lines, "client: "+res.Error.Message)
		} else {
			lines = append(lines, "client: ok")
		}
	}
	if t.Echo {
		var parts []string
		for _, b := range p.Prompt {
			if b.Type == "text" {
				parts = append(parts, b.Text)
			}
		}
		lines = append(lines, "echo: "+strings.Join(parts, " | "))
	}
	for _, name := range t.Env {
		lines = append(lines, name+"="+os.Getenv(name))
	}
	if t.Barrier > 0 {
		lines = append(lines, "barrier: "+a.awaitBarrier(t.Barrier, t.BarrierWait))
	}
	text := append([]string{}, t.Text...)
	if len(lines) > 0 && !t.Quiet {
		text = append(text, strings.Join(lines, "\n"))
	}
	for i := 0; i < t.Chunks; i++ {
		text = append(text, "x")
	}
	for _, chunk := range text {
		a.update(s.id, map[string]interface{}{"sessionUpdate": "agent_message_chunk", "content": map[string]string{"type": "text", "text": chunk}})
	}
	if t.Exit {
		// send flushed every line under the lock already.
		os.Exit(3)
	}
	stop := t.StopReason
	if t.IgnoreCancel {
		select {}
	}
	if t.WaitCancel {
		<-cancelled
		stop = "cancelled"
	}
	if stop == "" {
		stop = "end_turn"
	}
	result := map[string]interface{}{"stopReason": stop}
	if len(t.Usage) > 0 {
		result["usage"] = t.Usage
	}
	a.reply(m.ID, result)
	if t.ExitAfterReply {
		os.Exit(0)
	}
}

// awaitBarrier waits until n prompts of the process have reached a barrier,
// or the wait (10 s when 0) is over. Arrivals are counted, not the prompts
// still waiting: the last one to arrive passes at once and must not leave
// the others short. Prompts a client sends one after another meet only once
// the first ones gave up.
func (a *agent) awaitBarrier(n, waitMS int) string {
	wait := time.Duration(waitMS) * time.Millisecond
	if wait <= 0 {
		wait = 10 * time.Second
	}
	deadline := time.Now().Add(wait)
	a.mu.Lock()
	a.atBarrier++
	a.mu.Unlock()
	for {
		a.mu.Lock()
		met := a.atBarrier >= n
		a.mu.Unlock()
		if met {
			return strconv.Itoa(n)
		}
		if time.Now().After(deadline) {
			return "timeout"
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
