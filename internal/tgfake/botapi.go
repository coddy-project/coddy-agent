package tgfake

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// serveBotAPI answers /bot<token>/<method>. Anything else under / is a 404
// in the Bot API's own envelope, which is what api.telegram.org answers for
// an unknown path too.
func (s *Server) serveBotAPI(w http.ResponseWriter, r *http.Request) {
	rest, ok := strings.CutPrefix(r.URL.Path, "/bot")
	if !ok {
		s.writeError(w, "", nil, http.StatusNotFound, "Not Found", 0)
		return
	}
	token, method, found := strings.Cut(rest, "/")
	if !found || method == "" {
		s.writeError(w, "", nil, http.StatusNotFound, "Not Found", 0)
		return
	}
	method = strings.TrimSuffix(method, "/")
	params := parseParams(r)
	if s.opts.Token != "" && token != s.opts.Token {
		s.writeError(w, method, params, http.StatusUnauthorized, "Unauthorized", 0)
		return
	}
	if fault := s.takeFault(method, params); fault != nil {
		s.writeError(w, method, params, fault.Code, fault.Description, fault.RetryAfter)
		return
	}
	switch strings.ToLower(method) {
	case "getme":
		s.writeResult(w, method, params, s.me())
	case "getupdates":
		s.getUpdates(w, r, method, params)
	case "setmycommands":
		s.setMyCommands(w, method, params)
	case "getmycommands":
		s.writeResult(w, method, params, s.Commands())
	case "sendmessage":
		s.sendMessage(w, method, params)
	case "editmessagetext", "editmessagereplymarkup":
		s.editMessage(w, method, params)
	case "deletemessage":
		s.deleteMessage(w, method, params)
	case "sendchataction":
		s.sendChatAction(w, method, params)
	case "answercallbackquery":
		s.answerCallbackQuery(w, method, params)
	case "sendrichmessage":
		s.sendRichMessage(w, method, params)
	case "sendrichmessagedraft":
		s.sendRichMessageDraft(w, method, params)
	case "deletewebhook":
		s.writeResult(w, method, params, true)
	case "getwebhookinfo":
		s.writeResult(w, method, params, map[string]any{"url": "", "has_custom_certificate": false,
			"pending_update_count": s.PendingUpdates(), "allowed_updates": s.AllowedUpdates()})
	default:
		s.writeError(w, method, params, http.StatusNotFound, "Not Found: method not found", 0)
	}
}

// parseParams reads the request the way the Bot API does: query string and
// urlencoded form, or a JSON object whose nested values stay JSON text - the
// form a library encodes reply_markup and rich_message in anyway.
func parseParams(r *http.Request) url.Values {
	if strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		out := url.Values{}
		for k, v := range r.URL.Query() {
			out[k] = v
		}
		var doc map[string]json.RawMessage
		body, _ := io.ReadAll(io.LimitReader(r.Body, 4<<20))
		if json.Unmarshal(body, &doc) == nil {
			for k, raw := range doc {
				var str string
				if json.Unmarshal(raw, &str) == nil {
					out.Set(k, str)
				} else {
					out.Set(k, string(raw))
				}
			}
		}
		return out
	}
	_ = r.ParseForm()
	return r.Form
}

func (s *Server) takeFault(method string, params url.Values) *Fault {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.takeFaultLocked(method, params)
}

func (s *Server) me() map[string]any {
	return map[string]any{
		"id":                          s.opts.BotID,
		"is_bot":                      true,
		"first_name":                  "Coddy Fake",
		"username":                    s.opts.BotUsername,
		"can_join_groups":             true,
		"can_read_all_group_messages": false,
		"supports_inline_queries":     false,
	}
}

