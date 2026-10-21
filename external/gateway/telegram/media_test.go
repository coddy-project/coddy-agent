//go:build gateway || gateway.telegram

package telegram

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image/color"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	tgfake "github.com/EvilFreelancer/tgfake/pkg/server"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/EvilFreelancer/coddy-agent/external/gateway/replyquote"
	"github.com/EvilFreelancer/coddy-agent/external/gateway/sessionstore"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// logRecords collects the adapter's log records, safe for the worker
// goroutines that write while a test reads.
type logRecords struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *logRecords) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *logRecords) logger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(l, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func (l *logRecords) raw() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// find returns the first record with msg whose attributes include want.
func (l *logRecords) find(msg string, want map[string]any) (map[string]any, bool) {
	for _, line := range strings.Split(strings.TrimSpace(l.raw()), "\n") {
		var rec map[string]any
		if json.Unmarshal([]byte(line), &rec) != nil || rec["msg"] != msg {
			continue
		}
		match := true
		for k, v := range want {
			if rec[k] != v {
				match = false
				break
			}
		}
		if match {
			return rec, true
		}
	}
	return nil, false
}

// mediaBot is a bot over a scripted runner whose model reads images when
// multimodal is set, pointed at the fake for the API and the file downloads.
func mediaBot(t *testing.T, multimodal bool, logs *logRecords) (*Bot, *scriptedRunner, *fakeAPI) {
	t.Helper()
	f := newFakeAPI(t, tgfake.Options{Token: fakeToken})
	runner := newScriptedRunner()
	runner.cfg.Models[0].Multimodal = multimodal
	log := slog.New(slog.DiscardHandler)
	if logs != nil {
		log = logs.logger()
	}
	b := New(&config.TelegramGatewayConfig{DefaultAccess: config.AccessAll, DefaultIsolation: config.IsolationIndividual, Admins: []int64{9}},
		runner, t.TempDir(), log, "", nil)
	b.botName = "tgfake_bot"
	b.setAPI(f.api)
	b.apiBase = f.srv.URL
	b.albumWait = 50 * time.Millisecond
	t.Cleanup(b.asks.stop)
	return b, runner, f
}

func privateKey(uid int64) string {
	return sessionstore.SessionKey(adapterName, uid, uid, config.IsolationIndividual, false)
}

func mustMedia(t *testing.T, f *fakeAPI, in tgfake.IncomingMedia) *tgbotapi.Message {
	t.Helper()
	msg, err := f.userMedia(in)
	if err != nil {
		t.Fatal(err)
	}
	return msg
}

// waitFor polls cond until it holds or two seconds pass.
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (r *scriptedRunner) turns() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.prompts)
}

// dataURLBytes decodes a base64 data URL and returns its type and bytes.
func dataURLBytes(t *testing.T, url string) (string, []byte) {
	t.Helper()
	header, payload, ok := strings.Cut(strings.TrimPrefix(url, "data:"), ",")
	if !ok || !strings.HasSuffix(header, ";base64") {
		t.Fatalf("not a base64 data URL: %.60q", url)
	}
	data, err := base64.StdEncoding.DecodeString(payload)
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSuffix(header, ";base64"), data
}

func TestMessageTextFallsBackToTheCaption(t *testing.T) {
	for _, tc := range []struct{ text, caption, want string }{
		{"hello", "", "hello"},
		{"", "  what is this?  ", "what is this?"},
		{"text wins", "caption", "text wins"},
		{"  ", "", ""},
	} {
		if got := messageText(&tgbotapi.Message{Text: tc.text, Caption: tc.caption}); got != tc.want {
			t.Errorf("messageText(%q, %q) = %q, want %q", tc.text, tc.caption, got, tc.want)
		}
	}
}

// The update line counts what the person wrote, the caption of a photo
// included, and says what came with it (issue #509: text_len=0 for every
// captioned photo sent triage the wrong way).
func TestUpdateLogCountsTheCaptionAndNamesTheMedia(t *testing.T) {
	logs := &logRecords{}
	b, _, f := mediaBot(t, false, logs)
	msg := mustMedia(t, f, tgfake.IncomingMedia{IncomingMessage: tgfake.IncomingMessage{ChatID: 9, UserID: 9}, Type: "photo", Data: solidPNG(4, 4, color.NRGBA{A: 255}), Caption: "what is this?"})
	b.dispatch(context.Background(), f.api, msg)
	if _, ok := logs.find("telegram: update", map[string]any{"text_len": float64(len("what is this?")), "media": "photo"}); !ok {
		t.Fatalf("no update line with the caption's length and the media:\n%s", logs.raw())
	}
	waitFor(t, "the refusal", func() bool { return len(f.repliesTo(9, msg.MessageID)) == 1 })
}

