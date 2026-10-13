package llm

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// A user message with a picture and a text file, the shape prompt attachments
// and the pictures a read showed (after the agent's send boundary) both take.
func userMessageWithAttachments() Message {
	return Message{
		Role:    RoleUser,
		Content: "what is on it?",
		ImageParts: []ImagePart{
			{Name: "shot.png", DataURL: "data:image/png;base64,iVBORw0KGgo="},
			{Name: "notes.txt", DataURL: "data:text/plain;base64," + base64.StdEncoding.EncodeToString([]byte("file body"))},
		},
	}
}

func TestAnthropicSendsThePicturesOfAUserMessage(t *testing.T) {
	p := newAnthropicProvider("claude-sonnet-4-5", "", "", nil, 8192, 0.7, "")
	_, conv, err := p.splitMessages([]Message{userMessageWithAttachments()})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(conv)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	for _, want := range []string{
		`"text":"what is on it?\n\n[File: notes.txt]\nfile body"`,
		`"type":"image"`,
		`"media_type":"image/png"`,
		`"data":"iVBORw0KGgo="`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("request lacks %s: %s", want, s)
		}
	}
	if text, image := strings.Index(s, "what is on it?"), strings.Index(s, `"type":"image"`); text < 0 || text > image {
		t.Errorf("the picture comes before the text: %s", s)
	}
}

