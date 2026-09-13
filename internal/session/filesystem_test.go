package session

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
)

func TestFileStoreRoundTripUILog(t *testing.T) {
	root := t.TempDir()
	fs := &FileStore{Root: root}

	id := "sess_ulog"
	dir, err := fs.EnsureLayout(id)
	if err != nil {
		t.Fatal(err)
	}

	st := &State{
		ID:         id,
		CWD:        "/tmp/unit",
		Mode:       ModeAgent,
		SessionDir: dir,
	}
	st.AppendUILogError(1, "context exceeded")

	if err := fs.Save(st); err != nil {
		t.Fatal(err)
	}
	snap, err := fs.ReadSnapshot(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.UILog) != 1 {
		t.Fatalf("ui log len=%d", len(snap.UILog))
	}
	if snap.UILog[0].Message != "context exceeded" || snap.UILog[0].UserTurnIndex != 1 {
		t.Fatalf("entry %+v", snap.UILog[0])
	}
}

func TestFileStoreRoundTripMessages(t *testing.T) {
	root := t.TempDir()
	fs := &FileStore{Root: root}

	id := "sess_unit"
	dir, err := fs.EnsureLayout(id)
	if err != nil {
		t.Fatal(err)
	}

	st := &State{
		ID:         id,
		CWD:        "/tmp/unit",
		Mode:       ModeAgent,
		SessionDir: dir,
	}
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "hi"})
	st.AddMessage(llm.Message{
		Role:                llm.RoleAssistant,
		Content:             "hello",
		Reasoning:           "step one",
		ReasoningDurationMs: 42,
	})

	if err := fs.Save(st); err != nil {
		t.Fatal(err)
	}

	snap, err := fs.ReadSnapshot(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Messages) != 2 {
		t.Fatalf("messages roundtrip len=%d", len(snap.Messages))
	}
	if snap.Messages[1].Role != llm.RoleAssistant {
		t.Fatalf("second role %+v", snap.Messages[1].Role)
	}
	if snap.Messages[1].Reasoning != "step one" || snap.Messages[1].ReasoningDurationMs != 42 {
		t.Fatalf("reasoning roundtrip %+v", snap.Messages[1])
	}
}

func TestActiveTodoPersistence(t *testing.T) {
	root := t.TempDir()
	fs := &FileStore{Root: root}

	id := "sess_td"
	dir, err := fs.EnsureLayout(id)
	if err != nil {
		t.Fatal(err)
	}

	st := &State{
		ID:         id,
		CWD:        "/tmp",
		Mode:       ModeAgent,
		SessionDir: dir,
	}
	st.SetPlanWithoutPersist([]acp.PlanEntry{
		{Content: "a", Status: "pending"},
		{Content: "b", Status: "completed"},
	})

	if err := fs.Save(st); err != nil {
		t.Fatal(err)
	}
	snap, err := fs.ReadSnapshot(st.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Plan) != 2 {
		t.Fatalf("plan len=%d", len(snap.Plan))
	}
}

func TestListSnapshotsOrdersSameSecondSessionsByRecency(t *testing.T) {
	root := t.TempDir()
	fs := &FileStore{Root: root}
	// The older session deliberately gets the lexicographically smaller id:
	// with second-granularity timestamps the tie-break would list it first,
	// which is exactly the -c/--continue bug this guards against.
	for attempt := range 3 {
		older := fmt.Sprintf("sess_a%d", attempt)
		newer := fmt.Sprintf("sess_z%d", attempt)
		for _, id := range []string{older, newer} {
			dir, err := fs.EnsureLayout(id)
			if err != nil {
				t.Fatal(err)
			}
			st := &State{ID: id, CWD: "/tmp/order", Mode: ModeAgent, SessionDir: dir}
			if err := fs.Save(st); err != nil {
				t.Fatal(err)
			}
			time.Sleep(5 * time.Millisecond)
		}
		rows, err := fs.ListSnapshots("/tmp/order", false)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) == 0 || rows[0].SessionID != newer {
			t.Fatalf("attempt %d: newest session %q is not first: %+v", attempt, newer, rows)
		}
	}
}