// A message with nothing the bot can pass on - a service message, a pin -
// is dropped with a reason in the log and no answer.
func TestEmptyMessageIsLoggedNotAnswered(t *testing.T) {
	logs := &logRecords{}
	b, runner, f := mediaBot(t, true, logs)
	msg := f.userMessage(9, 9, "placeholder")
	msg.Text = ""
	msg.PinnedMessage = &tgbotapi.Message{MessageID: 1}
	b.processMessage(context.Background(), f.api, msg, privateKey(9))
	if runner.turns() != 0 || len(f.repliesTo(9, msg.MessageID)) != 0 {
		t.Fatalf("an empty message ran a turn or was answered")
	}
	if _, ok := logs.find("telegram: update ignored", map[string]any{"reason": "empty message"}); !ok {
		t.Fatalf("the drop was not logged with its reason:\n%s", logs.raw())
	}
}

func TestUnknownCommandIsLogged(t *testing.T) {
	logs := &logRecords{}
	b, runner, f := mediaBot(t, true, logs)
	b.processMessage(context.Background(), f.api, f.userMessage(9, 9, "/nosuchcommand now"), privateKey(9))
	if runner.turns() != 0 {
		t.Fatal("an unknown command ran a turn")
	}
	if _, ok := logs.find("telegram: update ignored", map[string]any{"reason": "unknown command", "command": "nosuchcommand"}); !ok {
		t.Fatalf("the unknown command was not logged:\n%s", logs.raw())
	}
}

// Media the bot cannot pass on is answered in the chat, caption or not: a
// turn on the caption alone would answer as if the video were not there.
func TestUnsupportedMediaIsAnswered(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		set        func(*tgbotapi.Message)
	}{
		{"voice", "voice message", func(m *tgbotapi.Message) { m.Voice = &tgbotapi.Voice{FileID: "v"} }},
		{"video with a caption", "video", func(m *tgbotapi.Message) {
			m.Video = &tgbotapi.Video{FileID: "v"}
			m.Caption = "what happens here?"
		}},
		{"sticker", "sticker", func(m *tgbotapi.Message) { m.Sticker = &tgbotapi.Sticker{FileID: "s"} }},
		{"animation", "animation", func(m *tgbotapi.Message) {
			// Telegram sends an animation with a document beside it.
			m.Animation = &tgbotapi.Animation{FileID: "a"}
			m.Document = &tgbotapi.Document{FileID: "a", FileName: "funny.mp4", MimeType: "video/mp4"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			logs := &logRecords{}
			b, runner, f := mediaBot(t, true, logs)
			msg := f.userMessage(9, 9, "placeholder")
			msg.Text = ""
			tc.set(msg)
			b.processMessage(context.Background(), f.api, msg, privateKey(9))
			if runner.turns() != 0 {
				t.Fatalf("a %s ran a turn: %q", tc.name, runner.prompts)
			}
			replies := f.repliesTo(9, msg.MessageID)
			if len(replies) != 1 || !strings.Contains(replies[0].Text, tc.want) {
				t.Fatalf("the %s was not answered naming it: %+v", tc.name, replies)
			}
			if _, ok := logs.find("telegram: update refused", map[string]any{"reason": "unsupported media", "media": tc.want}); !ok {
				t.Fatalf("the refusal was not logged:\n%s", logs.raw())
			}
			if n := len(f.fake.Calls("getFile")); n != 0 {
				t.Fatalf("the bot asked for the file of a %s (%d getFile calls)", tc.name, n)
			}
		})
	}
}

