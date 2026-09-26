//go:build gateway || gateway.telegram

package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/EvilFreelancer/coddy-agent/external/gateway/sessionstore"
	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
	"github.com/EvilFreelancer/coddy-agent/internal/logger"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/tgfake"
)

func sessionRow(id, title, updated string) acp.SessionListInfo {
	row := acp.SessionListInfo{SessionID: id, CWD: "/work"}
	if title != "" {
		t := title
		row.Title = &t
	}
	if updated != "" {
		u := updated
		row.UpdatedAt = &u
	}
	return row
}

// newResumeTestWorld builds a bot over a server keeping rows, with the stub
// Telegram API and a persisted store of its own, for the edge cases below.
func newResumeTestWorld(t *testing.T, rows ...acp.SessionListInfo) *resumeWorld {
	t.Helper()
	w := &resumeWorld{runner: newResumeRunner()}
	w.runner.rows = append(w.runner.rows, rows...)
	w.srv = httptest.NewServer(http.HandlerFunc(w.handler))
	w.api = &tgbotapi.BotAPI{Token: "TESTTOKEN", Client: &http.Client{}, Buffer: 100}
	w.api.SetAPIEndpoint(w.srv.URL + "/bot%s/%s")
	w.storePath = filepath.Join(t.TempDir(), "gateway_sessions.json")
	if err := w.buildBot(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(w.close)
	return w
}

// A pasted id names one session even when another session's title contains
// it, so an id never opens a picker.
func TestMatchSessionsPrefersTheExactID(t *testing.T) {
	rows := []acp.SessionListInfo{
		sessionRow("sess_a", "notes about sess_b", ""),
		sessionRow("sess_b", "second", ""),
		sessionRow("sess_bb", "third", ""),
	}
	got := matchSessions(rows, "sess_b")
	if len(got) != 1 || got[0].SessionID != "sess_b" {
		t.Fatalf("matchSessions(exact id) = %v, want only sess_b", ids(got))
	}
}

// Two ids that differ only in case are two sessions on Linux. The exact match
// is byte for byte, so the one typed is the one resumed; typed in another
// case, both are offered instead of one being picked in silence.
func TestMatchSessionsExactIDIsCaseSensitiveAndThePrefixIsNot(t *testing.T) {
	rows := []acp.SessionListInfo{
		sessionRow("Review-A", "first", ""),
		sessionRow("review-a", "second", ""),
	}
	if got := ids(matchSessions(rows, "review-a")); strings.Join(got, ",") != "review-a" {
		t.Fatalf("matchSessions(exact lower) = %v, want only review-a", got)
	}
	if got := ids(matchSessions(rows, "Review-A")); strings.Join(got, ",") != "Review-A" {
		t.Fatalf("matchSessions(exact upper) = %v, want only Review-A", got)
	}
	if got := ids(matchSessions(rows, "REVIEW-A")); strings.Join(got, ",") != "Review-A,review-a" {
		t.Fatalf("matchSessions(other case) = %v, want both", got)
	}
}

func TestMatchSessionsReadsIDPrefixAndTitleCaseInsensitively(t *testing.T) {
	rows := []acp.SessionListInfo{
		sessionRow("sess_a", "Fix the login redirect", ""),
		sessionRow("sess_b", "Login form validation", ""),
		sessionRow("sess_c", "Write release notes", ""),
		sessionRow("sess_d", "", ""),
	}
	cases := []struct {
		query string
		want  []string
	}{
		{"LOGIN", []string{"sess_a", "sess_b"}},
		{"SESS_", []string{"sess_a", "sess_b", "sess_c", "sess_d"}},
		{" release notes ", []string{"sess_c"}},
		{"deploy", nil},
		{"   ", nil},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			got := ids(matchSessions(rows, tc.query))
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Fatalf("matchSessions(%q) = %v, want %v", tc.query, got, tc.want)
			}
		})
	}
}