func TestListSnapshotsSkipsMarkedSchedulerRunsByMeta(t *testing.T) {
	root := t.TempDir()
	fs := &FileStore{Root: root}

	visibleID := "conv_normal"
	dirV, err := fs.EnsureLayout(visibleID)
	if err != nil {
		t.Fatal(err)
	}
	v := &State{ID: visibleID, CWD: "/tmp", Mode: ModeAgent, SessionDir: dirV}
	v.AddMessage(llm.Message{Role: llm.RoleUser, Content: "hi"})
	if err := fs.Save(v); err != nil {
		t.Fatal(err)
	}

	metaOnlyID := "custom_no_prefix"
	dirM, err := fs.EnsureLayout(metaOnlyID)
	if err != nil {
		t.Fatal(err)
	}
	m := &State{ID: metaOnlyID, CWD: "/tmp", Mode: ModeAgent, SessionDir: dirM}
	m.SetSchedulerRunMeta("job_a", time.Now().UTC().Format(time.RFC3339))
	m.AddMessage(llm.Message{Role: llm.RoleUser, Content: "cron"})
	if err := fs.Save(m); err != nil {
		t.Fatal(err)
	}

	rowsDefault, err := fs.ListSnapshots("", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rowsDefault) != 1 || rowsDefault[0].SessionID != visibleID {
		t.Fatalf("composer list: %+v", rowsDefault)
	}
	all, err := fs.ListSnapshots("", true)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Fatalf("want 2 sessions with include scheduler, got %d", len(all))
	}
}

func TestListSnapshotsSkipsSchedulerSessions(t *testing.T) {
	root := t.TempDir()
	fs := &FileStore{Root: root}

	// Normal persisted session.
	normalID := "sess_normal"
	normalDir, err := fs.EnsureLayout(normalID)
	if err != nil {
		t.Fatal(err)
	}
	normal := &State{
		ID:         normalID,
		CWD:        "/tmp/unit",
		Mode:       ModeAgent,
		SessionDir: normalDir,
	}
	normal.AddMessage(llm.Message{Role: llm.RoleUser, Content: "hello"})
	if err := fs.Save(normal); err != nil {
		t.Fatal(err)
	}

	// Scheduler-like session id prefix.
	schedID := "sched_deadbeef"
	schedDir, err := fs.EnsureLayout(schedID)
	if err != nil {
		t.Fatal(err)
	}
	sched := &State{
		ID:         schedID,
		CWD:        "/tmp/unit",
		Mode:       ModeAgent,
		SessionDir: schedDir,
	}
	sched.AddMessage(llm.Message{Role: llm.RoleUser, Content: "ignore me"})
	if err := fs.Save(sched); err != nil {
		t.Fatal(err)
	}

	rows, err := fs.ListSnapshots("", false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 visible snapshot, got %d", len(rows))
	}
	if rows[0].SessionID != normalID {
		t.Fatalf("expected %s, got %s", normalID, rows[0].SessionID)
	}
}

func TestFilterSnapshotListForSearchMatchesFirstUserNotTitle(t *testing.T) {
	root := t.TempDir()
	fs := &FileStore{Root: root}

	makeSess := func(id, title, firstUserContent string, prefixRoles []llm.Message) error {
		dir, err := fs.EnsureLayout(id)
		if err != nil {
			return err
		}
		st := &State{
			ID:         id,
			CWD:        "/tmp",
			Mode:       ModeAgent,
			SessionDir: dir,
		}
		st.SetTitlePinned(title)
		msgs := append(append([]llm.Message{}, prefixRoles...), llm.Message{Role: llm.RoleUser, Content: firstUserContent})
		for _, m := range msgs {
			st.AddMessage(m)
		}
		return fs.Save(st)
	}

	if err := makeSess("sess_a", "Alpha topic", "unique zebra finder", nil); err != nil {
		t.Fatal(err)
	}
	if err := makeSess("sess_b", "Other", "nothing special", nil); err != nil {
		t.Fatal(err)
	}

	rows, err := fs.ListSnapshots("", false)
	if err != nil {
		t.Fatal(err)
	}
	filtered, err := fs.FilterSnapshotListForSearch(rows, "zebra")
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].SessionID != "sess_a" {
		t.Fatalf("want sess_a only, got %+v", filtered)
	}
}