// A model that does not read images is not handed files: the person is told
// how to go on, and nothing is downloaded.
func TestMediaNeedsAModelThatReadsImages(t *testing.T) {
	logs := &logRecords{}
	b, runner, f := mediaBot(t, false, logs)
	msg := mustMedia(t, f, tgfake.IncomingMedia{IncomingMessage: tgfake.IncomingMessage{ChatID: 9, UserID: 9}, Type: "photo", Data: solidPNG(4, 4, color.NRGBA{A: 255}), Caption: "what is this?"})
	b.processMessage(context.Background(), f.api, msg, privateKey(9))
	if runner.turns() != 0 {
		t.Fatalf("a photo ran a turn on a model that does not read images: %q", runner.prompts)
	}
	replies := f.repliesTo(9, msg.MessageID)
	if len(replies) != 1 || !strings.Contains(replies[0].Text, "/model") {
		t.Fatalf("the person was not told to pick another model: %+v", replies)
	}
	if n := len(f.fake.Calls("getFile")); n != 0 {
		t.Fatalf("the file was downloaded for nothing (%d getFile calls)", n)
	}
	if _, ok := logs.find("telegram: update refused", map[string]any{"reason": "model does not read images", "model": "stub/model"}); !ok {
		t.Fatalf("the refusal was not logged:\n%s", logs.raw())
	}
}

func TestFileOverTheDownloadLimitIsRefused(t *testing.T) {
	logs := &logRecords{}
	b, runner, f := mediaBot(t, true, logs)
	msg := mustMedia(t, f, tgfake.IncomingMedia{IncomingMessage: tgfake.IncomingMessage{ChatID: 9, UserID: 9}, Type: "document", Data: []byte("x"), FileName: "huge.zip"})
	msg.Document.FileSize = maxInboundFileBytes + 1
	b.processMessage(context.Background(), f.api, msg, privateKey(9))
	if runner.turns() != 0 {
		t.Fatal("a file over the limit ran a turn")
	}
	if replies := f.repliesTo(9, msg.MessageID); len(replies) != 1 || !strings.Contains(replies[0].Text, "20 MB") {
		t.Fatalf("the person was not told the limit: %+v", replies)
	}
	if n := len(f.fake.Calls("getFile")); n != 0 {
		t.Fatalf("the bot asked for a file it cannot download (%d getFile calls)", n)
	}
	if _, ok := logs.find("telegram: update refused", map[string]any{"reason": "file too large"}); !ok {
		t.Fatalf("the refusal was not logged:\n%s", logs.raw())
	}
}

// A download that fails is said in the chat, and the bot's token, which is
// part of the file's address, never reaches the log or the chat.
func TestDownloadFailureIsAnswered(t *testing.T) {
	logs := &logRecords{}
	b, runner, f := mediaBot(t, true, logs)
	msg := mustMedia(t, f, tgfake.IncomingMedia{IncomingMessage: tgfake.IncomingMessage{ChatID: 9, UserID: 9}, Type: "document", Data: []byte("notes"), FileName: "notes.txt"})
	f.fake.SetFault(tgfake.Fault{Method: "getFile", Code: http.StatusBadRequest, Description: "Bad Request: wrong file_id or the file is temporarily unavailable", Times: 1})
	b.processMessage(context.Background(), f.api, msg, privateKey(9))
	if runner.turns() != 0 {
		t.Fatal("a file that did not download ran a turn")
	}
	replies := f.repliesTo(9, msg.MessageID)
	if len(replies) != 1 || !strings.Contains(replies[0].Text, "notes.txt") {
		t.Fatalf("the failure was not said in the chat: %+v", replies)
	}
	if _, ok := logs.find("telegram: file download failed", map[string]any{"file": "notes.txt"}); !ok {
		t.Fatalf("the failure was not logged:\n%s", logs.raw())
	}

	// The download itself failing, refused or unreachable: the address
	// carries the token, and so does the error of a request that failed.
	refusing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "gone", http.StatusBadGateway)
	}))
	t.Cleanup(refusing.Close)
	closed := httptest.NewServer(http.NotFoundHandler())
	closed.Close()
	for _, base := range []string{refusing.URL, closed.URL} {
		b.apiBase = base
		b.processMessage(context.Background(), f.api, msg, privateKey(9))
	}
	if runner.turns() != 0 {
		t.Fatal("a file that did not download ran a turn")
	}
	if replies := f.repliesTo(9, msg.MessageID); len(replies) != 3 {
		t.Fatalf("%d answers to three failed downloads, want 3: %+v", len(replies), replies)
	}
	for _, m := range f.fake.Chat(9).Messages {
		if strings.Contains(m.Text, fakeToken) {
			t.Fatalf("the chat shows the bot's token: %q", m.Text)
		}
	}
	if strings.Contains(logs.raw(), fakeToken) {
		t.Fatalf("the log carries the bot's token:\n%s", logs.raw())
	}
}