func ids(rows []acp.SessionListInfo) []string {
	out := make([]string, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.SessionID)
	}
	return out
}

// sessionButtonsOf returns the session buttons of a keyboard and the labels
// of its navigation row.
func sessionButtonsOf(kb tgbotapi.InlineKeyboardMarkup) (sessions []tgbotapi.InlineKeyboardButton, nav []string) {
	for _, row := range kb.InlineKeyboard {
		for _, btn := range row {
			if btn.CallbackData != nil && strings.HasPrefix(*btn.CallbackData, resumePickPrefix) {
				sessions = append(sessions, btn)
			} else {
				nav = append(nav, btn.Text)
			}
		}
	}
	return sessions, nav
}

func TestResumeMenuPagesAndMarksTheCurrentSession(t *testing.T) {
	var rows []acp.SessionListInfo
	for i := 0; i < 20; i++ {
		rows = append(rows, sessionRow("sess_"+strings.Repeat("a", 22)+string(rune('a'+i)), "Session "+string(rune('a'+i)), ""))
	}
	current := rows[9].SessionID
	now := time.Now()

	cases := []struct {
		page        int
		wantButtons int
		wantNav     string
		wantText    string
	}{
		{0, resumePageSize, "Next ▶", "Page 1 of 3 · 20 sessions"},
		{1, resumePageSize, "◀ Prev,Next ▶", "Page 2 of 3 · 20 sessions"},
		{2, 4, "◀ Prev", "Page 3 of 3 · 20 sessions"},
		// Past the end lands on the last page; before the start on the first.
		{99, 4, "◀ Prev", "Page 3 of 3"},
		{-5, resumePageSize, "Next ▶", "Page 1 of 3"},
	}
	for _, tc := range cases {
		menu := buildResumeMenu(rows, current, tc.page, "", now)
		sessions, nav := sessionButtonsOf(menu.keyboard)
		if len(sessions) != tc.wantButtons {
			t.Fatalf("page %d: %d session buttons, want %d", tc.page, len(sessions), tc.wantButtons)
		}
		if got := strings.Join(nav, ","); got != tc.wantNav {
			t.Fatalf("page %d: navigation %q, want %q", tc.page, got, tc.wantNav)
		}
		if !strings.Contains(menu.text, tc.wantText) {
			t.Fatalf("page %d: text %q does not say %q", tc.page, menu.text, tc.wantText)
		}
	}

	// The current session sits on page 2 (rows 8..15) and is the only one marked.
	menu := buildResumeMenu(rows, current, 1, "", now)
	sessions, _ := sessionButtonsOf(menu.keyboard)
	marked := 0
	for _, btn := range sessions {
		if strings.HasPrefix(btn.Text, "✓ ") {
			marked++
			if *btn.CallbackData != resumePickPrefix+current {
				t.Fatalf("the marked button carries %q, want the current session", *btn.CallbackData)
			}
		}
	}
	if marked != 1 {
		t.Fatalf("%d buttons marked as current, want 1", marked)
	}
	if !strings.Contains(menu.text, "Current: "+current) {
		t.Fatalf("text %q does not name the current session", menu.text)
	}

	// Without a current session nothing is marked and the text says so.
	menu = buildResumeMenu(rows, "", 0, "", now)
	sessions, _ = sessionButtonsOf(menu.keyboard)
	for _, btn := range sessions {
		if strings.HasPrefix(btn.Text, "✓ ") {
			t.Fatalf("a button is marked with no current session: %q", btn.Text)
		}
	}
	if !strings.Contains(menu.text, "Current: none yet") {
		t.Fatalf("text %q does not say there is no current session", menu.text)
	}
}