func TestFilterSnapshotListAssistantPrefixNoFirstUserSkippedForMessageMatch(t *testing.T) {
	root := t.TempDir()
	fs := &FileStore{Root: root}

	id := "sess_assist_only"
	dir, err := fs.EnsureLayout(id)
	if err != nil {
		t.Fatal(err)
	}
	st := &State{
		ID:         id,
		CWD:        "/tmp",
		Mode:       ModeAgent,
		SessionDir: dir,
	}
	st.SetTitlePinned("gamma title plain")
	st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "hidden needle in assistant"})
	if err := fs.Save(st); err != nil {
		t.Fatal(err)
	}

	rows, err := fs.ListSnapshots("", false)
	if err != nil {
		t.Fatal(err)
	}
	for _, needle := range []string{"needle", "hidden"} {
		filtered, err := fs.FilterSnapshotListForSearch(rows, needle)
		if err != nil {
			t.Fatal(err)
		}
		if len(filtered) != 0 {
			t.Fatalf("q=%q expected no rows (no user message); got %+v", needle, filtered)
		}
	}
	filteredGamma, err := fs.FilterSnapshotListForSearch(rows, "gamma")
	if err != nil {
		t.Fatal(err)
	}
	if len(filteredGamma) != 1 || filteredGamma[0].SessionID != id {
		t.Fatalf("title match gamma: %+v", filteredGamma)
	}
}

func TestFilterSnapshotFirstUserAfterSystemIgnoredForSecondUser(t *testing.T) {
	root := t.TempDir()
	fs := &FileStore{Root: root}

	id := "sess_order"
	dir, err := fs.EnsureLayout(id)
	if err != nil {
		t.Fatal(err)
	}
	st := &State{
		ID:         id,
		CWD:        "/tmp",
		Mode:       ModeAgent,
		SessionDir: dir,
	}
	st.SetTitlePinned("")
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "first hello"})
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "second unique xyzzy"})
	if err := fs.Save(st); err != nil {
		t.Fatal(err)
	}
	rows, err := fs.ListSnapshots("", false)
	if err != nil {
		t.Fatal(err)
	}
	got, err := fs.FilterSnapshotListForSearch(rows, "xyzzy")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("second user must not match, got %+v", got)
	}
	got2, err := fs.FilterSnapshotListForSearch(rows, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if len(got2) != 1 {
		t.Fatalf("first user hello: %+v", got2)
	}
}

func TestSavePreservesUpdatedAtWhenMessagesAndActivitySeqUnchanged(t *testing.T) {
	root := t.TempDir()
	fs := &FileStore{Root: root}
	id := "sess_preserve_ut"
	dir, err := fs.EnsureLayout(id)
	if err != nil {
		t.Fatal(err)
	}
	st := &State{
		ID:         id,
		CWD:        "/tmp",
		Mode:       ModeAgent,
		SessionDir: dir,
	}
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "hi"})
	st.RestoreActivityFromSnapshot(1, 0)
	if err := fs.Save(st); err != nil {
		t.Fatal(err)
	}
	snap1, err := fs.ReadSnapshot(id)
	if err != nil {
		t.Fatal(err)
	}
	ut1 := snap1.Meta.UpdatedAt
	if ut1 == "" {
		t.Fatal("expected updatedAt after first save")
	}
	if snap1.Meta.ActivitySeq != 1 || snap1.Meta.ReadActivitySeq != 0 {
		t.Fatalf("meta %+v", snap1.Meta)
	}
	st.MarkActivityReadSynced()
	if err := fs.PatchSessionMetaActivitySync(st); err != nil {
		t.Fatal(err)
	}
	snap2, err := fs.ReadSnapshot(id)
	if err != nil {
		t.Fatal(err)
	}
	if snap2.Meta.ReadActivitySeq != 1 {
		t.Fatalf("read seq %+v", snap2.Meta)
	}
	if snap2.Meta.UpdatedAt != ut1 {
		t.Fatalf("mark read only: updatedAt changed from %q to %q", ut1, snap2.Meta.UpdatedAt)
	}
	time.Sleep(1200 * time.Millisecond)
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "bye"})
	if err := fs.Save(st); err != nil {
		t.Fatal(err)
	}
	snap3, err := fs.ReadSnapshot(id)
	if err != nil {
		t.Fatal(err)
	}
	if snap3.Meta.UpdatedAt == ut1 {
		t.Fatal("expected updatedAt to change after new user message")
	}
}

