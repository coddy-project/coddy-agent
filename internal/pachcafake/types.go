package pachcafake

import (
	"encoding/json"
	"time"
)

// Button is one inline button of a message, as the API carries it: a text
// plus exactly one of a link (url) or a callback payload (data).
type Button struct {
	Text string `json:"text"`
	URL  string `json:"url,omitempty"`
	Data string `json:"data,omitempty"`
}

// MessageThread is the thread opened under a message: the thread id and the
// thread's own chat. It is null on a message nobody replied to in a thread.
type MessageThread struct {
	ID     int64 `json:"id"`
	ChatID int64 `json:"chat_id"`
}

// Message mirrors the Message schema field for field, nullable fields as
// pointers or raw JSON so they marshal to null the way Pachca sends them.
type Message struct {
	ID               int64             `json:"id"`
	EntityType       string            `json:"entity_type"`
	EntityID         int64             `json:"entity_id"`
	ChatID           int64             `json:"chat_id"`
	RootChatID       int64             `json:"root_chat_id"`
	Content          string            `json:"content"`
	UserID           int64             `json:"user_id"`
	CreatedAt        string            `json:"created_at"`
	URL              string            `json:"url"`
	Files            []json.RawMessage `json:"files"`
	VoiceContent     json.RawMessage   `json:"voice_content"`
	Buttons          [][]Button        `json:"buttons"`
	Thread           *MessageThread    `json:"thread"`
	Forwarding       json.RawMessage   `json:"forwarding"`
	ParentMessageID  *int64            `json:"parent_message_id"`
	DisplayAvatarURL *string           `json:"display_avatar_url"`
	DisplayName      *string           `json:"display_name"`
	ChangedAt        *string           `json:"changed_at"`
	DeletedAt        *string           `json:"deleted_at"`
}

// clone returns a deep copy, so a caller holding it cannot reach the store.
func (m Message) clone() Message {
	out := m
	out.Files = make([]json.RawMessage, 0, len(m.Files))
	for _, f := range m.Files {
		out.Files = append(out.Files, append(json.RawMessage(nil), f...))
	}
	out.Buttons = cloneButtons(m.Buttons)
	if m.Thread != nil {
		t := *m.Thread
		out.Thread = &t
	}
	out.ParentMessageID = cloneInt(m.ParentMessageID)
	out.DisplayAvatarURL = cloneString(m.DisplayAvatarURL)
	out.DisplayName = cloneString(m.DisplayName)
	out.ChangedAt = cloneString(m.ChangedAt)
	out.DeletedAt = cloneString(m.DeletedAt)
	return out
}

func cloneButtons(rows [][]Button) [][]Button {
	if rows == nil {
		return nil
	}
	out := make([][]Button, len(rows))
	for i, row := range rows {
		out[i] = append([]Button(nil), row...)
	}
	return out
}

func cloneInt(p *int64) *int64 {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func cloneString(p *string) *string {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

// WebhookEvent is one entry of the bot's event log, the WebhookEvent schema.
type WebhookEvent struct {
	ID        string          `json:"id"`
	EventType string          `json:"event_type"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt string          `json:"created_at"`
}

// webhookThread is WebhookMessageThread: the message a thread hangs under and
// that message's chat.
type webhookThread struct {
	MessageID     *int64 `json:"message_id"`
	MessageChatID *int64 `json:"message_chat_id"`
}

// messagePayload is MessageWebhookPayload.
type messagePayload struct {
	Type             string         `json:"type"`
	ID               int64          `json:"id"`
	Event            string         `json:"event"`
	EntityType       string         `json:"entity_type"`
	EntityID         int64          `json:"entity_id"`
	Content          string         `json:"content"`
	UserID           int64          `json:"user_id"`
	CreatedAt        string         `json:"created_at"`
	ChangedAt        *string        `json:"changed_at"`
	URL              string         `json:"url"`
	ChatID           int64          `json:"chat_id"`
	ParentMessageID  *int64         `json:"parent_message_id"`
	Thread           *webhookThread `json:"thread"`
	WebhookTimestamp int64          `json:"webhook_timestamp"`
}

// buttonPayload is ButtonWebhookPayload.
type buttonPayload struct {
	Type             string `json:"type"`
	Event            string `json:"event"`
	MessageID        int64  `json:"message_id"`
	TriggerID        string `json:"trigger_id"`
	Data             string `json:"data"`
	UserID           int64  `json:"user_id"`
	ChatID           int64  `json:"chat_id"`
	WebhookTimestamp int64  `json:"webhook_timestamp"`
}

// chatJSON is the Chat schema.
type chatJSON struct {
	ID            int64   `json:"id"`
	Name          string  `json:"name"`
	CreatedAt     string  `json:"created_at"`
	OwnerID       int64   `json:"owner_id"`
	MemberIDs     []int64 `json:"member_ids"`
	GroupTagIDs   []int64 `json:"group_tag_ids"`
	Channel       bool    `json:"channel"`
	Archived      bool    `json:"archived"`
	Personal      bool    `json:"personal"`
	Public        bool    `json:"public"`
	LastMessageAt string  `json:"last_message_at"`
	MeetRoomURL   string  `json:"meet_room_url"`
}

// userJSON is the User schema; the fields the fake has no story for carry
// their zero value or null.
type userJSON struct {
	ID               int64             `json:"id"`
	FirstName        *string           `json:"first_name"`
	LastName         *string           `json:"last_name"`
	Nickname         string            `json:"nickname"`
	Email            *string           `json:"email"`
	PhoneNumber      *string           `json:"phone_number"`
	Department       *string           `json:"department"`
	Title            *string           `json:"title"`
	Role             string            `json:"role"`
	Suspended        bool              `json:"suspended"`
	InviteStatus     string            `json:"invite_status"`
	InviterID        *int64            `json:"inviter_id"`
	ListTags         []string          `json:"list_tags"`
	CustomProperties []json.RawMessage `json:"custom_properties"`
	UserStatus       json.RawMessage   `json:"user_status"`
	Bot              bool              `json:"bot"`
	SSO              bool              `json:"sso"`
	CreatedAt        string            `json:"created_at"`
	LastActivityAt   *string           `json:"last_activity_at"`
	TimeZone         *string           `json:"time_zone"`
	ImageURL         *string           `json:"image_url"`
}

// tokenInfo is AccessTokenInfo.
type tokenInfo struct {
	ID         int64    `json:"id"`
	Token      string   `json:"token"`
	Name       *string  `json:"name"`
	UserID     int64    `json:"user_id"`
	Scopes     []string `json:"scopes"`
	CreatedAt  string   `json:"created_at"`
	RevokedAt  *string  `json:"revoked_at"`
	ExpiresIn  *int64   `json:"expires_in"`
	LastUsedAt *string  `json:"last_used_at"`
}

// apiErrorItem is ApiErrorItem; apiError wraps a list of them.
type apiErrorItem struct {
	Key     string         `json:"key"`
	Value   *string        `json:"value"`
	Message string         `json:"message"`
	Code    string         `json:"code"`
	Payload map[string]any `json:"payload"`
}

type apiError struct {
	Errors []apiErrorItem `json:"errors"`
}

// oauthError is OAuthError, the body of a 401 and of a scope 403.
type oauthError struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// timestampLayout is the ISO-8601 form Pachca prints, milliseconds and Z.
const timestampLayout = "2006-01-02T15:04:05.000Z"

func formatTime(t time.Time) string { return t.UTC().Format(timestampLayout) }