// A query cannot travel in the callback payload, so the matches come as one
// page with no navigation and the text says how many there were.
func TestResumeMenuOverAQueryHasNoNavigationAndCountsTheMatches(t *testing.T) {
	var rows []acp.SessionListInfo
	for i := 0; i < 10; i++ {
		rows = append(rows, sessionRow("sess_"+strings.Repeat("b", 23)+string(rune('a'+i)), "login "+string(rune('a'+i)), ""))
	}
	menu := buildResumeMenu(rows, "", 3, "login", time.Now())
	sessions, nav := sessionButtonsOf(menu.keyboard)
	if len(sessions) != resumePageSize || len(nav) != 0 {
		t.Fatalf("%d session buttons and %v navigation, want %d and none", len(sessions), nav, resumePageSize)
	}
	if !strings.Contains(menu.text, `10 sessions match "login", showing the first 8`) {
		t.Fatalf("text %q does not count the matches", menu.text)
	}
	menu = buildResumeMenu(rows[:2], "", 0, "login", time.Now())
	if !strings.Contains(menu.text, `2 sessions match "login"`) || strings.Contains(menu.text, "showing") {
		t.Fatalf("text %q for two matches", menu.text)
	}
}

func TestResumeButtonLabel(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	long := strings.Repeat("я", 60)
	cases := []struct {
		name    string
		row     acp.SessionListInfo
		current bool
		want    string
	}{
		{"title and age", sessionRow("sess_a", "Fix the login redirect", "2026-09-18T10:00:00Z"), false, "Fix the login redirect · 2h ago"},
		{"current", sessionRow("sess_a", "Fix the login redirect", "2026-09-18T10:00:00Z"), true, "✓ Fix the login redirect · 2h ago"},
		{"no title", sessionRow("sess_a", "", "2026-09-18T11:59:30Z"), false, "sess_a · just now"},
		{"no stamp", sessionRow("sess_a", "Title", ""), false, "Title"},
		{"unreadable stamp", sessionRow("sess_a", "Title", "yesterday"), false, "Title"},
		{"long title is cut on a rune", sessionRow("sess_a", long, ""), false, strings.Repeat("я", resumeLabelMaxRunes-1) + "…"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resumeButtonLabel(tc.row, tc.current, now); got != tc.want {
				t.Fatalf("label = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRelativeAge(t *testing.T) {
	now := time.Date(2026, 9, 18, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		stamp string
		want  string
	}{
		{"2026-09-18T11:59:40Z", "just now"},
		{"2026-09-18T12:00:30Z", "just now"}, // a stamp ahead of the clock is not negative time
		{"2026-09-18T11:15:00Z", "45m ago"},
		{"2026-09-18T03:00:00Z", "9h ago"},
		{"2026-09-11T12:00:00Z", "7d ago"},
		{"2026-06-01T12:00:00Z", "2026-06-01"},
		{"", ""},
		{"not a stamp", ""},
	}
	for _, tc := range cases {
		stamp := tc.stamp
		if got := relativeAge(&stamp, now); got != tc.want {
			t.Fatalf("relativeAge(%q) = %q, want %q", tc.stamp, got, tc.want)
		}
	}
	if got := relativeAge(nil, now); got != "" {
		t.Fatalf("relativeAge(nil) = %q, want empty", got)
	}
}

// An id an operator chose with --session-id can be far longer than the 55
// bytes left in callback_data; it travels as a digest and comes back as itself.
func TestResumeCallbackRoundTripsALongSessionID(t *testing.T) {
	long := "release-" + strings.Repeat("x", 70)
	rows := []acp.SessionListInfo{sessionRow("sess_short", "a", ""), sessionRow(long, "b", "")}
	menu := buildResumeMenu(rows, "", 0, "", time.Now())
	sessions, _ := sessionButtonsOf(menu.keyboard)
	for _, btn := range sessions {
		if len(*btn.CallbackData) > telegramCallbackDataMax {
			t.Fatalf("callback_data %q is %d bytes, over the limit", *btn.CallbackData, len(*btn.CallbackData))
		}
		payload := strings.TrimPrefix(*btn.CallbackData, resumePickPrefix)
		row, ok := resolveResumeCallback(rows, payload)
		if !ok {
			t.Fatalf("payload %q of button %q resolves to nothing", payload, btn.Text)
		}
		if strings.TrimSuffix(btn.Text, "") == "" || (row.SessionID != long && row.SessionID != "sess_short") {
			t.Fatalf("payload %q resolved to %q", payload, row.SessionID)
		}
	}
	if _, ok := resolveResumeCallback(rows, "sess_gone"); ok {
		t.Fatal("an id nobody stores resolved to a session")
	}
	if _, ok := resolveResumeCallback(rows, long[:55]); ok {
		t.Fatal("a truncated id resolved to a session")
	}
	if _, ok := resolveResumeCallback(rows, callbackValue(len(resumePickPrefix), "release-"+strings.Repeat("y", 70))); ok {
		t.Fatal("the digest of a session since removed resolved to one")
	}
}

// A query that names no stored session is refused before the server is asked
// for anything: EnsureHTTPSession would create a session under that id.
func TestResumeRefusesAnIDNobodyStores(t *testing.T) {
	w := newResumeTestWorld(t, sessionRow("sess_aaaaaaaaaaaaaaaaaaaaaaaa", "Kept", "2026-09-18T10:00:00Z"))
	if err := w.userSends("/resume sess_nope"); err != nil {
		t.Fatal(err)
	}
	if got := w.bot.store.Peek(w.sessionKey()); got != "" {
		t.Fatalf("the chat was bound to %q", got)
	}
	if len(w.runner.ensured) != 0 {
		t.Fatalf("the server was asked for %v", w.runner.ensured)
	}
	if err := w.chatReceived("No session matches"); err != nil {
		t.Fatal(err)
	}
}

// The keyboard outlives the sessions it lists: a tap for one deleted since is
// answered in the chat, as a reply to the keyboard, and binds nothing. The
// callback query carries no alert: it was answered as the tap arrived, and
// Telegram refuses a second answer.
func TestResumeTapForASessionSinceDeletedLeavesTheMappingAlone(t *testing.T) {
	w := newResumeTestWorld(t,
		sessionRow("sess_aaaaaaaaaaaaaaaaaaaaaaaa", "Kept", "2026-09-18T10:00:00Z"),
		sessionRow("sess_bbbbbbbbbbbbbbbbbbbbbbbb", "Gone", "2026-09-18T09:00:00Z"),
	)
	if err := w.userSends("/resume"); err != nil {
		t.Fatal(err)
	}
	w.runner.mu.Lock()
	w.runner.rows = w.runner.rows[:1]
	w.runner.mu.Unlock()
	if err := w.tapButton("Gone"); err != nil {
		t.Fatal(err)
	}
	if got := w.bot.store.Peek(w.sessionKey()); got != "" {
		t.Fatalf("the chat was bound to %q", got)
	}
	if len(w.runner.ensured) != 0 {
		t.Fatalf("the server was asked for %v", w.runner.ensured)
	}
	replied := false
	w.mu.Lock()
	for _, call := range w.calls {
		switch {
		case call.method == "sendMessage" && strings.Contains(call.form.Get("text"), "no longer exists") &&
			call.form.Get("reply_to_message_id") == "2":
			replied = true
		case call.method == "answerCallbackQuery" && call.form.Get("text") != "":
			t.Errorf("the tap was answered with the alert %q, which Telegram refuses after the acknowledgement", call.form.Get("text"))
		}
	}
	w.mu.Unlock()
	if !replied {
		t.Fatal("the chat was not told that the session no longer exists")
	}
}

// failingResumeRunner is the resume spec's server with the failures a tap on
// the /resume keyboard can meet, switched on once the keyboard is in the chat.
type failingResumeRunner struct {
	*resumeRunner
	listErr   error
	ensureErr error
}

func (r *failingResumeRunner) HandleSessionList(ctx context.Context, params acp.SessionListParams) (*acp.SessionListResult, error) {
	if r.listErr != nil {
		return nil, r.listErr
	}
	return r.resumeRunner.HandleSessionList(ctx, params)
}

func (r *failingResumeRunner) EnsureHTTPSession(ctx context.Context, sessionID, cwd string) (*session.State, error) {
	if r.ensureErr != nil {
		return nil, r.ensureErr
	}
	return r.resumeRunner.EnsureHTTPSession(ctx, sessionID, cwd)
}

// A resume tap that fails says why in the chat, as a reply to the keyboard,
// and binds nothing. The query already has its one answer, the
// acknowledgement sent as the tap arrives, and Telegram refuses a second: an
// alert would never be seen.
func TestResumeTapFailuresReplyInTheChat(t *testing.T) {
	cases := []struct {
		name  string
		spoil func(r *failingResumeRunner)
		want  string
	}{
		{"sessions cannot be listed", func(r *failingResumeRunner) { r.listErr = errors.New("sessions dir unreadable") },
			"❌ Cannot list sessions: sessions dir unreadable"},
		{"session deleted since the keyboard was sent", func(r *failingResumeRunner) {
			r.mu.Lock()
			r.rows = r.rows[:1]
			r.mu.Unlock()
		}, "❌ That session no longer exists."},
		{"session cannot be loaded", func(r *failingResumeRunner) { r.ensureErr = errors.New("bundle unreadable") },
			"❌ Cannot resume that session: bundle unreadable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeAPI(t, tgfake.Options{})
			runner := &failingResumeRunner{resumeRunner: newResumeRunner()}
			runner.keep("sess_aaaaaaaaaaaaaaaaaaaaaaaa", "Kept", "")
			runner.keep("sess_bbbbbbbbbbbbbbbbbbbbbbbb", "Gone", "")
			bot := New(&config.TelegramGatewayConfig{DefaultAccess: config.AccessAll, DefaultIsolation: config.IsolationIndividual},
				runner, "/work", slog.New(slog.DiscardHandler), "", nil)
			key := sessionstore.SessionKey(adapterName, resumeChatID, resumeUserID, config.IsolationIndividual, false)
			bot.processMessage(t.Context(), f.api, f.userMessage(resumeChatID, resumeUserID, "/resume"), key)
			tc.spoil(runner)
			cbq, err := f.tap(resumeChatID, resumeUserID, "Gone")
			if err != nil {
				t.Fatal(err)
			}
			bot.handleCallback(t.Context(), f.api, cbq)

			replies := f.repliesTo(resumeChatID, cbq.Message.MessageID)
			if len(replies) != 1 || replies[0].Text != tc.want {
				t.Fatalf("replies to the keyboard = %+v, want one saying %q:\n%s", replies, tc.want, f.fake.Chat(resumeChatID).Text())
			}
			if got := bot.store.Peek(key); got != "" {
				t.Fatalf("the chat was bound to %q", got)
			}
			requireOneAnswer(t, f, cbq.ID)
		})
	}
}

// Resuming leaves the session the chat came from loaded: /clear is what drops
// one, and the one left behind may be watched elsewhere or resumed again.
func TestResumeKeepsTheSessionLeftBehindLoaded(t *testing.T) {
	w := newResumeTestWorld(t, sessionRow("sess_bbbbbbbbbbbbbbbbbbbbbbbb", "Other", "2026-09-18T09:00:00Z"))
	if err := w.userSends("hello"); err != nil {
		t.Fatal(err)
	}
	own := w.bot.store.Peek(w.sessionKey())
	if own == "" {
		t.Fatal("the chat has no session after a message")
	}
	if err := w.userSends("/resume sess_bbbb"); err != nil {
		t.Fatal(err)
	}
	if got := w.bot.store.Peek(w.sessionKey()); got != "sess_bbbbbbbbbbbbbbbbbbbbbbbb" {
		t.Fatalf("the chat is bound to %q", got)
	}
	if len(w.runner.forgot) != 0 {
		t.Fatalf("the session left behind was dropped: %v", w.runner.forgot)
	}
	w.runner.mu.Lock()
	_, stillLive := w.runner.live[own]
	w.runner.mu.Unlock()
	if !stillLive {
		t.Fatal("the session left behind is no longer live")
	}
	// Naming the session the chat is already on changes nothing and says so.
	before := len(w.runner.ensured)
	if err := w.userSends("/resume sess_bbbb"); err != nil {
		t.Fatal(err)
	}
	if err := w.chatReceived("already on Other"); err != nil {
		t.Fatal(err)
	}
	if len(w.runner.ensured) != before {
		t.Fatal("resuming the current session loaded it again")
	}
}

// The confirmation replaces the menu so the keyboard does not outlive the
// choice, and a page turn redraws the menu in place.
func TestResumeTapReplacesTheMenuAndPagingEditsIt(t *testing.T) {
	var rows []acp.SessionListInfo
	for i := 0; i < resumePageSize+1; i++ {
		rows = append(rows, sessionRow("sess_"+strings.Repeat("c", 23)+string(rune('a'+i)), "Chat "+string(rune('a'+i)), "2026-09-18T10:00:00Z"))
	}
	w := newResumeTestWorld(t, rows...)
	if err := w.userSends("/resume"); err != nil {
		t.Fatal(err)
	}
	kb, err := w.lastKeyboard()
	if err != nil {
		t.Fatal(err)
	}
	_, nav := sessionButtonsOf(kb)
	if strings.Join(nav, ",") != "Next ▶" {
		t.Fatalf("first page navigation = %v", nav)
	}
	w.bot.handleCallback(t.Context(), w.api, &tgbotapi.CallbackQuery{
		ID: "cb-page", From: &tgbotapi.User{ID: resumeUserID},
		Message: &tgbotapi.Message{MessageID: 2, Chat: &tgbotapi.Chat{ID: resumeChatID, Type: "private"}},
		Data:    resumePageCallback(1),
	})
	w.mu.Lock()
	last := w.calls[len(w.calls)-1]
	w.mu.Unlock()
	if last.method != "editMessageText" || !strings.Contains(last.form.Get("text"), "Page 2 of 2") {
		t.Fatalf("the page turn did not edit the menu: %s %q", last.method, last.form.Get("text"))
	}
	var edited tgbotapi.InlineKeyboardMarkup
	if err := json.Unmarshal([]byte(last.form.Get("reply_markup")), &edited); err != nil {
		t.Fatal(err)
	}
	sessions, nav := sessionButtonsOf(edited)
	if len(sessions) != 1 || strings.Join(nav, ",") != "◀ Prev" {
		t.Fatalf("second page: %d sessions, navigation %v", len(sessions), nav)
	}

	if err := w.tapButton("Chat i"); err != nil {
		t.Fatal(err)
	}
	w.mu.Lock()
	last = w.calls[len(w.calls)-1]
	w.mu.Unlock()
	if last.method != "editMessageText" || !strings.Contains(last.form.Get("text"), "Resumed: Chat i") {
		t.Fatalf("the tap did not replace the menu: %s %q", last.method, last.form.Get("text"))
	}
	if last.form.Get("reply_markup") != "" {
		t.Fatalf("the confirmation still carries a keyboard: %s", last.form.Get("reply_markup"))
	}
}

// In a group the command is answered without a mention, like /clear.
func TestGroupChatAnswersResumeWithoutAMention(t *testing.T) {
	base, _, err := logger.New(config.Logger{Level: config.LogLevelError, Format: config.LogFormatText, Outputs: []string{config.LogOutputStderr}})
	if err != nil {
		t.Fatal(err)
	}
	b := New(&config.TelegramGatewayConfig{}, nil, "", logger.Component(base, logger.ComponentGatewayTelegram), "", nil)
	b.botName = "coddy_bot"
	msg := commandMessage("/resume login")
	if !b.shouldRespond(msg, msg.Text) {
		t.Fatal("/resume in a group is not answered")
	}
}