func TestConcurrentPatchSessionMetaActivitySync(t *testing.T) {
	root := t.TempDir()
	fs := &FileStore{Root: root}
	id := "sess_concur_meta"
	dir, err := fs.EnsureLayout(id)
	if err != nil {
		t.Fatal(err)
	}
	st := &State{
		ID:         id,
		CWD:        "/tmp",
		Mode:       ModeAgent,
		SessionDir: dir,
	}
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "hi"})
	st.RestoreActivityFromSnapshot(10, 0)
	if err := fs.Save(st); err != nil {
		t.Fatal(err)
	}
	const n = 64
	var wg sync.WaitGroup
	wg.Add(n)
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		go func(k int) {
			defer wg.Done()
			stLocal := &State{
				ID:         id,
				CWD:        "/tmp",
				Mode:       ModeAgent,
				SessionDir: dir,
			}
			stLocal.RestoreActivityFromSnapshot(uint64(10+k), uint64(k))
			if err := fs.PatchSessionMetaActivitySync(stLocal); err != nil {
				errCh <- fmt.Errorf("k=%d: %w", k, err)
			}
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
	snap, err := fs.ReadSnapshot(id)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Meta.ActivitySeq < 10 {
		t.Fatalf("activitySeq=%d", snap.Meta.ActivitySeq)
	}
}

// A hydrated @mention turn carries the file body as <coddy_attachment> XML; the
// derived title must show only what the user typed.
func TestDeriveSessionTitleStripsAttachmentBlocks(t *testing.T) {
	st := &State{ID: "sess_title_att", CWD: "/tmp", Mode: ModeAgent}
	st.AddMessage(llm.Message{
		Role: llm.RoleUser,
		Content: "@Dockerfile:21-31 почему медленно?\n\n" +
			"<coddy_attachment path=\"Dockerfile\" name=\"Dockerfile\" lines=\"21-31\">\n" +
			"<![CDATA[RUN go mod download]]>\n</coddy_attachment>",
	})
	if got := deriveSessionTitle(st); got != "@Dockerfile:21-31 почему медленно?" {
		t.Fatalf("got %q", got)
	}
}

func TestFileStoreResolveSessionID(t *testing.T) {
	root := t.TempDir()
	store := &FileStore{Root: root}
	for _, id := range []string{"sess_alpha_one", "sess_alpha_two", "sess_beta"} {
		if _, err := store.EnsureLayout(id); err != nil {
			t.Fatal(err)
		}
	}
	// A folder without session.json is not a session.
	if err := os.MkdirAll(filepath.Join(root, "sess_gamma_stray"), 0o755); err != nil {
		t.Fatal(err)
	}

	if got, err := store.ResolveSessionID("sess_beta"); err != nil || got != "sess_beta" {
		t.Fatalf("exact: %q, %v", got, err)
	}
	if got, err := store.ResolveSessionID("sess_b"); err != nil || got != "sess_beta" {
		t.Fatalf("unique prefix: %q, %v", got, err)
	}
	if got, err := store.ResolveSessionID("sess_alpha_o"); err != nil || got != "sess_alpha_one" {
		t.Fatalf("longer unique prefix: %q, %v", got, err)
	}
	if _, err := store.ResolveSessionID("sess_alpha"); err == nil || !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous prefix: %v", err)
	}
	if _, err := store.ResolveSessionID("sess_gamma"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("stray folder must not resolve: %v", err)
	}
	if _, err := store.ResolveSessionID("  "); err == nil {
		t.Fatal("blank id must fail")
	}
}

func TestStripCoddyAttachmentXML(t *testing.T) {
	raw := "@Dockerfile:21-31 why slow?\n\n<coddy_attachment path=\"Dockerfile\" name=\"Dockerfile\" lines=\"21-31\">\n<![CDATA[FROM x]]>\n</coddy_attachment>"
	if got := stripCoddyAttachmentXML(raw); got != "@Dockerfile:21-31 why slow?\n\n" {
		t.Fatalf("got %q", got)
	}
}