// getUpdates confirms what the client has seen and answers with what is
// pending, holding the request open up to the smaller of the client's
// timeout and MaxPollWait when there is nothing yet. allowed_updates works
// as on api.telegram.org: a list replaces the subscription, an empty list
// means every kind, and none at all (or the literal null some libraries
// send) keeps whatever the previous poll asked for.
func (s *Server) getUpdates(w http.ResponseWriter, r *http.Request, method string, params url.Values) {
	offset := atoi(params.Get("offset"))
	limit := atoi(params.Get("limit"))
	if raw := strings.TrimSpace(params.Get("allowed_updates")); raw != "" && raw != "null" {
		var kinds []string
		if err := json.Unmarshal([]byte(raw), &kinds); err != nil {
			s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: can't parse allowed_updates JSON array", 0)
			return
		}
		s.SetAllowedUpdates(kinds)
	}
	wait := time.Duration(atoi(params.Get("timeout"))) * time.Second
	if wait > s.opts.MaxPollWait {
		wait = s.opts.MaxPollWait
	}
	deadline := time.Now().Add(wait)
	for {
		s.mu.Lock()
		batch := s.takeUpdatesLocked(offset, limit)
		wake := s.wake
		s.mu.Unlock()
		if len(batch) > 0 || wait <= 0 || !time.Now().Before(deadline) || s.closed() {
			s.writeResult(w, method, params, batch)
			return
		}
		timer := time.NewTimer(time.Until(deadline))
		select {
		case <-wake:
		case <-timer.C:
		case <-s.closing:
		case <-r.Context().Done():
			// The client went away; nothing to answer.
			timer.Stop()
			return
		}
		timer.Stop()
	}
}

// closed reports whether Close was called.
func (s *Server) closed() bool {
	select {
	case <-s.closing:
		return true
	default:
		return false
	}
}

func (s *Server) setMyCommands(w http.ResponseWriter, method string, params url.Values) {
	var cmds []BotCommand
	if err := json.Unmarshal([]byte(params.Get("commands")), &cmds); err != nil {
		s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: can't parse commands JSON object", 0)
		return
	}
	s.mu.Lock()
	s.commands = cmds
	s.mu.Unlock()
	s.writeResult(w, method, params, true)
}

func (s *Server) sendMessage(w http.ResponseWriter, method string, params url.Values) {
	chatID, ok := chatIDOf(params)
	if !ok {
		s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: chat_id is empty", 0)
		return
	}
	text := params.Get("text")
	if text == "" {
		s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: message text is empty", 0)
		return
	}
	markup := parseKeyboard(params.Get("reply_markup"))
	if problem := validateKeyboard(markup); problem != "" {
		s.writeError(w, method, params, http.StatusBadRequest, problem, 0)
		return
	}
	s.mu.Lock()
	chat := s.ensureChatLocked(chatID, "", "", nil)
	msg := &Message{
		From: s.botUser(),
		Chat: chat.wire(),
		Date: s.now().Unix(),
		Text: text,
	}
	if id := atoi(params.Get("reply_to_message_id")); id != 0 {
		if target := chat.byID[id]; target != nil {
			msg.ReplyToMessage = target.quoted()
		}
	}
	msg.ReplyMarkup = markup
	stored := chat.appendLocked(msg, true, params.Get("parse_mode"), false)
	result := stored.clone()
	s.mu.Unlock()
	s.writeResult(w, method, params, result)
}

func (s *Server) editMessage(w http.ResponseWriter, method string, params url.Values) {
	chatID, _ := chatIDOf(params)
	msgID := atoi(params.Get("message_id"))
	s.mu.Lock()
	chat := s.chats[chatID]
	var target *storedMessage
	if chat != nil {
		target = chat.byID[msgID]
	}
	if target == nil || target.deleted {
		s.mu.Unlock()
		s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: message to edit not found", 0)
		return
	}
	if !target.fromBot {
		s.mu.Unlock()
		s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: message can't be edited", 0)
		return
	}
	newText := target.msg.Text
	if strings.EqualFold(method, "editMessageText") {
		newText = params.Get("text")
		if newText == "" {
			s.mu.Unlock()
			s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: message text is empty", 0)
			return
		}
	}
	newMarkup := parseKeyboard(params.Get("reply_markup"))
	if problem := validateKeyboard(newMarkup); problem != "" {
		s.mu.Unlock()
		s.writeError(w, method, params, http.StatusBadRequest, problem, 0)
		return
	}
	if newText == target.msg.Text && sameKeyboard(newMarkup, target.msg.ReplyMarkup) {
		s.mu.Unlock()
		s.writeError(w, method, params, http.StatusBadRequest,
			"Bad Request: message is not modified: specified new message content and reply markup are exactly the same as a current content and reply markup of the message", 0)
		return
	}
	target.msg.Text = newText
	target.msg.ReplyMarkup = newMarkup
	target.msg.EditDate = s.now().Unix()
	target.edited = true
	if pm := params.Get("parse_mode"); strings.EqualFold(method, "editMessageText") {
		target.parseMode = pm
	}
	result := target.clone()
	s.mu.Unlock()
	s.writeResult(w, method, params, result)
}