// The largest size of a photo reaches the session as a picture, the caption
// as the prompt, the way a web UI attachment does.
func TestPhotoAndCaptionReachTheSession(t *testing.T) {
	b, runner, f := mediaBot(t, true, nil)
	pic := solidPNG(200, 100, color.NRGBA{R: 200, A: 255})
	msg := mustMedia(t, f, tgfake.IncomingMedia{IncomingMessage: tgfake.IncomingMessage{ChatID: 9, UserID: 9}, Type: "photo", Data: pic, Caption: "what is this?"})
	if len(msg.Photo) < 2 {
		t.Fatalf("the fake delivered %d photo sizes, the test needs a thumbnail and the photo", len(msg.Photo))
	}
	b.processMessage(context.Background(), f.api, msg, privateKey(9))
	if runner.turns() != 1 || runner.prompts[0] != "what is this?" {
		t.Fatalf("prompts = %q, want the caption", runner.prompts)
	}
	if len(runner.images[0]) != 1 {
		t.Fatalf("the turn carried %d files, want the photo", len(runner.images[0]))
	}
	mime, data := dataURLBytes(t, runner.images[0][0].DataURL)
	if mime != "image/png" || !bytes.Equal(data, pic) {
		t.Fatalf("the session got a %s of %d bytes, want the full PNG", mime, len(data))
	}
	if name := runner.images[0][0].Name; !strings.HasPrefix(name, "photo_") || !strings.HasSuffix(name, ".png") {
		t.Fatalf("the photo is named %q", name)
	}
}

// A photo sent without a caption still runs a turn: the picture is the
// question.
func TestPhotoWithoutACaptionRunsATurn(t *testing.T) {
	b, runner, f := mediaBot(t, true, nil)
	msg := mustMedia(t, f, tgfake.IncomingMedia{IncomingMessage: tgfake.IncomingMessage{ChatID: 9, UserID: 9}, Type: "photo", Data: solidPNG(4, 4, color.NRGBA{A: 255})})
	b.processMessage(context.Background(), f.api, msg, privateKey(9))
	if runner.turns() != 1 || runner.prompts[0] != "" || len(runner.images[0]) != 1 {
		t.Fatalf("prompts = %q with %d files, want an empty prompt and the photo", runner.prompts, len(runner.images))
	}
}

// A picture sent as a file is a picture to the model, whatever type the
// client declared; any other document keeps the type it came with.
func TestDocumentTypes(t *testing.T) {
	b, runner, f := mediaBot(t, true, nil)
	pic := solidPNG(4, 4, color.NRGBA{G: 200, A: 255})
	for _, in := range []tgfake.IncomingMedia{
		{Type: "document", Data: pic, FileName: "shot.png", MimeType: "application/octet-stream"},
		{Type: "document", Data: []byte("a,b\n1,2\n"), FileName: "table.csv", MimeType: "text/csv"},
	} {
		in.ChatID, in.UserID = 9, 9
		b.processMessage(context.Background(), f.api, mustMedia(t, f, in), privateKey(9))
	}
	if runner.turns() != 2 {
		t.Fatalf("%d turns, want 2", runner.turns())
	}
	if mime, data := dataURLBytes(t, runner.images[0][0].DataURL); mime != "image/png" || !bytes.Equal(data, pic) || runner.images[0][0].Name != "shot.png" {
		t.Fatalf("the picture sent as a file reached the session as %s named %q", mime, runner.images[0][0].Name)
	}
	if mime, data := dataURLBytes(t, runner.images[1][0].DataURL); mime != "text/csv" || string(data) != "a,b\n1,2\n" || runner.images[1][0].Name != "table.csv" {
		t.Fatalf("the table reached the session as %s %q named %q", mime, data, runner.images[1][0].Name)
	}
}