func TestStripCoddyAttachmentXMLMultipleBlocks(t *testing.T) {
	raw := "a<coddy_attachment path=\"x\">\n<![CDATA[1]]>\n</coddy_attachment>b<coddy_attachment path=\"y\">\n<![CDATA[2]]>\n</coddy_attachment>c"
	if got := stripCoddyAttachmentXML(raw); got != "abc" {
		t.Fatalf("got %q", got)
	}
}

// The file body sits in CDATA, so a closing tag or a "]]>" inside the file must
// not end the block early and leak the rest into a title.
func TestStripCoddyAttachmentXMLIgnoresTagsInsideCDATA(t *testing.T) {
	// What internal/agent wrapXMLCDATA renders for a body holding "]]>" and the closing tag.
	raw := "ask\n\n<coddy_attachment path=\"trap.txt\" name=\"trap.txt\">\n" +
		"<![CDATA[first ]]]]><![CDATA[> then </coddy_attachment> SECRET]]>\n</coddy_attachment>\ntail"
	if got := stripCoddyAttachmentXML(raw); got != "ask\n\n\ntail" {
		t.Fatalf("got %q", got)
	}
	// An unterminated block is left alone rather than eaten.
	open := "ask <coddy_attachment path=\"x\">\n<![CDATA[body]]>"
	if got := stripCoddyAttachmentXML(open); got != open {
		t.Fatalf("got %q", got)
	}
}

// A session is often remembered by the checkout it was about rather than by
// whatever it ended up being called, so the working directory is searchable too.
func TestFilterSnapshotListForSearchMatchesCWD(t *testing.T) {
	root := t.TempDir()
	fs := &FileStore{Root: root}

	makeSess := func(id, title, cwd string) error {
		dir, err := fs.EnsureLayout(id)
		if err != nil {
			return err
		}
		st := &State{ID: id, CWD: cwd, Mode: ModeAgent, SessionDir: dir}
		st.SetTitlePinned(title)
		st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "nothing to see here"})
		return fs.Save(st)
	}

	if err := makeSess("sess_a", "Alpha", "/storage/Repository/coddy/coddy-agent"); err != nil {
		t.Fatal(err)
	}
	if err := makeSess("sess_b", "Beta", "/home/pasha/other-project"); err != nil {
		t.Fatal(err)
	}

	rows, err := fs.ListSnapshots("", false)
	if err != nil {
		t.Fatal(err)
	}
	filtered, err := fs.FilterSnapshotListForSearch(rows, "coddy-agent")
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].SessionID != "sess_a" {
		t.Fatalf("searching by working directory should find sess_a alone, got %+v", filtered)
	}

	// The match is case-insensitive like the others.
	filtered, err = fs.FilterSnapshotListForSearch(rows, "OTHER-PROJECT")
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].SessionID != "sess_b" {
		t.Fatalf("case-insensitive cwd search should find sess_b alone, got %+v", filtered)
	}
}

// TestListSnapshotsMatchesWorkspaceSpelledDifferently covers the ACP
// session/list filter: the console stores the logical $PWD spelling of a
// symlinked checkout while an editor sends the physical one, and both name the
// same folder (coddy-project/coddy-agent VS Code "sees no sessions" report).
func TestListSnapshotsMatchesWorkspaceSpelledDifferently(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "real", "project")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(filepath.Join(root, "real"), link); err != nil {
		t.Skipf("symlink: %v", err)
	}
	fs := &FileStore{Root: filepath.Join(root, "sessions")}
	dir, err := fs.EnsureLayout("sess_link")
	if err != nil {
		t.Fatal(err)
	}
	st := &State{ID: "sess_link", CWD: filepath.Join(link, "project"), Mode: ModeAgent, SessionDir: dir}
	if err := fs.Save(st); err != nil {
		t.Fatal(err)
	}
	for _, filter := range []string{
		real,                                 // the physical path
		real + string(filepath.Separator),    // a trailing separator
		filepath.Join(real, "..", "project"), // an unclean path
		filepath.Join(link, "project"),       // the stored spelling
	} {
		rows, err := fs.ListSnapshots(filter, false)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 || rows[0].SessionID != "sess_link" {
			t.Fatalf("filter %q: rows = %+v, want the session stored under %q", filter, rows, st.CWD)
		}
	}
	other := filepath.Join(root, "real", "other")
	if err := os.MkdirAll(other, 0o755); err != nil {
		t.Fatal(err)
	}
	rows, err := fs.ListSnapshots(other, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("filter %q: rows = %+v, want none", other, rows)
	}
}

