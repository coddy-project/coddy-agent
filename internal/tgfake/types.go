package tgfake

// The wire shapes of the Bot API objects the gateway sends and reads. They
// are written here rather than borrowed from a Telegram library so that the
// fake stays free of build tags and of the library's opinions: any client
// that speaks the Bot API can point at it.

// User mirrors the Bot API User object.
type User struct {
	ID        int64  `json:"id"`
	IsBot     bool   `json:"is_bot"`
	FirstName string `json:"first_name"`
	LastName  string `json:"last_name,omitempty"`
	Username  string `json:"username,omitempty"`
}

// Chat mirrors the Bot API Chat object: type is private, group, supergroup
// or channel.
type Chat struct {
	ID        int64  `json:"id"`
	Type      string `json:"type"`
	Title     string `json:"title,omitempty"`
	FirstName string `json:"first_name,omitempty"`
	Username  string `json:"username,omitempty"`
}

// MessageEntity marks a span of a message: a bot_command at offset 0 is what
// makes a text a command for every Bot API library.
type MessageEntity struct {
	Type   string `json:"type"`
	Offset int    `json:"offset"`
	Length int    `json:"length"`
}

// InlineKeyboardButton is one button of an inline keyboard.
type InlineKeyboardButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data,omitempty"`
	URL          string `json:"url,omitempty"`
}

// InlineKeyboardMarkup is the reply_markup the gateway attaches to its menus.
type InlineKeyboardMarkup struct {
	InlineKeyboard [][]InlineKeyboardButton `json:"inline_keyboard"`
}

// Message mirrors the subset of the Bot API Message object the gateway reads
// and writes.
type Message struct {
	MessageID      int                   `json:"message_id"`
	From           *User                 `json:"from,omitempty"`
	Chat           Chat                  `json:"chat"`
	Date           int64                 `json:"date"`
	EditDate       int64                 `json:"edit_date,omitempty"`
	Text           string                `json:"text,omitempty"`
	Entities       []MessageEntity       `json:"entities,omitempty"`
	ReplyToMessage *Message              `json:"reply_to_message,omitempty"`
	ReplyMarkup    *InlineKeyboardMarkup `json:"reply_markup,omitempty"`
}

// CallbackQuery is what a tap on an inline button delivers.
type CallbackQuery struct {
	ID           string   `json:"id"`
	From         User     `json:"from"`
	Message      *Message `json:"message,omitempty"`
	ChatInstance string   `json:"chat_instance"`
	Data         string   `json:"data,omitempty"`
}

// Update is one item of a getUpdates answer. The fake produces messages and
// callback queries, the two kinds the gateway handles.
type Update struct {
	UpdateID      int            `json:"update_id"`
	Message       *Message       `json:"message,omitempty"`
	CallbackQuery *CallbackQuery `json:"callback_query,omitempty"`
}

// BotCommand is one entry of setMyCommands.
type BotCommand struct {
	Command     string `json:"command"`
	Description string `json:"description"`
}

// apiResponse is the envelope every Bot API method answers with.
type apiResponse struct {
	OK          bool            `json:"ok"`
	Result      any             `json:"result,omitempty"`
	ErrorCode   int             `json:"error_code,omitempty"`
	Description string          `json:"description,omitempty"`
	Parameters  *responseParams `json:"parameters,omitempty"`
}

// responseParams carries retry_after on a 429.
type responseParams struct {
	RetryAfter int `json:"retry_after,omitempty"`
}