func (s *Server) deleteMessage(w http.ResponseWriter, method string, params url.Values) {
	chatID, _ := chatIDOf(params)
	msgID := atoi(params.Get("message_id"))
	s.mu.Lock()
	chat := s.chats[chatID]
	var target *storedMessage
	if chat != nil {
		target = chat.byID[msgID]
	}
	if target == nil || target.deleted {
		s.mu.Unlock()
		s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: message to delete not found", 0)
		return
	}
	target.deleted = true
	s.mu.Unlock()
	s.writeResult(w, method, params, true)
}

func (s *Server) sendChatAction(w http.ResponseWriter, method string, params url.Values) {
	chatID, ok := chatIDOf(params)
	if !ok {
		s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: chat_id is empty", 0)
		return
	}
	s.mu.Lock()
	chat := s.ensureChatLocked(chatID, "", "", nil)
	chat.lastTyped = s.now()
	s.mu.Unlock()
	s.writeResult(w, method, params, true)
}

func (s *Server) answerCallbackQuery(w http.ResponseWriter, method string, params url.Values) {
	id := params.Get("callback_query_id")
	if id == "" {
		s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: callback_query_id is empty", 0)
		return
	}
	answer := CallbackAnswer{ID: id, Text: params.Get("text"), ShowAlert: params.Get("show_alert") == "true"}
	s.mu.Lock()
	// The query names no chat; the tap that minted it does.
	if chat := s.chats[s.cbqChat[id]]; chat != nil {
		chat.callbacks = append(chat.callbacks, answer)
	}
	s.mu.Unlock()
	s.writeResult(w, method, params, true)
}

// richInput is the InputRichMessage object of Bot API 10.1.
type richInput struct {
	Markdown string `json:"markdown"`
	HTML     string `json:"html"`
}

func (s *Server) sendRichMessage(w http.ResponseWriter, method string, params url.Values) {
	chatID, ok := chatIDOf(params)
	if !ok {
		s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: chat_id is empty", 0)
		return
	}
	var in richInput
	if err := json.Unmarshal([]byte(params.Get("rich_message")), &in); err != nil || (in.Markdown == "" && in.HTML == "") {
		s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: can't parse rich_message JSON object", 0)
		return
	}
	body := in.Markdown
	if body == "" {
		body = in.HTML
	}
	s.mu.Lock()
	chat := s.ensureChatLocked(chatID, "", "", nil)
	msg := &Message{From: s.botUser(), Chat: chat.wire(), Date: s.now().Unix(), Text: body}
	var reply struct {
		MessageID int `json:"message_id"`
	}
	if json.Unmarshal([]byte(params.Get("reply_parameters")), &reply) == nil && reply.MessageID != 0 {
		if target := chat.byID[reply.MessageID]; target != nil {
			msg.ReplyToMessage = target.quoted()
		}
	}
	stored := chat.appendLocked(msg, true, "", true)
	result := stored.clone()
	s.mu.Unlock()
	s.writeResult(w, method, params, result)
}