// The creation stamp is written once, when the bundle directory appears, and is
// carried forward by every later save so the session management table can sort
// by age.
func TestCreatedAtIsStampedOnceAndSurvivesSaves(t *testing.T) {
	root := t.TempDir()
	fs := &FileStore{Root: root}
	id := "sess_created_ut"
	dir, err := fs.EnsureLayout(id)
	if err != nil {
		t.Fatal(err)
	}
	first, err := fs.ReadSnapshot(id)
	if err != nil {
		t.Fatal(err)
	}
	created := first.Meta.CreatedAt
	if created == "" {
		t.Fatal("EnsureLayout left the bundle without a createdAt")
	}

	st := &State{ID: id, CWD: "/tmp", Mode: ModeAgent, SessionDir: dir}
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "hi"})
	if err := fs.Save(st); err != nil {
		t.Fatal(err)
	}
	st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "hello"})
	if err := fs.Save(st); err != nil {
		t.Fatal(err)
	}
	snap, err := fs.ReadSnapshot(id)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Meta.CreatedAt != created {
		t.Fatalf("createdAt moved from %q to %q", created, snap.Meta.CreatedAt)
	}

	// Patching only the activity counters rewrites session.json; the stamp must
	// come through that path too.
	st.RestoreActivityFromSnapshot(1, 0)
	st.MarkActivityReadSynced()
	if err := fs.PatchSessionMetaActivitySync(st); err != nil {
		t.Fatal(err)
	}
	snap, err = fs.ReadSnapshot(id)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Meta.CreatedAt != created {
		t.Fatalf("activity patch dropped createdAt: %q", snap.Meta.CreatedAt)
	}
}

// A bundle written by an older build has a session.json without createdAt. The
// moment it was started is not recoverable, so a later save must leave the
// field empty rather than backdate the session to that save.
func TestSaveDoesNotInventCreatedAtForALegacyBundle(t *testing.T) {
	root := t.TempDir()
	fs := &FileStore{Root: root}
	id := "sess_legacy_ut"
	dir, err := fs.EnsureLayout(id)
	if err != nil {
		t.Fatal(err)
	}
	// Rewrite session.json the way an older build left it.
	legacy := SessionMeta{Version: sessionFileLayout, ID: id, UpdatedAt: "2020-01-01T00:00:00Z"}
	if err := writeJSONAtomic(filepath.Join(dir, sessionMetaFile), legacy); err != nil {
		t.Fatal(err)
	}

	st := &State{ID: id, CWD: "/tmp", Mode: ModeAgent, SessionDir: dir}
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "hi"})
	if err := fs.Save(st); err != nil {
		t.Fatal(err)
	}
	snap, err := fs.ReadSnapshot(id)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Meta.CreatedAt != "" {
		t.Fatalf("createdAt = %q, want empty for a legacy bundle", snap.Meta.CreatedAt)
	}
}

// The listing carries what the management table renders per row without
// reopening any bundle: the model override, the transcript size and the stamps.
func TestListSnapshotsCarriesRowStatistics(t *testing.T) {
	root := t.TempDir()
	fs := &FileStore{Root: root}
	id := "sess_rowstats_ut"
	dir, err := fs.EnsureLayout(id)
	if err != nil {
		t.Fatal(err)
	}
	st := &State{ID: id, CWD: t.TempDir(), Mode: ModeAgent, SessionDir: dir}
	st.SetSelectedModelID("openai/gpt-4o")
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "ask"})
	st.AddMessage(llm.Message{Role: llm.RoleAssistant, Content: "answer"})
	if err := fs.Save(st); err != nil {
		t.Fatal(err)
	}
	rows, err := fs.ListSnapshotsWith(ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if row.Model != "openai/gpt-4o" {
		t.Fatalf("model = %q", row.Model)
	}
	if row.MessageCount != 2 {
		t.Fatalf("messageCount = %d, want 2", row.MessageCount)
	}
	if row.CreatedAt == "" {
		t.Fatal("row carries no createdAt")
	}
}