// A photo sent as a reply asks about the message it answers, as a text does.
func TestPhotoReplyQuotesTheMessage(t *testing.T) {
	b, runner, f := mediaBot(t, true, nil)
	earlier := f.userMessage(9, 9, "the old design")
	msg := mustMedia(t, f, tgfake.IncomingMedia{IncomingMessage: tgfake.IncomingMessage{ChatID: 9, UserID: 9, ReplyToMessageID: earlier.MessageID}, Type: "photo", Data: solidPNG(4, 4, color.NRGBA{A: 255}), Caption: "and the new one"})
	b.processMessage(context.Background(), f.api, msg, privateKey(9))
	author, quoted := replyContext(msg.ReplyToMessage)
	if want := replyquote.Prompt(author, quoted, "and the new one"); runner.turns() != 1 || runner.prompts[0] != want {
		t.Fatalf("prompts = %q, want %q", runner.prompts, want)
	}
}

// In a group a photo is for the bot when its caption mentions the bot.
func TestGroupPhotoNeedsAMentionInTheCaption(t *testing.T) {
	logs := &logRecords{}
	b, runner, f := mediaBot(t, true, logs)
	pic := solidPNG(4, 4, color.NRGBA{A: 255})
	group := tgfake.IncomingMessage{ChatID: -100, ChatType: "group", UserID: 9}
	b.dispatch(context.Background(), f.api, mustMedia(t, f, tgfake.IncomingMedia{IncomingMessage: group, Type: "photo", Data: pic, Caption: "look"}))
	if _, ok := logs.find("telegram: update ignored", map[string]any{"reason": "not addressed to the bot"}); !ok {
		t.Fatalf("an unaddressed group photo was not ignored:\n%s", logs.raw())
	}
	group.Mention = true
	b.dispatch(context.Background(), f.api, mustMedia(t, f, tgfake.IncomingMedia{IncomingMessage: group, Type: "photo", Data: pic, Caption: "look"}))
	waitFor(t, "the addressed photo's turn", func() bool { return runner.turns() == 1 })
	if runner.prompts[0] != "look" {
		t.Fatalf("prompt = %q, want the caption without the mention", runner.prompts[0])
	}
}

// sendAlbum dispatches n photos of one album the way the poll does, the
// caption on item captionAt.
func sendAlbum(t *testing.T, b *Bot, f *fakeAPI, base tgfake.IncomingMessage, n, captionAt int, caption string) [][]byte {
	t.Helper()
	var sent [][]byte
	for i := 0; i < n; i++ {
		pic := solidPNG(4+i, 4, color.NRGBA{R: uint8(20 * i), A: 255})
		sent = append(sent, pic)
		in := tgfake.IncomingMedia{IncomingMessage: base, Type: "photo", Data: pic, MediaGroupID: "album-1"}
		if i == captionAt {
			in.Caption = caption
		} else {
			in.Mention = false
		}
		b.dispatch(context.Background(), f.api, mustMedia(t, f, in))
	}
	return sent
}

func checkAlbumTurn(t *testing.T, runner *scriptedRunner, prompt string, sent [][]byte) {
	t.Helper()
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if len(runner.prompts) != 1 || runner.prompts[0] != prompt {
		t.Fatalf("prompts = %q, want one turn asking %q", runner.prompts, prompt)
	}
	if len(runner.images[0]) != len(sent) {
		t.Fatalf("the album turn carried %d pictures, want %d", len(runner.images[0]), len(sent))
	}
	for i, part := range runner.images[0] {
		if _, data := dataURLBytes(t, part.DataURL); !bytes.Equal(data, sent[i]) {
			t.Fatalf("picture %d of the turn is not item %d of the album", i, i)
		}
	}
}

// An album is one turn with every picture in the order they were sent and
// the caption whichever item carries it.
func TestAlbumIsOneTurn(t *testing.T) {
	b, runner, f := mediaBot(t, true, nil)
	sent := sendAlbum(t, b, f, tgfake.IncomingMessage{ChatID: 9, UserID: 9}, 3, 1, "compare them")
	waitFor(t, "the album's turn", func() bool { return runner.turns() == 1 })
	time.Sleep(3 * b.albumWait)
	checkAlbumTurn(t, runner, "compare them", sent)
}

