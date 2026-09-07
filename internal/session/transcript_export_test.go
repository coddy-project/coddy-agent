package session

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func TestParseExportFormat(t *testing.T) {
	good := map[string]ExportFormat{
		"md":       ExportFormatMarkdown,
		"MD":       ExportFormatMarkdown,
		"markdown": ExportFormatMarkdown,
		"html":     ExportFormatHTML,
		"HTML":     ExportFormatHTML,
		"json":     ExportFormatJSON,
		"jsonl":    ExportFormatJSONL,
	}
	for in, want := range good {
		got, ok := ParseExportFormat(in)
		if !ok || got != want {
			t.Errorf("ParseExportFormat(%q) = %q, %v; want %q, true", in, got, ok, want)
		}
	}
	for _, bad := range []string{"", "txt", "md.txt", "xml", "chat.md"} {
		if got, ok := ParseExportFormat(bad); ok {
			t.Errorf("ParseExportFormat(%q) = %q, true; want false", bad, got)
		}
	}
}

func TestResolveExportRequest(t *testing.T) {
	cwd := t.TempDir()
	at := time.Date(2026, 9, 6, 12, 34, 56, 0, time.UTC)
	if err := os.MkdirAll(filepath.Join(cwd, "existing"), 0o755); err != nil {
		t.Fatal(err)
	}
	mdName := ExportFileName(ExportFormatMarkdown, at)
	tests := []struct {
		name        string
		req         ExportRequest
		wantFormat  ExportFormat
		wantDisplay string
		wantErr     error
		wantErrText string
	}{
		{name: "bare", req: ExportRequest{}, wantFormat: ExportFormatMarkdown, wantDisplay: mdName},
		{name: "token", req: ExportRequest{Format: "json"}, wantFormat: ExportFormatJSON, wantDisplay: ExportFileName(ExportFormatJSON, at)},
		{name: "token beats extension", req: ExportRequest{Format: "json", Target: "chat.md"}, wantFormat: ExportFormatJSON, wantDisplay: "chat.md"},
		{name: "markdown extension", req: ExportRequest{Target: "chat.md"}, wantFormat: ExportFormatMarkdown, wantDisplay: "chat.md"},
		{name: "html extension in subdir", req: ExportRequest{Target: "notes/chat.html"}, wantFormat: ExportFormatHTML, wantDisplay: filepath.Join("notes", "chat.html")},
		{name: "upper-case extension", req: ExportRequest{Target: "out.JSONL"}, wantFormat: ExportFormatJSONL, wantDisplay: "out.JSONL"},
		{name: "trailing separator", req: ExportRequest{Target: "exports/"}, wantFormat: ExportFormatMarkdown, wantDisplay: filepath.Join("exports", mdName)},
		{name: "existing directory", req: ExportRequest{Target: "existing"}, wantFormat: ExportFormatMarkdown, wantDisplay: filepath.Join("existing", mdName)},
		{name: "mistyped format", req: ExportRequest{Target: "yaml"}, wantErrText: "neither an export format nor an existing directory"},
		{name: "unknown extension", req: ExportRequest{Target: "chat.txt"}, wantErrText: "unknown export format"},
		{name: "unknown token", req: ExportRequest{Format: "yaml"}, wantErrText: "unknown export format"},
		{name: "escape", req: ExportRequest{Target: "../chat.md"}, wantErr: ErrExportOutsideWorkspace},
		{name: "unknown option", req: ExportRequest{UnknownOptions: []string{"--bogus"}}, wantErrText: "unknown option --bogus"},
	}
	for _, tc := range tests {
		format, target, err := ResolveExportRequest(cwd, tc.req, at)
		if tc.wantErr != nil || tc.wantErrText != "" {
			if err == nil {
				t.Errorf("%s: got %q %+v, want error", tc.name, format, target)
				continue
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Errorf("%s: err = %v, want %v", tc.name, err, tc.wantErr)
			}
			if tc.wantErrText != "" && !strings.Contains(err.Error(), tc.wantErrText) {
				t.Errorf("%s: err = %v, want it to mention %q", tc.name, err, tc.wantErrText)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error %v", tc.name, err)
			continue
		}
		if format != tc.wantFormat || target.Display != tc.wantDisplay {
			t.Errorf("%s: got %q %q, want %q %q", tc.name, format, target.Display, tc.wantFormat, tc.wantDisplay)
		}
		if target.Path != filepath.Join(cwd, tc.wantDisplay) {
			t.Errorf("%s: path = %q", tc.name, target.Path)
		}
	}
}

func TestExportFileName(t *testing.T) {
	at := time.Date(2026, 9, 6, 12, 34, 56, 0, time.UTC)
	if got := ExportFileName(ExportFormatMarkdown, at); got != "coddy-export-2026-09-06T12-34-56Z.md" {
		t.Fatalf("ExportFileName md = %q", got)
	}
	// Non-UTC clocks are normalized so the name never depends on the host zone.
	plus3 := time.FixedZone("plus3", 3*3600)
	if got := ExportFileName(ExportFormatJSONL, at.In(plus3)); got != "coddy-export-2026-09-06T12-34-56Z.jsonl" {
		t.Fatalf("ExportFileName jsonl (zoned) = %q", got)
	}
}

func TestResolveExportTarget(t *testing.T) {
	cwd := t.TempDir()
	at := time.Date(2026, 9, 6, 12, 34, 56, 0, time.UTC)
	name := ExportFileName(ExportFormatMarkdown, at)
	if err := os.MkdirAll(filepath.Join(cwd, "existing"), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(filepath.Dir(cwd), "outside-"+filepath.Base(cwd)+".md")

	tests := []struct {
		name        string
		target      string
		wantPath    string
		wantDisplay string
		wantErr     error
	}{
		{"default", "", filepath.Join(cwd, name), name, nil},
		{"dot", ".", filepath.Join(cwd, name), name, nil},
		{"existing directory", "existing", filepath.Join(cwd, "existing", name), filepath.Join("existing", name), nil},
		{"trailing separator", "notes/", filepath.Join(cwd, "notes", name), filepath.Join("notes", name), nil},
		{"file", "chat.md", filepath.Join(cwd, "chat.md"), "chat.md", nil},
		{"nested file", "notes/chat.md", filepath.Join(cwd, "notes", "chat.md"), filepath.Join("notes", "chat.md"), nil},
		{"absolute inside", filepath.Join(cwd, "abs.md"), filepath.Join(cwd, "abs.md"), "abs.md", nil},
		{"parent escape", "../chat.md", "", "", ErrExportOutsideWorkspace},
		{"nested escape", "notes/../../chat.md", "", "", ErrExportOutsideWorkspace},
		{"absolute outside", outside, "", "", ErrExportOutsideWorkspace},
	}
	for _, tc := range tests {
		got, err := ResolveExportTarget(cwd, tc.target, ExportFormatMarkdown, at)
		if tc.wantErr != nil {
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("%s: err = %v, want %v", tc.name, err, tc.wantErr)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: unexpected error %v", tc.name, err)
			continue
		}
		if got.Path != tc.wantPath || got.Display != tc.wantDisplay {
			t.Errorf("%s: got %+v, want path %q display %q", tc.name, got, tc.wantPath, tc.wantDisplay)
		}
	}
}

func TestPrepareExportOutput(t *testing.T) {
	cwd := t.TempDir()
	elsewhere := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, "existing"), 0o755); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name       string
		out        string
		wantRoot   string
		wantTarget string
	}{
		{"empty", "", cwd, ""},
		{"file in cwd", "chat.md", cwd, "chat.md"},
		{"nested file in cwd", "exports/chat.json", cwd, filepath.Join("exports", "chat.json")},
		{"directory in cwd", "exports/", cwd, "exports/"},
		{"existing directory in cwd", "existing", cwd, "existing"},
		{"absolute inside cwd", filepath.Join(cwd, "abs.md"), cwd, "abs.md"},
		{"absolute file elsewhere", filepath.Join(elsewhere, "reports", "chat.html"), filepath.Join(elsewhere, "reports"), "chat.html"},
		{"absolute directory elsewhere", filepath.Join(elsewhere, "dump") + string(filepath.Separator), filepath.Join(elsewhere, "dump"), ""},
		{"parent escape", filepath.Join("..", "up.md"), filepath.Dir(cwd), "up.md"},
	}
	for _, tc := range tests {
		root, target, err := PrepareExportOutput(cwd, tc.out)
		if err != nil {
			t.Errorf("%s: %v", tc.name, err)
			continue
		}
		if root != tc.wantRoot || target != tc.wantTarget {
			t.Errorf("%s: got (%q, %q), want (%q, %q)", tc.name, root, target, tc.wantRoot, tc.wantTarget)
			continue
		}
		if st, err := os.Stat(root); err != nil || !st.IsDir() {
			t.Errorf("%s: root %q must exist as a directory: %v", tc.name, root, err)
		}
	}
}