// Persisting a session rewrote the whole history on every state change, so the
// cost of a save followed the length of the conversation rather than what had
// moved in it. These tests hold a save to the size of the change.

// buildHistory returns n messages of a realistic shape.
func buildHistory(n int) []llm.Message {
	msgs := make([]llm.Message, 0, n)
	for i := 0; i < n; i++ {
		switch i % 3 {
		case 0:
			msgs = append(msgs, llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("question %d about the repository layout", i)})
		case 1:
			msgs = append(msgs, llm.Message{
				Role:      llm.RoleAssistant,
				Content:   fmt.Sprintf("answer %d with some detail worth persisting", i),
				ToolCalls: []llm.ToolCall{{ID: fmt.Sprintf("call_%d", i), Name: "read_file", InputJSON: `{"path":"internal/session/filesystem.go"}`}},
			})
		default:
			msgs = append(msgs, llm.Message{Role: llm.RoleTool, ToolCallID: fmt.Sprintf("call_%d", i-1), Content: strings.Repeat("file line\n", 40)})
		}
	}
	return msgs
}

func savedState(t *testing.T, id string, n int) (*FileStore, *State) {
	t.Helper()
	fs := &FileStore{Root: t.TempDir()}
	dir, err := fs.EnsureLayout(id)
	if err != nil {
		t.Fatal(err)
	}
	st := &State{ID: id, CWD: "/tmp", Mode: ModeAgent, SessionDir: dir}
	st.ReplaceMessagesWithoutPersist(buildHistory(n))
	if err := fs.Save(st); err != nil {
		t.Fatal(err)
	}
	return fs, st
}

