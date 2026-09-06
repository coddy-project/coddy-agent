package hooks

import "encoding/json"

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
	if err := json.Unmarshal([]byte(argsJSON), &input); err != nil || input == nil {
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
		p["subagent"] = map[string]interface{}{
			"name":              s.Subagent.Name,
			"parent_session_id": s.Subagent.ParentSessionID,
			"depth":             s.Subagent.Depth,
		}
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