func TestExportSessionHonorsOutputRoot(t *testing.T) {
	in := exportFixture("/some/workspace")
	in.OutputRoot = t.TempDir()
	res, err := ExportSession(in, ExportRequest{Format: "json"})
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(res.Target.Path) != in.OutputRoot {
		t.Fatalf("export landed in %q, want %q", res.Target.Path, in.OutputRoot)
	}
	b, err := os.ReadFile(res.Target.Path)
	if err != nil {
		t.Fatal(err)
	}
	var back ExportDocument
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatal(err)
	}
	if back.Session.CWD != "/some/workspace" {
		t.Fatalf("session cwd in the document = %q, must stay the session workspace", back.Session.CWD)
	}
}

func TestWriteExportFileCreatesParentAndWrites(t *testing.T) {
	cwd := t.TempDir()
	target, err := ResolveExportTarget(cwd, "notes/deep/chat.md", ExportFormatMarkdown, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := WriteExportFile(cwd, target, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(target.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "hello" {
		t.Fatalf("content = %q", b)
	}
	if runtime.GOOS != "windows" {
		st, err := os.Stat(target.Path)
		if err != nil {
			t.Fatal(err)
		}
		if st.Mode().Perm() != 0o600 {
			t.Fatalf("mode = %o, want 0600", st.Mode().Perm())
		}
	}
}

func TestWriteExportFileRejectsSymlinkEscape(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation needs privileges on windows")
	}
	root := t.TempDir()
	cwd := filepath.Join(root, "ws")
	outside := filepath.Join(root, "outside")
	for _, d := range []string{cwd, outside} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(cwd, "link")); err != nil {
		t.Fatal(err)
	}
	// A directory symlink that leaves the workspace is lexically inside it.
	target, err := ResolveExportTarget(cwd, "link/chat.md", ExportFormatMarkdown, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := WriteExportFile(cwd, target, []byte("x")); !errors.Is(err, ErrExportOutsideWorkspace) {
		t.Fatalf("directory symlink: err = %v, want ErrExportOutsideWorkspace", err)
	}
	if _, err := os.Stat(filepath.Join(outside, "chat.md")); !os.IsNotExist(err) {
		t.Fatalf("file was written through the directory symlink: %v", err)
	}

	// A file symlink pointing outside must not be written through either.
	victim := filepath.Join(outside, "victim.md")
	if err := os.WriteFile(victim, []byte("untouched"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(cwd, "alias.md")); err != nil {
		t.Fatal(err)
	}
	target, err = ResolveExportTarget(cwd, "alias.md", ExportFormatMarkdown, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := WriteExportFile(cwd, target, []byte("x")); !errors.Is(err, ErrExportOutsideWorkspace) {
		t.Fatalf("file symlink: err = %v, want ErrExportOutsideWorkspace", err)
	}
	if b, _ := os.ReadFile(victim); string(b) != "untouched" {
		t.Fatalf("victim rewritten through the file symlink: %q", b)
	}
}

// exportFixture builds a transcript that exercises every entry kind.
func exportFixture(cwd string) ExportInput {
	msgs := []llm.Message{
		{
			Role: llm.RoleUser,
			Content: "look at @README.md\n\n<coddy_attachment path=\"README.md\" name=\"README.md\">\n" +
				"<![CDATA[# Readme body]]>\n</coddy_attachment>",
			CreatedAt: "2026-09-06T10:00:00Z",
		},
		{
			Role:      llm.RoleAssistant,
			Reasoning: "thinking about it",
			ToolCalls: []llm.ToolCall{
				{ID: "call_1", Name: "read", InputJSON: `{"path":"README.md"}`},
				{ID: "call_2", Name: "grep", InputJSON: `{not json`},
			},
			Model:     "fake/model",
			CreatedAt: "2026-09-06T10:00:05Z",
		},
		{Role: llm.RoleTool, ToolCallID: "call_1", Content: "# Readme body"},
		{Role: llm.RoleTool, ToolCallID: "call_2", Content: "```go\ncode\n```"},
		{Role: llm.RoleAssistant, Content: "Here is the summary with <b>&</b>.", Model: "fake/model", CreatedAt: "2026-09-06T10:00:09Z"},
		NewCompactionSummaryMessage("older stuff was folded", "fake/model"),
		{Role: llm.RoleTool, ToolCallID: "orphan", Content: "late result"},
		{
			Role:         llm.RoleAssistant,
			PlanDocument: &llm.PlanDocumentSnapshot{Slug: "plan-1", Name: "Plan one", Content: "# Plan one\n\nsteps", Body: "steps"},
			CreatedAt:    "2026-09-06T10:01:00Z",
		},
	}
	return ExportInput{
		SessionID:  "sess_1",
		Title:      "Readme review",
		CWD:        cwd,
		GitBranch:  "main",
		Model:      "fake/model",
		Messages:   msgs,
		Stats:      &SessionStats{TokenUsageTotal: TokenUsageTotals{InputTokens: 10, OutputTokens: 5, TotalTokens: 15}},
		ExportedAt: time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC),
	}
}

func TestBuildExportDocumentPairsToolResultsAndStripsAttachments(t *testing.T) {
	doc := BuildExportDocument(exportFixture("/ws"))

	if doc.Version != 1 {
		t.Fatalf("version = %d", doc.Version)
	}
	s := doc.Session
	if s.ID != "sess_1" || s.Title != "Readme review" || s.CWD != "/ws" || s.GitBranch != "main" || s.Model != "fake/model" {
		t.Fatalf("session info = %+v", s)
	}
	if s.StartedAt != "2026-09-06T10:00:00Z" || s.ExportedAt != "2026-09-06T12:00:00Z" {
		t.Fatalf("timestamps = %q / %q", s.StartedAt, s.ExportedAt)
	}
	if s.MessageCount != 8 || s.UserTurns != 1 {
		t.Fatalf("counts = %d messages, %d user turns", s.MessageCount, s.UserTurns)
	}
	if s.TokenUsage == nil || s.TokenUsage.TotalTokens != 15 {
		t.Fatalf("token usage = %+v", s.TokenUsage)
	}

	if len(doc.Entries) != 6 {
		t.Fatalf("entries = %d, want 6: %+v", len(doc.Entries), doc.Entries)
	}
	e := doc.Entries
	if e[0].Type != "user" || e[0].Text != "look at @README.md" || e[0].CreatedAt != "2026-09-06T10:00:00Z" {
		t.Fatalf("user entry = %+v", e[0])
	}
	if len(e[0].Attachments) != 1 || e[0].Attachments[0].Path != "README.md" || e[0].Attachments[0].Name != "README.md" {
		t.Fatalf("attachments = %+v", e[0].Attachments)
	}
	if e[1].Type != "assistant" || e[1].Reasoning != "thinking about it" || e[1].Model != "fake/model" {
		t.Fatalf("assistant entry = %+v", e[1])
	}
	if len(e[1].ToolCalls) != 2 {
		t.Fatalf("tool calls = %+v", e[1].ToolCalls)
	}
	tc := e[1].ToolCalls[0]
	if tc.ID != "call_1" || tc.Name != "read" || string(tc.Input) != `{"path":"README.md"}` || tc.Result == nil || *tc.Result != "# Readme body" {
		t.Fatalf("paired tool call = %+v", tc)
	}
	// Malformed arguments stay a JSON string so the document is always valid JSON.
	if got := string(e[1].ToolCalls[1].Input); got != `"{not json"` {
		t.Fatalf("malformed input = %s", got)
	}
	if e[2].Type != "assistant" || e[2].Text != "Here is the summary with <b>&</b>." {
		t.Fatalf("second assistant entry = %+v", e[2])
	}
	if e[3].Type != "compaction_summary" || !strings.Contains(e[3].Text, "older stuff was folded") {
		t.Fatalf("compaction entry = %+v", e[3])
	}
	if e[4].Type != "tool_result" || e[4].ToolCallID != "orphan" || e[4].Text != "late result" {
		t.Fatalf("orphan tool result = %+v", e[4])
	}
	if e[5].Type != "plan_document" || e[5].Plan == nil || e[5].Plan.Slug != "plan-1" || e[5].Plan.Name != "Plan one" || e[5].Text != "# Plan one\n\nsteps" {
		t.Fatalf("plan entry = %+v", e[5])
	}
}

func TestBuildExportDocumentToolResultPresence(t *testing.T) {
	msgs := []llm.Message{
		{Role: llm.RoleUser, Content: "go"},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{
			{ID: "empty", Name: "run_command", InputJSON: `{"command":"true"}`},
			{ID: "pending", Name: "glob", InputJSON: `{"pattern":"*.go"}`},
		}},
		{Role: llm.RoleTool, ToolCallID: "empty", Content: ""},
	}
	doc := BuildExportDocument(ExportInput{SessionID: "s", Messages: msgs})
	calls := doc.Entries[1].ToolCalls
	if calls[0].Result == nil || *calls[0].Result != "" {
		t.Fatalf("an empty tool result must be recorded as present: %+v", calls[0])
	}
	if calls[1].Result != nil {
		t.Fatalf("a call without a result row must have no result: %+v", calls[1])
	}
	md, err := RenderExport(doc, ExportFormatMarkdown)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(md), "Result:\n\n(empty)") || strings.Count(string(md), "Result:") != 1 {
		t.Fatalf("markdown must show the empty result once and nothing for the pending call:\n%s", md)
	}
	out, err := RenderExport(doc, ExportFormatJSON)
	if err != nil {
		t.Fatal(err)
	}
	var back struct {
		Entries []struct {
			ToolCalls []map[string]json.RawMessage `json:"tool_calls"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatal(err)
	}
	if got, ok := back.Entries[1].ToolCalls[0]["result"]; !ok || string(got) != `""` {
		t.Fatalf("json must carry the empty result explicitly: %s", out)
	}
	if _, ok := back.Entries[1].ToolCalls[1]["result"]; ok {
		t.Fatalf("json must omit the missing result: %s", out)
	}
}

func TestSplitUserAttachmentsStripsEveryBlockShape(t *testing.T) {
	content := "see @a.md and @b.md\n\n" +
		"<coddy_attachment name=\"a.md\" path=\"dir/a.md\" extra=\"1\">\n<![CDATA[body a]]>\n</coddy_attachment>\n" +
		"<coddy_attachment>\n<![CDATA[body without attributes]]>\n</coddy_attachment>\n" +
		"<coddy_attachment path=\"b &amp; c.md\" name=\"b.md\">\n<![CDATA[body b]]>\n</coddy_attachment>"
	text, atts := splitUserAttachments(content)
	if text != "see @a.md and @b.md" {
		t.Fatalf("text = %q", text)
	}
	if len(atts) != 2 || atts[0].Path != "dir/a.md" || atts[0].Name != "a.md" || atts[1].Path != "b & c.md" || atts[1].Name != "b.md" {
		t.Fatalf("attachments = %+v", atts)
	}
}

func TestSplitUserAttachmentsWalksCDATA(t *testing.T) {
	// A file that itself contains the closing tag, plus a "]]>" the producer
	// split into two CDATA sections, must not end the block early.
	body := "b.WriteString(\"</coddy_attachment>\")\nx := \"]]]]><![CDATA[>\""
	content := "review @react.go please\n\n" +
		"<coddy_attachment path=\"internal/agent/react.go\" name=\"react.go\">\n<![CDATA[" + body + "]]>\n</coddy_attachment>\n\nand tell me"
	text, atts := splitUserAttachments(content)
	if text != "review @react.go please\n\nand tell me" {
		t.Fatalf("text = %q", text)
	}
	if len(atts) != 1 || atts[0].Path != "internal/agent/react.go" {
		t.Fatalf("attachments = %+v", atts)
	}

	// Plain text that merely mentions the tag is not a block and stays, and
	// a message without blocks is verbatim, whitespace included.
	for _, keep := range []string{
		"the <coddy_attachment> tag is unterminated here",
		"a longer word <coddy_attachments> is not the tag",
		"<coddy_attachment path=\"x\">\n<![CDATA[never closed",
		"\n\n  leading and trailing space  \n\n\n\nkept as typed\n",
	} {
		if got, atts := splitUserAttachments(keep); got != keep || len(atts) != 0 {
			t.Errorf("splitUserAttachments(%q) = %q, %+v; want the text unchanged", keep, got, atts)
		}
	}
}

func TestMarkdownFenceOutgrowsTheLongestRun(t *testing.T) {
	cases := map[string]string{
		"":                        "```",
		"plain":                   "```",
		"a `code` span":           "```",
		"```\ncode\n```":          "````",
		"``````six":               "```````",
		"x ```` y `````` z ``` w": "```````",
	}
	for in, want := range cases {
		if got := markdownFence(in); got != want {
			t.Errorf("markdownFence(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGeneratedNamesNeverOverwriteEachOther(t *testing.T) {
	cwd := t.TempDir()
	at := time.Date(2026, 9, 6, 12, 34, 56, 0, time.UTC)
	first := ExportFileName(ExportFormatMarkdown, at)

	// Resolution skips names already on disk so the reply is usually right.
	if err := os.WriteFile(filepath.Join(cwd, first), []byte("earlier"), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := ResolveExportTarget(cwd, "", ExportFormatMarkdown, at)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Generated || got.Display != "coddy-export-2026-09-06T12-34-56Z-2.md" {
		t.Fatalf("second export in the same second = %+v", got)
	}

	// Two exports that resolved the same name before either wrote land in
	// two files: the write reserves the name exclusively.
	a, err := ResolveExportTarget(cwd, "", ExportFormatMarkdown, at)
	if err != nil {
		t.Fatal(err)
	}
	b, err := ResolveExportTarget(cwd, "", ExportFormatMarkdown, at)
	if err != nil {
		t.Fatal(err)
	}
	if a.Path != b.Path {
		t.Fatalf("both resolutions should pick the same free name: %q vs %q", a.Path, b.Path)
	}
	wroteA, err := WriteExportFile(cwd, a, []byte("A"))
	if err != nil {
		t.Fatal(err)
	}
	wroteB, err := WriteExportFile(cwd, b, []byte("B"))
	if err != nil {
		t.Fatal(err)
	}
	if wroteA.Path != a.Path || wroteB.Path == wroteA.Path || wroteB.Display != "coddy-export-2026-09-06T12-34-56Z-3.md" {
		t.Fatalf("race resolution: A=%+v B=%+v", wroteA, wroteB)
	}
	for path, want := range map[string]string{wroteA.Path: "A", wroteB.Path: "B", filepath.Join(cwd, first): "earlier"} {
		if data, err := os.ReadFile(path); err != nil || string(data) != want {
			t.Fatalf("%s = %q, %v; want %q", path, data, err, want)
		}
	}

	// An explicit file name is the user's choice and is replaced as documented.
	got, err = ResolveExportTarget(cwd, first, ExportFormatMarkdown, at)
	if err != nil || got.Generated || got.Display != first {
		t.Fatalf("explicit name = %+v, %v", got, err)
	}
	if _, err := WriteExportFile(cwd, got, []byte("replaced")); err != nil {
		t.Fatal(err)
	}
	if data, _ := os.ReadFile(got.Path); string(data) != "replaced" {
		t.Fatalf("explicit name not replaced: %q", data)
	}
}

func TestWriteExportFileReplacesWithOwnerOnlyMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes are unix-only")
	}
	cwd := t.TempDir()
	target, err := ResolveExportTarget(cwd, "chat.md", ExportFormatMarkdown, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(target.Path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := WriteExportFile(cwd, target, []byte("new")); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(target.Path)
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(target.Path)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "new" || st.Mode().Perm() != 0o600 {
		t.Fatalf("overwrite left content %q mode %o, want new content with 0600", b, st.Mode().Perm())
	}
	if leftovers, _ := filepath.Glob(filepath.Join(cwd, "chat.md.tmp.*")); len(leftovers) != 0 {
		t.Fatalf("temporary files left behind: %v", leftovers)
	}
}

func TestBuildExportDocumentNormalizesTitleAndUnknownRows(t *testing.T) {
	in := exportFixture("/ws")
	in.Title = "  first line\nsecond   line  "
	in.Messages = append(in.Messages, llm.Message{Role: llm.RoleSystem, Content: "system note"}, llm.Message{Role: "", Content: "no role"})
	doc := BuildExportDocument(in)
	if doc.Session.Title != "first line second line" {
		t.Fatalf("title = %q", doc.Session.Title)
	}
	last := doc.Entries[len(doc.Entries)-2:]
	if last[0].Type != ExportEntrySystem || last[0].Text != "system note" || last[1].Type != ExportEntrySystem || last[1].Text != "no role" {
		t.Fatalf("fallback entries = %+v", last)
	}
	for _, f := range ExportFormats() {
		if _, err := RenderExport(doc, f); err != nil {
			t.Fatalf("%s: %v", f, err)
		}
	}
	// A document assembled elsewhere may carry an empty type; rendering must not panic.
	doc.Entries = append(doc.Entries, ExportEntry{Text: "typeless"})
	md, err := RenderExport(doc, ExportFormatMarkdown)
	if err != nil || !strings.Contains(string(md), "## Entry") {
		t.Fatalf("typeless entry: %v\n%s", err, md)
	}
	if _, err := RenderExport(doc, ExportFormatHTML); err != nil {
		t.Fatalf("typeless entry html: %v", err)
	}
}

func TestBuildExportDocumentHonorsOptions(t *testing.T) {
	in := exportFixture("/ws")
	in.Options = ExportOptions{NoTools: true, NoThinking: true}
	doc := BuildExportDocument(in)

	// The assistant row that only carried reasoning and tool calls disappears,
	// so does the orphan tool result; the conversation itself stays.
	var types []string
	for _, e := range doc.Entries {
		types = append(types, e.Type)
		if e.Reasoning != "" || len(e.ToolCalls) > 0 {
			t.Fatalf("entry still carries tool calls or reasoning: %+v", e)
		}
	}
	want := []string{ExportEntryUser, ExportEntryAssistant, ExportEntryCompactionSummary, ExportEntryPlanDocument}
	if !reflect.DeepEqual(types, want) {
		t.Fatalf("entry types = %v, want %v", types, want)
	}
	// The counters still describe the raw transcript.
	if doc.Session.MessageCount != 8 || doc.Session.UserTurns != 1 {
		t.Fatalf("counts = %d messages, %d user turns", doc.Session.MessageCount, doc.Session.UserTurns)
	}

	in.Options = ExportOptions{NoThinking: true}
	doc = BuildExportDocument(in)
	if len(doc.Entries) != 6 || doc.Entries[1].Reasoning != "" || len(doc.Entries[1].ToolCalls) != 2 {
		t.Fatalf("NoThinking alone must keep the tool calls: %+v", doc.Entries[1])
	}
}

func TestRenderExportMarkdown(t *testing.T) {
	doc := BuildExportDocument(exportFixture("/ws"))
	out, err := RenderExport(doc, ExportFormatMarkdown)
	if err != nil {
		t.Fatal(err)
	}
	md := string(out)
	for _, want := range []string{
		"# Coddy session export",
		"`sess_1`",
		"Readme review",
		"`/ws`",
		"`main`",
		"## User",
		"look at @README.md",
		"`README.md`",
		"<summary>Reasoning</summary>",
		"thinking about it",
		"Tool call: read",
		"```json\n{\n  \"path\": \"README.md\"\n}\n```",
		"# Readme body",
		"Here is the summary with <b>&</b>.",
		"## Compaction summary",
		"older stuff was folded",
		"## Plan document: Plan one",
		"late result",
		// A result holding a fence is wrapped in a longer fence so it stays intact.
		"````\n```go\ncode\n```\n````",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("markdown lacks %q:\n%s", want, md)
		}
	}
	if strings.Contains(md, "<coddy_attachment") {
		t.Errorf("markdown leaks the attachment XML:\n%s", md)
	}
}

func TestRenderExportHTMLIsSelfContainedAndEscaped(t *testing.T) {
	doc := BuildExportDocument(exportFixture("/ws"))
	out, err := RenderExport(doc, ExportFormatHTML)
	if err != nil {
		t.Fatal(err)
	}
	h := string(out)
	if !strings.HasPrefix(strings.ToLower(h), "<!doctype html>") {
		t.Fatalf("html does not start with a doctype: %.40q", h)
	}
	if strings.Contains(strings.ToLower(h), "<script") {
		t.Fatalf("html must not carry scripts")
	}
	for _, want := range []string{
		"<title>Readme review</title>",
		"Here is the summary with &lt;b&gt;&amp;&lt;/b&gt;.",
		"look at @README.md",
		"thinking about it",
		"Tool call: read",
		"older stuff was folded",
		"Plan one",
	} {
		if !strings.Contains(h, want) {
			t.Errorf("html lacks %q", want)
		}
	}
	if strings.Contains(h, "<b>&</b>") {
		t.Errorf("html leaks unescaped message content")
	}
}

func TestRenderExportJSONAndJSONL(t *testing.T) {
	doc := BuildExportDocument(exportFixture("/ws"))

	out, err := RenderExport(doc, ExportFormatJSON)
	if err != nil {
		t.Fatal(err)
	}
	var back ExportDocument
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatalf("json round trip: %v", err)
	}
	if back.Session.ID != "sess_1" || len(back.Entries) != len(doc.Entries) {
		t.Fatalf("json round trip = %+v", back.Session)
	}
	var args map[string]string
	if err := json.Unmarshal(back.Entries[1].ToolCalls[0].Input, &args); err != nil || args["path"] != "README.md" {
		t.Fatalf("tool input round trip = %s (%v)", back.Entries[1].ToolCalls[0].Input, err)
	}

	out, err = RenderExport(doc, ExportFormatJSONL)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	if len(lines) != 1+len(doc.Entries) {
		t.Fatalf("jsonl lines = %d, want %d", len(lines), 1+len(doc.Entries))
	}
	var head struct {
		Type    string `json:"type"`
		Version int    `json:"version"`
		ID      string `json:"id"`
	}
	if err := json.Unmarshal([]byte(lines[0]), &head); err != nil {
		t.Fatalf("jsonl head: %v", err)
	}
	if head.Type != "session" || head.Version != 1 || head.ID != "sess_1" {
		t.Fatalf("jsonl head = %+v", head)
	}
	for i, ln := range lines[1:] {
		var ent ExportEntry
		if err := json.Unmarshal([]byte(ln), &ent); err != nil {
			t.Fatalf("jsonl line %d: %v", i+1, err)
		}
		if ent.Type != doc.Entries[i].Type {
			t.Fatalf("jsonl line %d type = %q, want %q", i+1, ent.Type, doc.Entries[i].Type)
		}
	}
}

func TestExportSessionWritesRenderedDocument(t *testing.T) {
	cwd := t.TempDir()
	res, err := ExportSession(exportFixture(cwd), ExportRequest{Format: "json", Target: "out/chat.json"})
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(cwd, "out", "chat.json"); res.Target.Path != want {
		t.Fatalf("path = %q, want %q", res.Target.Path, want)
	}
	if res.Format != ExportFormatJSON || res.Entries != 6 || res.Bytes == 0 {
		t.Fatalf("result = %+v", res)
	}
	b, err := os.ReadFile(res.Target.Path)
	if err != nil {
		t.Fatal(err)
	}
	var back ExportDocument
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("written file is not the JSON document: %v", err)
	}
	if len(back.Entries) != 6 {
		t.Fatalf("written entries = %d", len(back.Entries))
	}
}