// In a group the mention is on one item only, and it addresses the album.
func TestGroupAlbumIsAddressedByTheCaptionsMention(t *testing.T) {
	b, runner, f := mediaBot(t, true, nil)
	sent := sendAlbum(t, b, f, tgfake.IncomingMessage{ChatID: -100, ChatType: "group", UserID: 9, Mention: true}, 3, 0, "compare them")
	waitFor(t, "the album's turn", func() bool { return runner.turns() == 1 })
	time.Sleep(3 * b.albumWait)
	checkAlbumTurn(t, runner, "compare them", sent)
}

// Telegram puts at most ten items in an album: the tenth does not wait.
func TestAlbumOfTenRunsWithoutWaiting(t *testing.T) {
	b, runner, f := mediaBot(t, true, nil)
	b.albumWait = time.Hour
	sent := sendAlbum(t, b, f, tgfake.IncomingMessage{ChatID: 9, UserID: 9}, maxAlbumItems, 0, "all of them")
	waitFor(t, "the album's turn", func() bool { return runner.turns() == 1 })
	checkAlbumTurn(t, runner, "all of them", sent)
}

// A stop waits for an album still being collected: Telegram confirmed its
// updates, and the turn is owed.
func TestDrainWaitsForAnAlbumBeingCollected(t *testing.T) {
	b, runner, f := mediaBot(t, true, nil)
	sendAlbum(t, b, f, tgfake.IncomingMessage{ChatID: 9, UserID: 9}, 2, 0, "both")
	b.drain()
	if runner.turns() != 1 {
		t.Fatalf("the stop returned before the album's turn ran (%d turns)", runner.turns())
	}
}

// roundTripFunc is an http.RoundTripper made of a function.
type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// A getFile that fails on the way returns the *url.Error of
// /bot<token>/getFile: the token stays out of the log and the chat.
func TestGetFileTransportFailureKeepsTheTokenOut(t *testing.T) {
	logs := &logRecords{}
	b, runner, f := mediaBot(t, true, logs)
	msg := mustMedia(t, f, tgfake.IncomingMedia{IncomingMessage: tgfake.IncomingMessage{ChatID: 9, UserID: 9}, Type: "document", Data: []byte("notes"), FileName: "notes.txt"})
	api := *f.api
	api.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/getFile") {
			return nil, errors.New("connection reset by peer")
		}
		return http.DefaultTransport.RoundTrip(r)
	})}
	b.processMessage(context.Background(), &api, msg, privateKey(9))
	if runner.turns() != 0 {
		t.Fatal("a file that did not download ran a turn")
	}
	if replies := f.repliesTo(9, msg.MessageID); len(replies) != 1 {
		t.Fatalf("the failure was not said in the chat: %+v", replies)
	}
	if _, ok := logs.find("telegram: file download failed", map[string]any{"file": "notes.txt"}); !ok {
		t.Fatalf("the failure was not logged:\n%s", logs.raw())
	}
	if strings.Contains(logs.raw(), fakeToken) {
		t.Fatalf("the log carries the bot's token:\n%s", logs.raw())
	}
}

// The client every Bot API request of the bot goes through takes the token
// out of a failed request's error, whatever the adapter then logs it as.
func TestRedactingClientTakesTheTokenOut(t *testing.T) {
	c := redactingClient{token: "123:SECRET", inner: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("dial tcp: connection refused")
	})}}
	req, _ := http.NewRequest(http.MethodPost, "https://api.telegram.org/bot123:SECRET/setMyCommands", nil)
	_, err := c.Do(req)
	var ue *url.Error
	if err == nil || !errors.As(err, &ue) {
		t.Fatalf("err = %v, want the *url.Error kept", err)
	}
	if strings.Contains(err.Error(), "SECRET") || !strings.Contains(err.Error(), "/bot<token>/setMyCommands") {
		t.Fatalf("err = %q, want the token replaced", err)
	}

	// A redirect the client cannot follow names its target in the wrapped
	// error; a timeout stays a timeout.
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Location", "/file/bot123:SECRET/%zz")
		w.WriteHeader(http.StatusFound)
	}))
	defer redirect.Close()
	c = redactingClient{token: "123:SECRET", inner: &http.Client{}}
	req, _ = http.NewRequest(http.MethodGet, redirect.URL+"/file/bot123:SECRET/documents/a.txt", nil)
	if _, err = c.Do(req); err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("a bad redirect: %v, want an error without the token", err)
	}
	slow := redactingClient{token: "123:SECRET", inner: &http.Client{}}
	req, _ = http.NewRequest(http.MethodGet, redirect.URL+"/bot123:SECRET/getMe", nil)
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	req = req.WithContext(ctx)
	_, err = slow.Do(req)
	var timeout interface{ Timeout() bool }
	if err == nil || strings.Contains(err.Error(), "SECRET") || !errors.As(err, &timeout) || !timeout.Timeout() {
		t.Fatalf("a timeout: %v, want a timeout without the token", err)
	}
}

