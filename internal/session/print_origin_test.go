package session

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

// A session one-shot print mode (coddy -p) creates is marked "print" when it is
// created and left out of every listing a person picks a conversation from,
// unless the listing asks for print runs.

func TestOriginFilterKeepsPrintRunsApart(t *testing.T) {
	cases := []struct {
		filter OriginFilter
		origin string
		want   bool
	}{
		{OriginAny, PrintOrigin, true},
		{OriginLocal, "", true},
		{OriginLocal, PrintOrigin, false},
		{OriginLocal, GatewayOrigin("telegram"), false},
		{OriginGateway, PrintOrigin, false},
		{OriginPrint, PrintOrigin, true},
		{OriginPrint, "", false},
		{OriginPrint, GatewayOrigin("telegram"), false},
	}
	for _, tc := range cases {
		if got := tc.filter.Keeps(tc.origin); got != tc.want {
			t.Errorf("%q.Keeps(%q) = %v, want %v", tc.filter, tc.origin, got, tc.want)
		}
	}
	if got, ok := ParseOriginFilter("print"); !ok || got != OriginPrint {
		t.Fatalf("ParseOriginFilter(print) = %q,%v", got, ok)
	}
}

func TestParseRequestedOriginAcceptsOnlyPrint(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want string
		ok   bool
	}{
		{"", "", true},
		{"print", PrintOrigin, true},
		{" Print ", PrintOrigin, true},
		{"gateway:telegram", "", false},
		{"gateway", "", false},
		{"cli", "", false},
	} {
		got, ok := ParseRequestedOrigin(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("ParseRequestedOrigin(%q) = %q,%v, want %q,%v", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

// writeOriginBundle stores a root session with the given origin, or a child
// of parent when parent is not empty.
func writeOriginBundle(t *testing.T, fs *FileStore, id, origin, parent, cwd string) {
	t.Helper()
	var dir string
	if parent == "" {
		var err error
		if dir, err = fs.EnsureLayout(id); err != nil {
			t.Fatal(err)
		}
	} else {
		dir = filepath.Join(fs.SessionPath(parent), ChildSessionsDirName, id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	meta := SessionMeta{
		Version:   sessionFileLayout,
		ID:        id,
		CWD:       cwd,
		Mode:      "agent",
		Origin:    origin,
		UpdatedAt: "2026-10-09T10:00:00Z",
	}
	if parent != "" {
		meta.SubagentRun = true
		meta.ParentSessionID = parent
		meta.SubagentName = "explore"
		meta.SubagentDepth = 1
	}
	b, err := json.Marshal(meta)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, sessionMetaFile), b, 0o644); err != nil {
		t.Fatal(err)
	}
	// Somebody wrote in it: a session without a message is left out of the
	// listings on its own account (issue #357).
	tr, err := json.Marshal(messagesFileData{Version: messagesLayout, Messages: []llm.Message{{Role: llm.RoleUser, Content: "hello"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, messagesFile), tr, 0o644); err != nil {
		t.Fatal(err)
	}
}

func listedIDs(t *testing.T, fs *FileStore, opts ListOptions) []string {
	t.Helper()
	rows, err := fs.ListSnapshotsWith(opts)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.SessionID)
	}
	sort.Strings(ids)
	return ids
}

func sameIDs(got []string, want ...string) bool {
	sort.Strings(want)
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}

func TestListingLeavesPrintRunsOutUnlessAsked(t *testing.T) {
	root := t.TempDir()
	fs := &FileStore{Root: root}
	cwd := t.TempDir()
	writeOriginBundle(t, fs, "sess_chat", "", "", cwd)
	writeOriginBundle(t, fs, "sess_script", PrintOrigin, "", cwd)
	writeOriginBundle(t, fs, "sess_bot", GatewayOrigin("telegram"), "", cwd)
	writeOriginBundle(t, fs, "sess_chatkid", "", "sess_chat", cwd)
	writeOriginBundle(t, fs, "sess_scriptkid", "", "sess_script", cwd)

	for _, tc := range []struct {
		name string
		opts ListOptions
		want []string
	}{
		{"default", ListOptions{}, []string{"sess_chat", "sess_bot"}},
		{"include print", ListOptions{IncludePrintRuns: true}, []string{"sess_chat", "sess_script", "sess_bot"}},
		{"origin print alone", ListOptions{Origin: OriginPrint}, []string{"sess_script"}},
		{"origin local", ListOptions{Origin: OriginLocal}, []string{"sess_chat"}},
		{"origin local with print", ListOptions{Origin: OriginLocal, IncludePrintRuns: true}, []string{"sess_chat"}},
		{"folder", ListOptions{CWD: cwd}, []string{"sess_chat", "sess_bot"}},
		// A hidden print run takes its children with it: an active subagent of a
		// script's run is not a conversation of the person either.
		{"with children", ListOptions{IncludeSubagents: true}, []string{"sess_chat", "sess_bot", "sess_chatkid"}},
		{"with children and print", ListOptions{IncludeSubagents: true, IncludePrintRuns: true},
			[]string{"sess_chat", "sess_script", "sess_bot", "sess_chatkid", "sess_scriptkid"}},
	} {
		if got := listedIDs(t, fs, tc.opts); !sameIDs(got, tc.want...) {
			t.Errorf("%s: listed %v, want %v", tc.name, got, tc.want)
		}
	}

	// The wrapper every picker goes through hides them too.
	rows, err := fs.ListSnapshots(cwd, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.SessionID == "sess_script" {
			t.Errorf("ListSnapshots listed the print run")
		}
	}
}

func readStoredOrigin(t *testing.T, fs *FileStore, id string) string {
	t.Helper()
	snap, err := fs.ReadSnapshot(id)
	if err != nil {
		t.Fatalf("read %s: %v", id, err)
	}
	return snap.Meta.Origin
}

func TestNextSessionOriginMarksOnlyTheSessionItCreates(t *testing.T) {
	root := t.TempDir()
	fs := &FileStore{Root: root}
	mgr := newLoadTestManager(t, root, fs)
	ctx := context.Background()

	mgr.SetNextSessionOrigin(PrintOrigin)
	res, err := mgr.HandleSessionNew(ctx, acp.SessionNewParams{CWD: root})
	if err != nil {
		t.Fatal(err)
	}
	if got := readStoredOrigin(t, fs, res.SessionID); got != PrintOrigin {
		t.Fatalf("stored origin = %q, want %q", got, PrintOrigin)
	}
	if got := mgr.SessionByID(res.SessionID).GetOrigin(); got != PrintOrigin {
		t.Fatalf("live origin = %q, want %q", got, PrintOrigin)
	}

	// The mark is spent on the session it created.
	next, err := mgr.HandleSessionNew(ctx, acp.SessionNewParams{CWD: root})
	if err != nil {
		t.Fatal(err)
	}
	if got := readStoredOrigin(t, fs, next.SessionID); got != "" {
		t.Fatalf("a later session inherited origin %q", got)
	}
}

func TestNextSessionOriginNeverRelabelsAReopenedSession(t *testing.T) {
	root := t.TempDir()
	fs := &FileStore{Root: root}
	mgr := newLoadTestManager(t, root, fs)
	ctx := context.Background()

	chat, err := mgr.HandleSessionNew(ctx, acp.SessionNewParams{CWD: root})
	if err != nil {
		t.Fatal(err)
	}
	mgr.ForgetLiveSession(chat.SessionID)

	// coddy --session-id <a person's chat> -p "...": the run continues that chat
	// and must not turn it into a print run.
	mgr.SetPreferredSessionID(chat.SessionID)
	mgr.SetNextSessionOrigin(PrintOrigin)
	again, err := mgr.HandleSessionNew(ctx, acp.SessionNewParams{CWD: root})
	if err != nil {
		t.Fatal(err)
	}
	if again.SessionID != chat.SessionID {
		t.Fatalf("reopened %s, want %s", again.SessionID, chat.SessionID)
	}
	if got := readStoredOrigin(t, fs, chat.SessionID); got != "" {
		t.Fatalf("reopened chat relabelled %q", got)
	}
	if got := mgr.SessionByID(chat.SessionID).GetOrigin(); got != "" {
		t.Fatalf("reopened chat relabelled %q in memory", got)
	}

	// A new id named on the command line is a session the run creates.
	mgr.SetPreferredSessionID("sess_named_by_the_script")
	mgr.SetNextSessionOrigin(PrintOrigin)
	named, err := mgr.HandleSessionNew(ctx, acp.SessionNewParams{CWD: root})
	if err != nil {
		t.Fatal(err)
	}
	if got := readStoredOrigin(t, fs, named.SessionID); got != PrintOrigin {
		t.Fatalf("new --session-id origin = %q, want %q", got, PrintOrigin)
	}
}

func TestEnsureHTTPSessionAsMarksOnlyANewBundle(t *testing.T) {
	root := t.TempDir()
	fs := &FileStore{Root: root}
	mgr := newLoadTestManager(t, root, fs)
	ctx := context.Background()

	st, err := mgr.EnsureHTTPSessionAs(ctx, "sess_remote_print", root, PrintOrigin)
	if err != nil {
		t.Fatal(err)
	}
	if st.GetOrigin() != PrintOrigin || readStoredOrigin(t, fs, "sess_remote_print") != PrintOrigin {
		t.Fatalf("new bundle origin = %q / %q", st.GetOrigin(), readStoredOrigin(t, fs, "sess_remote_print"))
	}

	if _, err := mgr.EnsureHTTPSession(ctx, "sess_browser_chat", root); err != nil {
		t.Fatal(err)
	}
	mgr.ForgetLiveSession("sess_browser_chat")
	st, err = mgr.EnsureHTTPSessionAs(ctx, "sess_browser_chat", root, PrintOrigin)
	if err != nil {
		t.Fatal(err)
	}
	if st.GetOrigin() != "" || readStoredOrigin(t, fs, "sess_browser_chat") != "" {
		t.Fatalf("an existing chat was relabelled: %q / %q", st.GetOrigin(), readStoredOrigin(t, fs, "sess_browser_chat"))
	}
}
