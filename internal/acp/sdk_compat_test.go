package acp_test

// The wire shapes this agent writes, held to the ACP SDK's own types.
//
// Coddy's ACP server is hand-written - it speaks the protocol with the
// structures in this package - while the ACP client half of the same binary
// talks through github.com/coder/acp-go-sdk. Nothing ties the two halves
// together at compile time, so a field the server writes under a name a real
// SDK cannot read is a break no other test would catch: a client built on that
// SDK is exactly who these lines are for. Here every standard line the agent
// sends is decoded with the SDK's types, which is what makes the shapes held by
// something other than our own reader.

import (
	"encoding/json"
	"testing"

	acpsdk "github.com/coder/acp-go-sdk"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// decodeWithSDK marshals one of our lines and reads it back with the SDK type
// out must point at.
func decodeWithSDK(t *testing.T, line any, out any) {
	t.Helper()
	raw, err := json.Marshal(line)
	if err != nil {
		t.Fatalf("marshal %T: %v", line, err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("the ACP SDK cannot read our %T: %v\n%s", line, err, raw)
	}
}

// The initialize answer advertises session/close, and the SDK reads that
// capability as the field it is defined as.
func TestInitializeAnswerIsReadByTheSDK(t *testing.T) {
	var got acpsdk.InitializeResponse
	decodeWithSDK(t, acp.InitializeResult{
		ProtocolVersion: acp.ProtocolVersion,
		AgentCapabilities: acp.AgentCapabilities{
			LoadSession:         true,
			SessionCapabilities: acp.FullSessionCaps(),
			PromptCapabilities:  &acp.PromptCapabilities{EmbeddedContext: true},
			MCPCapabilities:     &acp.MCPCapabilities{HTTP: true},
		},
		AgentInfo:   acp.ImplementationInfo{Name: acp.AgentName, Title: acp.AgentTitle, Version: "1.0.0"},
		AuthMethods: []acp.AuthMethod{},
	}, &got)

	if got.ProtocolVersion != acp.ProtocolVersion {
		t.Errorf("protocolVersion = %d, want %d", got.ProtocolVersion, acp.ProtocolVersion)
	}
	if !got.AgentCapabilities.LoadSession {
		t.Error("the SDK read no loadSession")
	}
	if got.AgentCapabilities.SessionCapabilities.Close == nil {
		t.Error("the SDK read no sessionCapabilities.close")
	}
	if got.AgentCapabilities.SessionCapabilities.List == nil {
		t.Error("the SDK read no sessionCapabilities.list")
	}
	if len(got.AuthMethods) != 0 {
		t.Errorf("authMethods = %d entries, want none", len(got.AuthMethods))
	}
}

// A sign-in method we might one day advertise is the tagged object the spec
// defines, and the SDK decodes it without an error.
//
// The variant it lands in is not asserted: the SDK at this version has no
// explicit case for the agent-handled type and matches its terminal variant on
// type+id+name instead, so an agent method written the way the spec asks reads
// back as a terminal one. Coddy advertises no method today, so this is a note
// for whoever gives it one - and the reason the object's own fields are held
// here rather than the SDK's reading of them.
func TestAuthMethodIsTheTaggedObjectOfTheSpec(t *testing.T) {
	raw, err := json.Marshal(acp.AuthMethod{Type: "agent", ID: "login", Name: "Log in"})
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"type", "id", "name"} {
		if _, ok := fields[key]; !ok {
			t.Errorf("auth method %s carries no %q", raw, key)
		}
	}
	var got acpsdk.AuthMethod
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("the ACP SDK cannot read our auth method: %v\n%s", err, raw)
	}
}

// Closing a session is the request the SDK names.
func TestSessionCloseRequestIsReadByTheSDK(t *testing.T) {
	var got acpsdk.CloseSessionRequest
	decodeWithSDK(t, acp.SessionCloseParams{SessionID: "sess_1"}, &got)
	if string(got.SessionId) != "sess_1" {
		t.Errorf("sessionId = %q, want sess_1", got.SessionId)
	}
}