// Appending a message must not re-encode the conversation behind it. Encoding
// a long history is slow rather than allocation-heavy (1.27 ms against 0.03 ms
// for 400 messages), so the work is asserted by how many messages the encoder
// had to touch rather than by time or allocations.
func TestAppendingEncodesOnlyTheNewMessages(t *testing.T) {
	msgs := buildHistory(400)

	whole, encoded, err := encodeMessagesFile(msgs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if encoded != 400 {
		t.Fatalf("encoding from nothing touched %d messages, want the whole history", encoded)
	}

	base, _, err := encodeMessagesFile(msgs[:398], nil)
	if err != nil {
		t.Fatal(err)
	}
	grown, encoded, err := encodeMessagesFile(msgs, &persistedMessages{count: 398, bytes: base})
	if err != nil {
		t.Fatal(err)
	}
	if encoded != 2 {
		t.Fatalf("appending two messages to a 398-message history encoded %d of them", encoded)
	}
	if !bytes.Equal(whole, grown) {
		t.Fatalf("the incremental encoding differs from the full one")
	}
}

// A history that was edited rather than extended cannot be built on, and the
// encoder must notice rather than splice onto stale bytes.
func TestAnEditedHistoryIsEncodedInFull(t *testing.T) {
	msgs := buildHistory(20)
	base, _, err := encodeMessagesFile(msgs, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Same length: there is no new tail to append, so the whole history is
	// encoded again.
	edited := append([]llm.Message(nil), msgs...)
	edited[3].Content = "rewritten"
	out, encoded, err := encodeMessagesFile(edited, &persistedMessages{count: 20, bytes: base})
	if err != nil {
		t.Fatal(err)
	}
	if encoded != 20 {
		t.Fatalf("an edited history encoded %d messages, want all of them", encoded)
	}
	want, _, err := encodeMessagesFile(edited, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out, want) {
		t.Fatalf("an edited history was not encoded as a full encoding")
	}
}

// Content the previous bytes cannot be built on is refused rather than spliced
// into something malformed.
func TestSpliceRefusesContentItDoesNotRecognise(t *testing.T) {
	if _, ok := spliceMessages([]byte("{\n  \"version\": 1,\n  \"messages\": []\n}\n"), buildHistory(1)); ok {
		t.Fatalf("spliced onto an empty history instead of refusing")
	}
	if _, ok := spliceMessages([]byte("garbage"), buildHistory(1)); ok {
		t.Fatalf("spliced onto unrecognised content instead of refusing")
	}
}

// A save that changes nothing must not rewrite the history at all, and must
// leave updatedAt alone. The byte comparison this used to rely on compared a
// compact encoding against the indented file and so never matched.
func TestSaveThatChangesNothingKeepsUpdatedAtAndTheFile(t *testing.T) {
	fs, st := savedState(t, "sess_nochange", 12)
	first, err := fs.ReadSnapshot("sess_nochange")
	if err != nil {
		t.Fatal(err)
	}
	msgPath := filepath.Join(st.SessionDir, messagesFile)
	before, err := os.Stat(msgPath)
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(1100 * time.Millisecond)
	if err := fs.Save(st); err != nil {
		t.Fatal(err)
	}

	second, err := fs.ReadSnapshot("sess_nochange")
	if err != nil {
		t.Fatal(err)
	}
	if second.Meta.UpdatedAt != first.Meta.UpdatedAt {
		t.Fatalf("updatedAt moved on a save that changed nothing: %q -> %q", first.Meta.UpdatedAt, second.Meta.UpdatedAt)
	}
	after, err := os.Stat(msgPath)
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatalf("messages.json was rewritten by a save that changed nothing")
	}
	if len(second.Messages) != 12 {
		t.Fatalf("history came back with %d messages", len(second.Messages))
	}
}

// However the file is produced, it must be byte-for-byte what a plain full
// encoding would have written: the incremental path may not drift from the
// format every other reader expects.
func TestPersistedHistoryMatchesAFullEncoding(t *testing.T) {
	fs, st := savedState(t, "sess_format", 5)
	msgPath := filepath.Join(st.SessionDir, messagesFile)

	check := func(stage string) {
		t.Helper()
		onDisk, err := os.ReadFile(msgPath)
		if err != nil {
			t.Fatal(err)
		}
		want, err := json.MarshalIndent(messagesFileData{Version: messagesLayout, Messages: st.GetMessages()}, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, '\n')
		if !bytes.Equal(onDisk, want) {
			t.Fatalf("%s: persisted history is not a full encoding\n--- on disk ---\n%s\n--- want ---\n%s", stage, onDisk, want)
		}
	}
	check("initial")

	for i := 0; i < 3; i++ {
		st.AddMessage(llm.Message{Role: llm.RoleUser, Content: fmt.Sprintf("appended %d", i)})
		if err := fs.Save(st); err != nil {
			t.Fatal(err)
		}
		check(fmt.Sprintf("append %d", i))
	}

	// An edit in the middle of the history is not an append and must still
	// land correctly.
	st.AddMessage(llm.Message{Role: llm.RoleAssistant, PlanDocument: &llm.PlanDocumentSnapshot{Slug: "plan", Name: "before"}})
	if err := fs.Save(st); err != nil {
		t.Fatal(err)
	}
	st.MarkPlanDocumentDiscarded("plan")
	if err := fs.Save(st); err != nil {
		t.Fatal(err)
	}
	check("after an in-place edit")

	// A wholesale replacement (compaction, restore) too.
	st.ReplaceMessagesWithoutPersist(buildHistory(3))
	if err := fs.Save(st); err != nil {
		t.Fatal(err)
	}
	check("after a replacement")

	// A store that never wrote this session - the next process - has nothing to
	// build on and must fall back to encoding the history in full.
	fresh := &FileStore{Root: fs.Root}
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "after a restart"})
	if err := fresh.Save(st); err != nil {
		t.Fatal(err)
	}
	check("from a store with no memory of the session")
}

// The store skips rewriting a history it believes is already on disk, so it
// must notice when that file is no longer there.
func TestSaveRewritesAHistoryThatVanishedFromDisk(t *testing.T) {
	fs, st := savedState(t, "sess_vanished", 6)
	msgPath := filepath.Join(st.SessionDir, messagesFile)
	if err := os.Remove(msgPath); err != nil {
		t.Fatal(err)
	}
	if err := fs.Save(st); err != nil {
		t.Fatal(err)
	}
	snap, err := fs.ReadSnapshot("sess_vanished")
	if err != nil {
		t.Fatalf("history was not written back: %v", err)
	}
	if len(snap.Messages) != 6 {
		t.Fatalf("history came back with %d messages", len(snap.Messages))
	}
}