func TestAnthropicKeepsATextOnlyUserMessageAsItWas(t *testing.T) {
	p := newAnthropicProvider("claude-sonnet-4-5", "", "", nil, 8192, 0.7, "")
	_, conv, err := p.splitMessages([]Message{{Role: RoleUser, Content: "hi"}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(conv)
	if string(raw) != `[{"content":[{"text":"hi","type":"text"}],"role":"user"}]` {
		t.Errorf("a plain prompt changed on the wire: %s", raw)
	}
}

func TestCodexSendsThePicturesOfAUserMessage(t *testing.T) {
	p := newCodexProvider("gpt-5.6", filepath.Join(t.TempDir(), "auth.json"), true, "", nil, 0, "")
	params := p.buildParams([]Message{userMessageWithAttachments()}, nil)
	raw, err := json.Marshal(params.Input)
	if err != nil {
		t.Fatal(err)
	}
	s := string(raw)
	for _, want := range []string{
		`"type":"input_text"`,
		`what is on it?\n\n[File: notes.txt]\nfile body`,
		`"type":"input_image"`,
		`"image_url":"data:image/png;base64,iVBORw0KGgo="`,
		`"detail":"auto"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("request lacks %s: %s", want, s)
		}
	}
}

// The Messages API and the Responses API take PNG, JPEG, GIF and WebP pictures.
// Any other image type goes as what it is: an SVG is text and is sent as its
// source, another type is named as not sent, and so is a data URL that is not
// base64 - never dropped without a word, never a request the API refuses.
func TestPicturesAProviderCannotTakeGoAsText(t *testing.T) {
	msg := Message{Role: RoleUser, Content: "files", ImageParts: []ImagePart{
		{Name: "logo.svg", DataURL: "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte("<svg/>"))},
		{Name: "old.bmp", DataURL: "data:image/bmp;base64,Qk0="},
		{Name: "raw.png", DataURL: "data:image/png,not-base64"},
	}}
	anthropic := newAnthropicProvider("claude-sonnet-4-5", "", "", nil, 8192, 0.7, "")
	_, conv, err := anthropic.splitMessages([]Message{msg})
	if err != nil {
		t.Fatal(err)
	}
	codex := newCodexProvider("gpt-5.6", filepath.Join(t.TempDir(), "auth.json"), true, "", nil, 0, "")
	params := codex.buildParams([]Message{msg}, nil)
	for name, v := range map[string]any{"anthropic": conv, "codex": params.Input} {
		raw, _ := json.Marshal(v)
		s := string(raw)
		if strings.Contains(s, `"type":"image"`) || strings.Contains(s, `"type":"input_image"`) {
			t.Errorf("%s sends a picture it cannot take: %s", name, s)
		}
		for _, want := range []string{`[File: logo.svg]\n`, "svg/", "old.bmp", "raw.png"} {
			if !strings.Contains(s, want) {
				t.Errorf("%s does not name %s: %s", name, want, s)
			}
		}
	}
}

// An SVG is text: no provider takes it as a picture, so every one of them
// writes it out as a labelled file, OpenAI-compatible and Devin included.
func TestAnSVGGoesAsTextToEveryProvider(t *testing.T) {
	svg := ImagePart{Name: "logo.svg", DataURL: "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte("<svg/>"))}
	msg := Message{Role: RoleUser, Content: "look", ImageParts: []ImagePart{svg}}

	openai := newOpenAIProvider("gpt-4o", "key", "", nil, 0, 0, "")
	raw, _ := json.Marshal(openai.buildParams([]Message{msg}, nil, false).Messages)
	if s := string(raw); strings.Contains(s, "image_url") || !strings.Contains(s, `[File: logo.svg]\n`) || !strings.Contains(s, "svg/") {
		t.Errorf("OpenAI sends the SVG as %s", s)
	}

	_, prompts := devinPrompts([]Message{msg}, "m")
	if len(prompts[0].images) != 0 || !strings.Contains(prompts[0].text, "[File: logo.svg]\n<svg/>") {
		t.Errorf("Devin sends the SVG as %+v", prompts[0])
	}
	if IsPicture(svg) {
		t.Error("an SVG counts as a picture")
	}
}

// The base64 flag of a data URL is its last parameter, spelled in any case,
// and nothing else: a parameter that only starts with it is not the flag.
func TestTheBase64FlagOfADataURLIsReadExactly(t *testing.T) {
	for _, tc := range []struct {
		url      string
		isBase64 bool
		mime     string
	}{
		{"data:image/png;base64,AAAA", true, "image/png"},
		{"data:image/png;BASE64,AAAA", true, "image/png"},
		{"data:Image/PNG;charset=x;base64,AAAA", true, "image/png"},
		{"data:image/png;base64x,AAAA", false, "image/png"},
		{"data:image/png;base64=1,AAAA", false, "image/png"},
		{"data:image/png;base64;x=1,AAAA", false, "image/png"},
		{"data:image/png,AAAA", false, "image/png"},
		{"https://example.com/a.png", false, ""},
	} {
		mime, isBase64, _ := parseDataURL(tc.url)
		if mime != tc.mime || isBase64 != tc.isBase64 {
			t.Errorf("%s: type %q base64 %v, want %q %v", tc.url, mime, isBase64, tc.mime, tc.isBase64)
		}
	}

	msg := Message{Role: RoleUser, Content: "look", ImageParts: []ImagePart{{Name: "odd.png", DataURL: "data:image/png;base64x,AAAA"}}}
	_, conv, err := newAnthropicProvider("claude-sonnet-4-5", "", "", nil, 8192, 0.7, "").splitMessages([]Message{msg})
	if err != nil {
		t.Fatal(err)
	}
	if raw, _ := json.Marshal(conv); strings.Contains(string(raw), `"type":"image"`) || !strings.Contains(string(raw), "odd.png") {
		t.Errorf("Anthropic sends a data URL with no base64 flag as %s", raw)
	}
}

// A file that is not text - a PDF a person sent from a messenger, an archive
// picked in the web UI - is named in the message and never written out: its
// bytes as text are noise that fills the context window (issue #509). The
// session keeps the file with its assets, and the prompt says where.
func TestABinaryFileIsNamedNotWrittenOut(t *testing.T) {
	pdf := ImagePart{Name: "report.pdf", DataURL: "data:application/pdf;base64," + base64.StdEncoding.EncodeToString([]byte("%PDF-1.4\n%\xe2\xe3\xcf\xd3\x00\n1 0 obj"))}
	notes := ImagePart{Name: "notes.txt", DataURL: "data:text/plain;base64," + base64.StdEncoding.EncodeToString([]byte("file body"))}
	msg := Message{Role: RoleUser, Content: "summarize", ImageParts: []ImagePart{pdf, notes}}

	anthropic := newAnthropicProvider("claude-sonnet-4-5", "", "", nil, 8192, 0.7, "")
	_, conv, err := anthropic.splitMessages([]Message{msg})
	if err != nil {
		t.Fatal(err)
	}
	codex := newCodexProvider("gpt-5.6", filepath.Join(t.TempDir(), "auth.json"), true, "", nil, 0, "")
	openai := newOpenAIProvider("gpt-4o", "key", "", nil, 0, 0, "")
	_, prompts := devinPrompts([]Message{msg}, "m")
	for name, v := range map[string]any{
		"anthropic": conv,
		"codex":     codex.buildParams([]Message{msg}, nil).Input,
		"openai":    openai.buildParams([]Message{msg}, nil, false).Messages,
		"devin":     prompts[0].text,
	} {
		raw, _ := json.Marshal(v)
		s := string(raw)
		if strings.Contains(s, "PDF-1.4") || strings.Contains(s, "1 0 obj") {
			t.Errorf("%s writes the PDF out as text: %s", name, s)
		}
		for _, want := range []string{`[File: report.pdf (application/pdf), not text`, `[File: notes.txt]\nfile body`} {
			if !strings.Contains(s, want) {
				t.Errorf("%s lacks %s: %s", name, want, s)
			}
		}
	}
}

// A text file longer than maxInlinedFileBytes is written out up to there,
// labelled with what was cut: one message larger than the context window
// would stay in the history and fail every request after it. A PDF is never
// written out, even one whose bytes happen to be ASCII.
func TestALongTextFileIsCutAndAPDFNeverWrittenOut(t *testing.T) {
	long := strings.Repeat("log line\n", maxInlinedFileBytes/9+100) + "LAST LINE"
	asciiPDF := "%PDF-1.4\n1 0 obj << /Type /Catalog >> endobj\ntrailer << >>\n%%EOF\n"
	for name, tc := range map[string]struct {
		part      ImagePart
		want, not string
	}{
		"long log": {
			part: ImagePart{Name: "app.log", DataURL: "data:text/plain;base64," + base64.StdEncoding.EncodeToString([]byte(long))},
			want: fmt.Sprintf("[File: app.log, the first %d of its %d bytes]", maxInlinedFileBytes, len(long)), not: "LAST LINE",
		},
		"ascii pdf": {
			part: ImagePart{Name: "plain.pdf", DataURL: "data:application/pdf;base64," + base64.StdEncoding.EncodeToString([]byte(asciiPDF))},
			want: "[File: plain.pdf (application/pdf), not text", not: "/Catalog",
		},
	} {
		mime, _, _ := parseDataURL(tc.part.DataURL)
		got := fileText(tc.part, mime)
		if !strings.Contains(got, tc.want) || strings.Contains(got, tc.not) {
			t.Errorf("%s: %.200q, want %q and no %q", name, got, tc.want, tc.not)
		}
	}
	// A cut never splits a character.
	multi := strings.Repeat("я", maxInlinedFileBytes) // two bytes each
	if got := fileText(ImagePart{Name: "ru.txt", DataURL: "data:text/plain;base64," + base64.StdEncoding.EncodeToString([]byte(multi))}, "text/plain"); !utf8.ValidString(got) {
		t.Error("the cut split a character")
	}
}