// A question asked right after an album comes after it: the album still
// collecting its items is routed first, so the turns run in the order the
// person sent them.
func TestMessageAfterAnAlbumWaitsForIt(t *testing.T) {
	b, runner, f := mediaBot(t, true, nil)
	b.albumWait = time.Hour // only the next message can route the album
	sent := sendAlbum(t, b, f, tgfake.IncomingMessage{ChatID: 9, UserID: 9}, 2, -1, "")
	b.dispatch(context.Background(), f.api, f.userMessage(9, 9, "which one is brighter?"))
	waitFor(t, "both turns", func() bool { return runner.turns() == 2 })
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if runner.prompts[0] != "" || len(runner.images[0]) != len(sent) || runner.prompts[1] != "which one is brighter?" || len(runner.images[1]) != 0 {
		t.Fatalf("turns %q with %d and %d files, want the album first, then the question", runner.prompts, len(runner.images[0]), len(runner.images[1]))
	}
}

// Two albums sent one after the other run in that order.
func TestAlbumsRunInTheOrderSent(t *testing.T) {
	b, runner, f := mediaBot(t, true, nil)
	b.albumWait = time.Hour
	first := sendAlbum(t, b, f, tgfake.IncomingMessage{ChatID: 9, UserID: 9}, 2, 0, "first")
	for i := 0; i < 2; i++ {
		in := tgfake.IncomingMedia{IncomingMessage: tgfake.IncomingMessage{ChatID: 9, UserID: 9}, Type: "photo", Data: solidPNG(5, 5, color.NRGBA{B: 200, A: 255}), MediaGroupID: "album-2"}
		if i == 0 {
			in.Caption = "second"
		}
		b.dispatch(context.Background(), f.api, mustMedia(t, f, in))
	}
	waitFor(t, "the first album", func() bool { return runner.turns() == 1 })
	b.dispatch(context.Background(), f.api, f.userMessage(9, 9, "done"))
	waitFor(t, "every turn", func() bool { return runner.turns() == 3 })
	runner.mu.Lock()
	defer runner.mu.Unlock()
	if got := strings.Join(runner.prompts, "|"); got != "first|second|done" || len(runner.images[0]) != len(first) || len(runner.images[1]) != 2 {
		t.Fatalf("turns %q, want first|second|done", runner.prompts)
	}
}

// A command in a caption is a command: Telegram marks it in the caption's
// entities, and the caption is what the person wrote.
func TestCommandInACaptionIsACommand(t *testing.T) {
	b, runner, f := mediaBot(t, true, nil)
	key := privateKey(9)
	pic := solidPNG(4, 4, color.NRGBA{A: 255})
	before := b.store.Get(key)
	msg := mustMedia(t, f, tgfake.IncomingMedia{IncomingMessage: tgfake.IncomingMessage{ChatID: 9, UserID: 9}, Type: "photo", Data: pic, Caption: "/clear"})
	b.processMessage(context.Background(), f.api, msg, key)
	if runner.turns() != 0 || b.store.Get(key) == before {
		t.Fatalf("a photo captioned /clear ran %d turns and kept the session", runner.turns())
	}
	if replies := f.repliesTo(9, msg.MessageID); len(replies) != 1 || !strings.Contains(replies[0].Text, "New session") {
		t.Fatalf("/clear in a caption was not answered as the command: %+v", replies)
	}
	msg = mustMedia(t, f, tgfake.IncomingMedia{IncomingMessage: tgfake.IncomingMessage{ChatID: 9, UserID: 9}, Type: "photo", Data: pic, Caption: "/plan --once look at it"})
	b.processMessage(context.Background(), f.api, msg, key)
	if runner.turns() != 1 || runner.prompts[0] != "/plan --once look at it" || len(runner.images[0]) != 1 {
		t.Fatalf("a settings command in a caption: %q with %d files, want it with the photo", runner.prompts, len(runner.images))
	}
}

