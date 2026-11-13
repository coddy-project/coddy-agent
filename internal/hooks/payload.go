package hooks

import (
	"encoding/json"
	"strings"
)

// Session is what every hook learns about the session it runs for; the
// runner puts these fields at the top of every payload.
type Session struct {
	ID             string
	CWD            string
	TranscriptPath string
	PermissionMode string
	Mode           string
	Model          string
	Turn           int
	// Subagent is set inside a child session.
	Subagent *Subagent
}

// Subagent identifies a child session in the payload.
type Subagent struct {
	Name            string
	ParentSessionID string
	Depth           int
	// Kind marks a child the runtime started on its own behalf ("memory");
	// empty for a spawn_agent child. A hook that treats memory writes
	// differently keys on it.
	Kind string
}

// Event is one occurrence the runner dispatches: its name, the subject the
// matchers are compared with (the tool name, the source, the trigger, ...)
// and the event-specific payload fields.
type Event struct {
	Name    string
	Subject string
	Fields  map[string]interface{}
}

// ToolEvent builds a tool event (PreToolUse, PostToolUse, PostToolUseFailure)
// with the fields every tool payload carries.
func ToolEvent(name, tool string, input map[string]interface{}, callID string) Event {
	return Event{
		Name:    name,
		Subject: tool,
		Fields: map[string]interface{}{
			"tool_name":   tool,
			"tool_input":  input,
			"tool_use_id": callID,
		},
	}
}

// ToolInput decodes a tool call's JSON arguments for the payload. Arguments
// that are not a JSON object travel under a "raw" key rather than being lost.
func ToolInput(argsJSON string) map[string]interface{} {
	var input map[string]interface{}
	// Number literals stay verbatim, so a hook reads the integer the model
	// wrote and not a float64 rounding of it.
	dec := json.NewDecoder(strings.NewReader(argsJSON))
	dec.UseNumber()
	if err := dec.Decode(&input); err != nil || input == nil {
		return map[string]interface{}{"raw": argsJSON}
	}
	return input
}

// payload assembles the document a hook reads on stdin: the session fields
// first, then the event fields (which win on a name clash).
func (s Session) payload(event string, fields map[string]interface{}) map[string]interface{} {
	p := map[string]interface{}{
		"session_id":      s.ID,
		"hook_event_name": event,
		"cwd":             s.CWD,
		"transcript_path": s.TranscriptPath,
		"permission_mode": s.PermissionMode,
		"mode":            s.Mode,
		"model":           s.Model,
		"turn":            s.Turn,
	}
	if s.Subagent != nil {
		sub := map[string]interface{}{
			"name":              s.Subagent.Name,
			"parent_session_id": s.Subagent.ParentSessionID,
			"depth":             s.Subagent.Depth,
		}
		if s.Subagent.Kind != "" {
			sub["kind"] = s.Subagent.Kind
		}
		p["subagent"] = sub
	}
	for k, v := range fields {
		p[k] = v
	}
	return p
}

// PromptEvent builds the UserPromptSubmit event for a prompt text.
func PromptEvent(prompt string) Event {
	return Event{Name: EventUserPromptSubmit, Fields: map[string]interface{}{"prompt": prompt}}
}

// StopEvent builds the Stop event. active reports that a stop hook already
// sent the agent back to work in this turn; last is the assistant's final
// text.
func StopEvent(active bool, last string) Event {
	return Event{Name: EventStop, Fields: map[string]interface{}{
		"stop_hook_active":       active,
		"last_assistant_message": last,
	}}
}

// SessionStartEvent builds the SessionStart event; source is startup or
// resume and is what the matcher is compared with.
func SessionStartEvent(source, model string) Event {
	return Event{Name: EventSessionStart, Subject: source, Fields: map[string]interface{}{
		"source": source,
		"model":  model,
	}}
}

// CompactEvent builds PreCompact or PostCompact; trigger is manual or auto
// and is what the matcher is compared with. PreCompact carries the operator's
// custom_instructions, PostCompact the summary.
func CompactEvent(name, trigger string, fields map[string]interface{}) Event {
	ev := Event{Name: name, Subject: trigger, Fields: map[string]interface{}{"trigger": trigger}}
	for k, v := range fields {
		ev.Fields[k] = v
	}
	return ev
}

// SubagentStartEvent builds the SubagentStart event fired in the parent
// before a child's turn; the matcher is compared with the agent name.
func SubagentStartEvent(name, childSessionID, prompt string, background bool) Event {
	return Event{Name: EventSubagentStart, Subject: name, Fields: map[string]interface{}{
		"agent_name":       name,
		"agent_session_id": childSessionID,
		"prompt":           prompt,
		"background":       background,
	}}
}

// SubagentStopEvent builds the SubagentStop event fired in the parent after a
// child's turn ended; the matcher is compared with the agent name.
func SubagentStopEvent(name, childSessionID, taskID, status, report string, turns int) Event {
	return Event{Name: EventSubagentStop, Subject: name, Fields: map[string]interface{}{
		"agent_name":       name,
		"agent_session_id": childSessionID,
		"task_id":          taskID,
		"status":           status,
		"report":           report,
		"turns":            turns,
	}}
}

// NotificationEvent builds a Notification event; kind (permission_prompt) is
// what the matcher is compared with.
func NotificationEvent(kind, message string, fields map[string]interface{}) Event {
	ev := Event{Name: EventNotification, Subject: kind, Fields: map[string]interface{}{
		"notification_type": kind,
		"message":           message,
	}}
	for k, v := range fields {
		ev.Fields[k] = v
	}
	return ev
}

// FirstPromptEvent builds the FirstPrompt event for the first prompt text of
// a session - the same payload as UserPromptSubmit, one turn earlier in life.
func FirstPromptEvent(prompt string) Event {
	return Event{Name: EventFirstPrompt, Fields: map[string]interface{}{"prompt": prompt}}
}

// QuestionEvent builds the Question event: the question tool is about to ask
// its questions. The payload carries the questions and their options verbatim
// (the tool's own arguments), so a notification hook can show exactly what the
// user is about to be asked. The subject is the tool name, so a matcher can
// name `question`.
func QuestionEvent(callID string, input map[string]interface{}) Event {
	fields := map[string]interface{}{
		"tool_name":   "question",
		"tool_input":  input,
		"tool_use_id": callID,
	}
	// Lifted for a notification hook's convenience: the questions and their
	// options are what the user is about to be asked.
	if q, ok := input["questions"]; ok {
		fields["questions"] = q
	}
	return Event{Name: EventQuestion, Subject: "question", Fields: fields}
}

// TurnEndEvent builds the TurnEnd event: the turn finished and the agent is
// waiting for the user's next message. last is the assistant's final text.
func TurnEndEvent(stopReason, last string) Event {
	return Event{Name: EventTurnEnd, Fields: map[string]interface{}{
		"stop_reason":            stopReason,
		"last_assistant_message": last,
	}}
}