// A message chunk carries its messageId under the name the SDK reads.
func TestMessageChunkIsReadByTheSDK(t *testing.T) {
	want := acp.MessageID(3)
	var agent acpsdk.SessionUpdate
	decodeWithSDK(t, acp.MessageChunkUpdate{
		SessionUpdate: acp.UpdateTypeAgentMessageChunk,
		Content:       acp.ContentBlock{Type: acp.ContentTypeText, Text: "hi"},
		MessageID:     want,
	}, &agent)
	if agent.AgentMessageChunk == nil {
		t.Fatalf("the SDK read no agent_message_chunk: %+v", agent)
	}
	if id := agent.AgentMessageChunk.MessageId; id == nil || *id != want {
		t.Errorf("messageId = %v, want %q", agent.AgentMessageChunk.MessageId, want)
	}

	var user acpsdk.SessionUpdate
	decodeWithSDK(t, acp.MessageChunkUpdate{
		SessionUpdate: acp.UpdateTypeUserMessageChunk,
		Content:       acp.ContentBlock{Type: acp.ContentTypeText, Text: "hi"},
		MessageID:     want,
	}, &user)
	if user.UserMessageChunk == nil {
		t.Fatalf("the SDK read no user_message_chunk: %+v", user)
	}
	if id := user.UserMessageChunk.MessageId; id == nil || *id != want {
		t.Errorf("messageId = %v, want %q", user.UserMessageChunk.MessageId, want)
	}
}

// A tool call is read as the SDK's tool call, and the programmatic name we send
// beside the title - a field the SDK at this version does not model - costs the
// SDK nothing: it decodes the line and keeps what it knows.
func TestToolCallIsReadByTheSDK(t *testing.T) {
	var got acpsdk.SessionUpdate
	decodeWithSDK(t, acp.ToolCallUpdate{
		SessionUpdate: acp.UpdateTypeToolCall,
		ToolCallID:    "call_1",
		Title:         "Run: go build ./...",
		Name:          "run_command",
		Kind:          "execute",
		Status:        "pending",
	}, &got)
	call := got.ToolCall
	if call == nil {
		t.Fatalf("the SDK read no tool call: %+v", got)
	}
	if string(call.ToolCallId) != "call_1" {
		t.Errorf("toolCallId = %q, want call_1", call.ToolCallId)
	}
	if call.Title != "Run: go build ./..." {
		t.Errorf("title = %q", call.Title)
	}
	if string(call.Kind) != "execute" {
		t.Errorf("kind = %q, want execute", call.Kind)
	}
}

// The permission request's tool call is read by the SDK too, name included.
func TestPermissionRequestIsReadByTheSDK(t *testing.T) {
	var got acpsdk.RequestPermissionRequest
	decodeWithSDK(t, acp.PermissionRequestParams{
		SessionID: "sess_1",
		ToolCall: acp.PermissionToolCall{
			ToolCallID: "call_2",
			Title:      "Run: go build ./...",
			Name:       "run_command",
			Kind:       "execute",
			Status:     "pending",
		},
		Options: []acp.PermissionOption{{OptionID: "allow", Name: "Allow", Kind: "allow_once"}},
	}, &got)
	if string(got.SessionId) != "sess_1" {
		t.Errorf("sessionId = %q", got.SessionId)
	}
	if got.ToolCall.Title == nil || *got.ToolCall.Title != "Run: go build ./..." {
		t.Errorf("toolCall.title = %v", got.ToolCall.Title)
	}
	if len(got.Options) != 1 || got.Options[0].OptionId != "allow" {
		t.Errorf("options = %+v", got.Options)
	}
}

// A rename and the context-window figure are the SDK's own updates.
func TestSessionInfoAndUsageUpdatesAreReadByTheSDK(t *testing.T) {
	title := "Ship the release"
	var named acpsdk.SessionUpdate
	decodeWithSDK(t, acp.SessionInfoUpdate{
		SessionUpdate: acp.UpdateTypeSessionInfoUpdate,
		Title:         &title,
	}, &named)
	if named.SessionInfoUpdate == nil {
		t.Fatalf("the SDK read no session_info_update: %+v", named)
	}
	if named.SessionInfoUpdate.Title == nil || *named.SessionInfoUpdate.Title != title {
		t.Errorf("title = %v, want %q", named.SessionInfoUpdate.Title, title)
	}

	var usage acpsdk.SessionUpdate
	decodeWithSDK(t, acp.UsageUpdate{
		SessionUpdate: acp.UpdateTypeUsage,
		Used:          1200,
		Size:          128000,
	}, &usage)
	if usage.UsageUpdate == nil {
		t.Fatalf("the SDK read no usage_update: %+v", usage)
	}
	if usage.UsageUpdate.Used != 1200 || usage.UsageUpdate.Size != 128000 {
		t.Errorf("usage = %+v", *usage.UsageUpdate)
	}
}
