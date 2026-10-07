//go:build gateway || gateway.telegram

package telegram

// The pictures a call showed the model, sent into the chat. The happy path is
// features/gateway_telegram_image_preview.feature; these are the edges.

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tgfake "github.com/EvilFreelancer/tgfake/pkg/server"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

type fixedSession struct{ id, dir string }

func (f fixedSession) GetID() string                  { return f.id }
func (f fixedSession) GetPersistedSessionDir() string { return f.dir }

// savedPicture writes a PNG into the session's assets the way the agent keeps
// its copy, and returns the update that announces it.
func savedPicture(t *testing.T, sessionDir, asset string) ([]byte, acp.ToolCallStatusUpdate) {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 3, 2))
	img.Set(0, 0, color.NRGBA{B: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	dir := session.AssetsPath(sessionDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, asset), buf.Bytes(), 0o444); err != nil {
		t.Fatal(err)
	}
	update := acp.ToolCallStatusUpdate{
		SessionUpdate: acp.UpdateTypeToolCallUpdate,
		ToolCallID:    "r1",
		Status:        "completed",
		Content:       []acp.ToolCallResultItem{{Type: "content", Content: acp.ContentBlock{Type: acp.ContentTypeText, Text: "shot.png: PNG image"}}},
		Meta:          session.ToolImagesMeta(nil, []session.ToolImage{{Name: "shot.png", MIMEType: "image/png", Asset: asset}}),
	}
	return buf.Bytes(), update
}

func TestPicturesOfAnotherSessionStayOutOfTheChat(t *testing.T) {
	f := newFakeAPI(t, tgfake.Options{})
	dir := t.TempDir()
	_, update := savedPicture(t, dir, "shot-1a.png")
	s := newSender(f.api, 5, 0, slog.Default(), richConfig{})
	s.pictures = fixedSession{id: "sess_chat", dir: dir}

	// A subagent's read belongs to its own session.
	_ = s.SendSessionUpdate("sess_child", update)
	// A call still running has shown nothing yet.
	running := update
	running.Status = "in_progress"
	_ = s.SendSessionUpdate("sess_chat", running)

	if calls := f.fake.Calls("sendPhoto"); len(calls) != 0 {
		t.Fatalf("sent %d photos, want none", len(calls))
	}
}

func TestAPictureRefusedAsAPhotoGoesAsADocument(t *testing.T) {
	f := newFakeAPI(t, tgfake.Options{})
	f.fake.SetFault(tgfake.Fault{Method: "sendPhoto", Code: http.StatusBadRequest, Description: "Bad Request: PHOTO_INVALID_DIMENSIONS", Times: 1})
	dir := t.TempDir()
	data, update := savedPicture(t, dir, "shot-1a.png")
	s := newSender(f.api, 5, 0, slog.Default(), richConfig{})
	s.pictures = fixedSession{id: "sess_chat", dir: dir}

	_ = s.SendSessionUpdate("sess_chat", update)

	msgs := f.fake.Chat(5).Messages
	if len(msgs) != 1 || msgs[0].Document == nil || msgs[0].Caption != "shot.png" {
		t.Fatalf("chat = %+v, want the picture as a document captioned shot.png", msgs)
	}
	if got, ok := f.fake.File(msgs[0].Document.FileID); !ok || !bytes.Equal(got, data) {
		t.Error("the document is not the saved picture")
	}
}

func TestAPictureNamedOutsideTheAssetsIsNotSent(t *testing.T) {
	f := newFakeAPI(t, tgfake.Options{})
	dir := t.TempDir()
	_, update := savedPicture(t, dir, "shot-1a.png")
	update.Meta = session.ToolImagesMeta(nil, []session.ToolImage{
		{Name: "passwd", Asset: "../../etc/passwd"},
		{Name: "gone.png", Asset: "gone-2b.png"},
	})
	s := newSender(f.api, 5, 0, slog.Default(), richConfig{})
	s.pictures = fixedSession{id: "sess_chat", dir: dir}

	_ = s.SendSessionUpdate("sess_chat", update)

	if calls := len(f.fake.Calls("sendPhoto")) + len(f.fake.Calls("sendDocument")); calls != 0 {
		t.Fatalf("sent %d files, want none", calls)
	}
}

// Without Rich Messages the answer grows in one live message that was sent
// before the call ran. A picture posted then would stand under the finished
// answer, so the live message moves below it: the old one is removed and the
// answer comes as a new reply to the person's message, after the photo.
func TestTheLiveMessageMovesBelowAPictureItWouldOtherwiseAnswerFromAbove(t *testing.T) {
	f := newFakeAPI(t, tgfake.Options{})
	asked := f.userMessage(5, 5, "look at it")
	dir := t.TempDir()
	_, update := savedPicture(t, dir, "shot-1a.png")
	s := newSender(f.api, 5, asked.MessageID, slog.Default(), richConfig{})
	s.pictures = fixedSession{id: "sess_chat", dir: dir}

	_ = s.SendSessionUpdate("sess_chat", chunk("Let me look."))
	_ = s.SendSessionUpdate("sess_chat", update)
	_ = s.SendSessionUpdate("sess_chat", chunk(" It is blue."))
	s.Flush()

	var shown []tgfake.MessageView
	for _, m := range f.fake.Chat(5).Messages {
		if !m.Deleted {
			shown = append(shown, m)
		}
	}
	if len(shown) != 3 || shown[1].Photo == nil || shown[2].From != "bot" || shown[2].Text != "Let me look. It is blue." {
		t.Fatalf("chat = %+v, want the question, the photo, then the whole answer", shown)
	}
	if shown[2].ReplyToMessageID != asked.MessageID {
		t.Errorf("the answer replies to %d, want the person's message %d", shown[2].ReplyToMessageID, asked.MessageID)
	}
}

