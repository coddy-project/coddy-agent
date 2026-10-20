package llm

import "context"

// Conversation describes the session a provider call belongs to. The agent
// loop puts it on the context of its own Stream call only (WithConversation);
// helpers that call a provider under the turn's context - compaction, titles,
// the goal judge - do not inherit it. Providers that keep state per
// conversation (the acp provider binds a Coddy session to one ACP session)
// read it; the others ignore it.
type Conversation struct {
	// SessionID is the Coddy session id.
	SessionID string
	// CWD is the session's workspace.
	CWD string
	// Mode is the session mode: agent, plan or ask.
	Mode string
	// PermissionMode is the session's permission mode (config.PermMode*).
	PermissionMode string
	// Restricted marks a turn the surface restricted to some of Coddy's
	// tools (a messenger sender who is not an admin): a provider whose
	// agent runs tools of its own refuses it, since the restriction does
	// not reach those.
	Restricted bool
	// AskPermission asks the person who holds the session, through whatever
	// surface answers the session's own permission prompts. Nil when nobody
	// can be asked.
	AskPermission func(ctx context.Context, req PermissionRequest) (PermissionAnswer, error)
}

// PermissionRequest is a permission prompt a provider forwards to the person:
// the acp provider's agent asking before it runs one of its own tools.
type PermissionRequest struct {
	// ToolCallID identifies the agent's tool call.
	ToolCallID string
	Title      string
	// Kind is the ACP tool kind (read, edit, execute, ...).
	Kind string
	// Body is what the prompt shows: the tool call's content as text.
	Body    string
	Options []PermissionOption
}

// PermissionOption is one answer the prompt offers, as the agent named it.
type PermissionOption struct {
	ID   string
	Name string
	// Kind is allow_once, allow_always, reject_once or reject_always.
	Kind string
}

// PermissionAnswer is the person's answer: the option chosen, or none when
// the prompt was dismissed or nobody answered.
type PermissionAnswer struct {
	OptionID  string
	Cancelled bool
}

type conversationKey struct{}

// WithConversation returns ctx carrying conv.
func WithConversation(ctx context.Context, conv Conversation) context.Context {
	return context.WithValue(ctx, conversationKey{}, &conv)
}

// ConversationFrom returns the conversation ctx carries, or nil.
func ConversationFrom(ctx context.Context) *Conversation {
	conv, _ := ctx.Value(conversationKey{}).(*Conversation)
	return conv
}