// Telegram lets every item of an album carry a caption: all of them are
// read, in the order the items were sent.
func TestEveryCaptionOfAnAlbumIsRead(t *testing.T) {
	b, runner, f := mediaBot(t, true, nil)
	for i, caption := range []string{"the old logo", "", "the new one"} {
		in := tgfake.IncomingMedia{IncomingMessage: tgfake.IncomingMessage{ChatID: 9, UserID: 9}, Type: "photo", Data: solidPNG(4+i, 4, color.NRGBA{A: 255}), MediaGroupID: "album-3", Caption: caption}
		b.dispatch(context.Background(), f.api, mustMedia(t, f, in))
	}
	waitFor(t, "the album's turn", func() bool { return runner.turns() == 1 })
	if want := "the old logo\n\nthe new one"; runner.prompts[0] != want {
		t.Fatalf("prompt = %q, want %q", runner.prompts[0], want)
	}
}

func TestUnsupportedMediaNoteArticle(t *testing.T) {
	for kind, want := range map[string]string{"animation": "an animation", "audio file": "an audio file", "video": "a video", "sticker": "a sticker"} {
		if note := unsupportedMediaNote(kind); !strings.Contains(note, "take "+want+" here") {
			t.Errorf("unsupportedMediaNote(%q) = %q, want %q", kind, note, want)
		}
	}
}

// The type a client declares goes into a data URL: a malformed one is
// replaced by what the bytes look like.
func TestFileTypeTakesOnlyAWellFormedDeclaredType(t *testing.T) {
	pic := solidPNG(2, 2, color.NRGBA{A: 255})
	for _, tc := range []struct {
		declared string
		data     []byte
		want     string
	}{
		{"application/octet-stream", pic, "image/png"},
		{"text/csv; charset=utf-8", []byte("a,b"), "text/csv"},
		{"text/csv,evil;base64", []byte("a,b"), "text/plain"},
		{"application/octet-stream", []byte("%PDF-1.4\n"), "application/pdf"},
		{"", []byte("%PDF-1.4\n"), "application/pdf"},
		{"image/png", []byte("not a picture"), "text/plain"},
	} {
		if got := fileType(tc.declared, tc.data); got != tc.want {
			t.Errorf("fileType(%q) = %q, want %q", tc.declared, got, tc.want)
		}
	}
}

// A self-hosted Bot API server started with --local answers getFile with a
// path on its own disk: the bot says it could not download the file and
// logs why, rather than asking the server for an address it does not serve.
func TestLocalModeServerPathIsNotDownloaded(t *testing.T) {
	logs := &logRecords{}
	b, runner, f := mediaBot(t, true, logs)
	msg := mustMedia(t, f, tgfake.IncomingMedia{IncomingMessage: tgfake.IncomingMessage{ChatID: 9, UserID: 9}, Type: "document", Data: []byte("notes"), FileName: "notes.txt"})
	api := *f.api
	api.Client = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if strings.HasSuffix(r.URL.Path, "/getFile") {
			body := `{"ok":true,"result":{"file_id":"x","file_unique_id":"ux","file_size":5,"file_path":"/var/lib/telegram-bot-api/123/documents/file_1.txt"}}`
			return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
		}
		if strings.HasPrefix(r.URL.Path, "/file/") {
			t.Errorf("the bot asked for %s", r.URL.Path)
		}
		return http.DefaultTransport.RoundTrip(r)
	})}
	b.processMessage(context.Background(), &api, msg, privateKey(9))
	if runner.turns() != 0 || len(f.repliesTo(9, msg.MessageID)) != 1 {
		t.Fatalf("turns %d, answers %d: want no turn and one answer", runner.turns(), len(f.repliesTo(9, msg.MessageID)))
	}
	if rec, ok := logs.find("telegram: file download failed", map[string]any{"file": "notes.txt"}); !ok || !strings.Contains(rec["err"].(string), "--local") {
		t.Fatalf("the log does not say why:\n%s", logs.raw())
	}
}