// Telegram may refuse to delete the live message (too old, already gone); the
// answer still comes below the photo, as a new reply, rather than as an edit
// of the message above it.
func TestTheAnswerComesBelowThePictureWhenTheLiveMessageCannotBeDeleted(t *testing.T) {
	f := newFakeAPI(t, tgfake.Options{})
	f.fake.SetFault(tgfake.Fault{Method: "deleteMessage", Code: http.StatusBadRequest, Description: "Bad Request: message can't be deleted", Times: 1})
	asked := f.userMessage(5, 5, "look at it")
	dir := t.TempDir()
	_, update := savedPicture(t, dir, "shot-1a.png")
	s := newSender(f.api, 5, asked.MessageID, slog.Default(), richConfig{})
	s.pictures = fixedSession{id: "sess_chat", dir: dir}

	_ = s.SendSessionUpdate("sess_chat", chunk("Let me look."))
	_ = s.SendSessionUpdate("sess_chat", update)
	s.Flush()

	msgs := f.fake.Chat(5).Messages
	photoAt, answerAt := -1, -1
	for i, m := range msgs {
		if m.Photo != nil {
			photoAt = i
		}
		if m.From == "bot" && !m.Deleted && strings.Contains(m.Text, "Let me look.") && !strings.HasSuffix(m.Text, "…") {
			answerAt = i
		}
	}
	if photoAt < 0 || answerAt < photoAt {
		t.Fatalf("chat = %+v, want the finished answer below the photo", msgs)
	}
	if msgs[answerAt].ReplyToMessageID != asked.MessageID {
		t.Errorf("the answer replies to %d, want %d", msgs[answerAt].ReplyToMessageID, asked.MessageID)
	}
}

// Only a refusal of the photo itself (a 400: its size, its shape) is worth
// sending the file again as a document; a rate limit or a server error is
// not, and would only double the traffic.
func TestAPictureIsNotResentAsADocumentAfterARateLimit(t *testing.T) {
	f := newFakeAPI(t, tgfake.Options{})
	f.fake.SetFault(tgfake.Fault{Method: "sendPhoto", Code: http.StatusTooManyRequests, Description: "Too Many Requests: retry after 3", RetryAfter: 3, Times: 1})
	dir := t.TempDir()
	_, update := savedPicture(t, dir, "shot-1a.png")
	s := newSender(f.api, 5, 0, slog.Default(), richConfig{})
	s.pictures = fixedSession{id: "sess_chat", dir: dir}

	_ = s.SendSessionUpdate("sess_chat", update)

	if calls := f.fake.Calls("sendDocument"); len(calls) != 0 {
		t.Fatalf("the picture was sent again as a document after a 429: %+v", calls)
	}
}

// A Bad Request about something other than the photo (the chat is gone, the
// thread closed) meets the document the same way, so the file is not sent
// again; a refusal of photos in the chat is about the photo, and a document
// may still be allowed there.
func TestAPictureIsResentAsADocumentOnlyWhenThePhotoWasRefused(t *testing.T) {
	for _, tc := range []struct {
		description string
		resent      bool
	}{
		{"Bad Request: chat not found", false},
		{"Bad Request: message thread not found", false},
		{"Bad Request: IMAGE_PROCESS_FAILED", true},
		{"Bad Request: not enough rights to send photos to the chat", true},
	} {
		f := newFakeAPI(t, tgfake.Options{})
		f.fake.SetFault(tgfake.Fault{Method: "sendPhoto", Code: http.StatusBadRequest, Description: tc.description, Times: 1})
		dir := t.TempDir()
		_, update := savedPicture(t, dir, "shot-1a.png")
		s := newSender(f.api, 5, 0, slog.Default(), richConfig{})
		s.pictures = fixedSession{id: "sess_chat", dir: dir}

		_ = s.SendSessionUpdate("sess_chat", update)

		if resent := len(f.fake.Calls("sendDocument")) > 0; resent != tc.resent {
			t.Errorf("%q: sent again as a document = %v, want %v", tc.description, resent, tc.resent)
		}
	}
}

// The bytes are checked, not only the name: an asset that is not a picture
// (replaced after the call, damaged) is not sent, not even as a document.
func TestAnAssetThatIsNotAPictureIsNotSent(t *testing.T) {
	f := newFakeAPI(t, tgfake.Options{})
	dir := t.TempDir()
	_, update := savedPicture(t, dir, "shot-1a.png")
	path := filepath.Join(session.AssetsPath(dir), "shot-1a.png")
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("-----BEGIN PRIVATE KEY-----"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newSender(f.api, 5, 0, slog.Default(), richConfig{})
	s.pictures = fixedSession{id: "sess_chat", dir: dir}

	_ = s.SendSessionUpdate("sess_chat", update)

	if calls := len(f.fake.Calls("sendPhoto")) + len(f.fake.Calls("sendDocument")); calls != 0 {
		t.Fatalf("sent %d files for an asset that is not a picture", calls)
	}
}
