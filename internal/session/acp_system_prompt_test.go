package session

import (
	"context"
	"errors"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// The system prompt a client sends is append-only plain text. Every refusal is
// invalid params, the shape of the request being what the client got wrong.
func TestValidateACPSystemPrompt(t *testing.T) {
	text := func(s string) acp.ContentBlock {
		return acp.ContentBlock{Type: acp.ContentTypeText, Text: s}
	}
	cases := []struct {
		name    string
		prompt  *acp.SystemPrompt
		want    string
		wantErr int
	}{
		{name: "no prompt", prompt: nil},
		{
			name:   "append",
			prompt: &acp.SystemPrompt{Mode: "append", Content: []acp.ContentBlock{text("Be a reviewer.")}},
			want:   "Be a reviewer.",
		},
		{
			name: "several blocks",
			prompt: &acp.SystemPrompt{Mode: "append", Content: []acp.ContentBlock{
				text("Be a reviewer."), text("Focus on security."),
			}},
			want: "Be a reviewer.\n\nFocus on security.",
		},
		{
			name:    "override is declined",
			prompt:  &acp.SystemPrompt{Mode: "override", Content: []acp.ContentBlock{text("x")}},
			wantErr: acp.ErrInvalidParams,
		},
		{
			name:    "no mode is not append",
			prompt:  &acp.SystemPrompt{Content: []acp.ContentBlock{text("x")}},
			wantErr: acp.ErrInvalidParams,
		},
		{
			name:    "empty content",
			prompt:  &acp.SystemPrompt{Mode: "append"},
			wantErr: acp.ErrInvalidParams,
		},
		{
			name:    "blank text only",
			prompt:  &acp.SystemPrompt{Mode: "append", Content: []acp.ContentBlock{text("   ")}},
			wantErr: acp.ErrInvalidParams,
		},
		{
			name: "a picture is refused",
			prompt: &acp.SystemPrompt{Mode: "append", Content: []acp.ContentBlock{
				{Type: "image"},
			}},
			wantErr: acp.ErrInvalidParams,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := validateACPSystemPrompt(tc.prompt)
			if tc.wantErr != 0 {
				if err == nil {
					t.Fatalf("no error, want code %d", tc.wantErr)
				}
				var coded *acp.CodedError
				if !errors.As(err, &coded) || coded.Code != tc.wantErr {
					t.Fatalf("error = %v, want code %d", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("text = %q, want %q", got, tc.want)
			}
		})
	}
}

// The block a client sent is stored with the session, so a client that reopens
// it without resending the instructions keeps them.
func TestACPSystemPromptIsKeptWithTheSession(t *testing.T) {
	mgr, fs := newTestManager(t)
	id := "sess_prompt"
	dir, err := fs.EnsureLayout(id)
	if err != nil {
		t.Fatal(err)
	}
	st := &State{ID: id, CWD: t.TempDir(), Mode: ModeAgent, SessionDir: dir}
	st.SetACPSystemPromptWithoutPersist("Be a reviewer.")
	if err := fs.Save(st); err != nil {
		t.Fatalf("save: %v", err)
	}
	snap, err := fs.ReadSnapshot(id)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if snap.Meta.ACPSystemPrompt != "Be a reviewer." {
		t.Fatalf("stored prompt = %q, want the client's text", snap.Meta.ACPSystemPrompt)
	}
	// And the state a restore builds carries it again.
	restored := &State{ID: id, CWD: st.CWD, Mode: ModeAgent, SessionDir: dir}
	restored.SetACPSystemPromptWithoutPersist(snap.Meta.ACPSystemPrompt)
	if got := restored.GetACPSystemPrompt(); got != "Be a reviewer." {
		t.Fatalf("restored prompt = %q", got)
	}
	_ = mgr
}

// fakeNoticeSender is a sender that answers SupportsNotices as the test wants
// and records what it was sent.
type fakeNoticeSender struct {
	notices bool
	sent    []interface{}
}

func (f *fakeNoticeSender) SendSessionUpdate(_ string, update interface{}) error {
	f.sent = append(f.sent, update)
	return nil
}

func (f *fakeNoticeSender) RequestPermission(context.Context, acp.PermissionRequestParams) (*acp.PermissionResult, error) {
	return &acp.PermissionResult{}, nil
}

func (f *fakeNoticeSender) RequestQuestion(context.Context, acp.QuestionRequestParams) (*acp.QuestionResult, error) {
	return &acp.QuestionResult{}, nil
}

func (f *fakeNoticeSender) SupportsNotices() bool { return f.notices }

// plainSender is a surface that reads the Coddy updates only (the HTTP relay,
// a remote console): it implements no notice capability.
type plainSender struct{ fakeNoticeSender }

// A client that renders the standard notice is sent one on its own; a surface
// that does not keeps reading the notice field of the Coddy update.
func TestSendNoticeGoesOnlyToAClientThatRendersThem(t *testing.T) {
	capable := &fakeNoticeSender{notices: true}
	if !sendNotice(capable, "sess_1", acp.NoticeSeverityInfo, "Model: qwen3.8-27b for this session", "") {
		t.Fatal("a capable client was sent no standard notice")
	}
	if len(capable.sent) != 1 {
		t.Fatalf("sent = %d updates, want one", len(capable.sent))
	}
	notice, ok := capable.sent[0].(acp.NoticeUpdate)
	if !ok {
		t.Fatalf("sent %T, want acp.NoticeUpdate", capable.sent[0])
	}
	if notice.SessionUpdate != acp.UpdateTypeNotice || notice.Severity != acp.NoticeSeverityInfo {
		t.Fatalf("notice = %+v", notice)
	}
	if notice.Title != "Model: qwen3.8-27b for this session" {
		t.Fatalf("title = %q", notice.Title)
	}

	plain := &plainSender{}
	if sendNotice(plain, "sess_1", acp.NoticeSeverityInfo, "Model: qwen3.8-27b for this session", "") {
		t.Fatal("a surface without the capability was sent a standard notice")
	}
	if len(plain.sent) != 0 {
		t.Fatalf("sent = %d updates, want none", len(plain.sent))
	}
}

// An empty line is nothing to say: no update goes out either way.
func TestSendNoticeIgnoresAnEmptyLine(t *testing.T) {
	capable := &fakeNoticeSender{notices: true}
	if sendNotice(capable, "sess_1", acp.NoticeSeverityInfo, "", "") {
		t.Fatal("an empty notice was sent")
	}
	if len(capable.sent) != 0 {
		t.Fatalf("sent = %d updates, want none", len(capable.sent))
	}
}