func (s *Server) sendRichMessageDraft(w http.ResponseWriter, method string, params url.Values) {
	chatID, ok := chatIDOf(params)
	if !ok {
		s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: chat_id is empty", 0)
		return
	}
	draftID, err := strconv.ParseInt(params.Get("draft_id"), 10, 64)
	if err != nil || draftID == 0 {
		s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: draft_id must be a non-zero integer", 0)
		return
	}
	var in richInput
	if err := json.Unmarshal([]byte(params.Get("rich_message")), &in); err != nil {
		s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: can't parse rich_message JSON object", 0)
		return
	}
	s.mu.Lock()
	chat := s.ensureChatLocked(chatID, "", "", nil)
	now := s.now()
	chat.expireDraftsLocked(now)
	d := chat.drafts[draftID]
	if d == nil {
		d = &draft{id: draftID}
		chat.drafts[draftID] = d
	}
	d.markdown = in.Markdown
	d.updatedAt = now
	d.revisions++
	s.mu.Unlock()
	s.writeResult(w, method, params, true)
}

func (s *Server) botUser() *User {
	return &User{ID: s.opts.BotID, IsBot: true, FirstName: "Coddy Fake", Username: s.opts.BotUsername}
}

// writeResult answers ok:true and files the call.
func (s *Server) writeResult(w http.ResponseWriter, method string, params url.Values, result any) {
	body, _ := json.Marshal(apiResponse{OK: true, Result: result})
	s.respond(w, method, params, http.StatusOK, body)
}

// writeError answers ok:false with the Bot API's status-as-error_code
// convention and files the call.
func (s *Server) writeError(w http.ResponseWriter, method string, params url.Values, code int, description string, retryAfter int) {
	resp := apiResponse{OK: false, ErrorCode: code, Description: description}
	if retryAfter > 0 {
		resp.Parameters = &responseParams{RetryAfter: retryAfter}
	}
	body, _ := json.Marshal(resp)
	s.respond(w, method, params, code, body)
}

func (s *Server) respond(w http.ResponseWriter, method string, params url.Values, status int, body []byte) {
	if method != "" {
		s.mu.Lock()
		s.recordLocked(method, params, status, body)
		s.mu.Unlock()
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

func chatIDOf(params url.Values) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimSpace(params.Get("chat_id")), 10, 64)
	return id, err == nil && id != 0
}

// parseKeyboard reads reply_markup; a markup of another kind (a reply
// keyboard, a remove) is not an inline keyboard and reads as none.
func parseKeyboard(raw string) *InlineKeyboardMarkup {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var kb InlineKeyboardMarkup
	if err := json.Unmarshal([]byte(raw), &kb); err != nil || len(kb.InlineKeyboard) == 0 {
		return nil
	}
	return &kb
}

// callbackDataMax is Telegram's limit on a button's callback_data, in bytes.
const callbackDataMax = 64

// validateKeyboard refuses what api.telegram.org refuses: a button with
// nothing behind it, and callback_data over the limit - the mistake that is
// invisible until a real chat shows a keyboard that never arrives. It returns
// the error description, or "" for a keyboard Telegram would take.
func validateKeyboard(kb *InlineKeyboardMarkup) string {
	if kb == nil {
		return ""
	}
	for _, row := range kb.InlineKeyboard {
		for _, b := range row {
			if b.CallbackData == "" && b.URL == "" {
				return "Bad Request: BUTTON_TYPE_INVALID"
			}
			if len(b.CallbackData) > callbackDataMax {
				return "Bad Request: BUTTON_DATA_INVALID"
			}
		}
	}
	return ""
}

func sameKeyboard(a, b *InlineKeyboardMarkup) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	if len(a.InlineKeyboard) != len(b.InlineKeyboard) {
		return false
	}
	for i := range a.InlineKeyboard {
		if len(a.InlineKeyboard[i]) != len(b.InlineKeyboard[i]) {
			return false
		}
		for j := range a.InlineKeyboard[i] {
			if a.InlineKeyboard[i][j] != b.InlineKeyboard[i][j] {
				return false
			}
		}
	}
	return true
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}
