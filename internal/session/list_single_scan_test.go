package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeBundle(t *testing.T, dir string, meta map[string]any, messages int) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(meta)
	if err := os.WriteFile(filepath.Join(dir, sessionMetaFile), b, 0o644); err != nil {
		t.Fatal(err)
	}
	msgs := make([]map[string]string, 0, messages)
	for i := 0; i < messages; i++ {
		msgs = append(msgs, map[string]string{"role": "user", "content": "hi"})
	}
	b, _ = json.Marshal(map[string]any{"version": 1, "messages": msgs})
	if err := os.WriteFile(filepath.Join(dir, messagesFile), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// One scan with IncludeSubagents, its subagent rows dropped, is the listing
// without them: the same rows in the same order, whatever the bundles hold.
func TestWithoutSubagentRunsIsTheListingWithoutSubagents(t *testing.T) {
	root := t.TempDir()
	cwd := t.TempDir()
	at := func(day int) string {
		return "2026-10-" + string(rune('0'+day/10)) + string(rune('0'+day%10)) + "T10:00:00Z"
	}
	writeBundle(t, filepath.Join(root, "sess_plain"), map[string]any{"version": 1, "id": "sess_plain", "cwd": cwd, "updatedAt": at(10), "messageCount": 2}, 2)
	writeBundle(t, filepath.Join(root, "sess_empty"), map[string]any{"version": 1, "id": "sess_empty", "cwd": cwd, "updatedAt": at(11)}, 0)
	writeBundle(t, filepath.Join(root, "sess_archived"), map[string]any{"version": 1, "id": "sess_archived", "cwd": cwd, "updatedAt": at(12), "archived": true, "messageCount": 1}, 1)
	writeBundle(t, filepath.Join(root, "sess_toplevel_sub"), map[string]any{"version": 1, "id": "sess_toplevel_sub", "cwd": cwd, "updatedAt": at(13), "subagentRun": true, "messageCount": 1}, 1)
	writeBundle(t, filepath.Join(root, "sess_parent"), map[string]any{"version": 1, "id": "sess_parent", "cwd": cwd, "updatedAt": at(14), "messageCount": 1}, 1)
	writeBundle(t, filepath.Join(root, "sess_parent", ChildSessionsDirName, "sess_child"), map[string]any{"version": 1, "id": "sess_child", "cwd": cwd, "updatedAt": at(15), "subagentRun": true, "parentSessionId": "sess_parent", "messageCount": 1}, 1)
	writeBundle(t, filepath.Join(root, "sess_parent", ChildSessionsDirName, "sess_unmarked"), map[string]any{"version": 1, "id": "sess_unmarked", "cwd": cwd, "updatedAt": at(16), "messageCount": 1}, 1)
	writeBundle(t, filepath.Join(root, "sess_same_time_a"), map[string]any{"version": 1, "id": "sess_same_time_a", "cwd": cwd, "updatedAt": at(17), "messageCount": 1}, 1)
	writeBundle(t, filepath.Join(root, "sess_same_time_b"), map[string]any{"version": 1, "id": "sess_same_time_b", "cwd": cwd, "updatedAt": at(17), "messageCount": 1}, 1)

	fs := &FileStore{Root: root}
	want, err := fs.ListSnapshotsWith(ListOptions{IncludeEmpty: true})
	if err != nil {
		t.Fatal(err)
	}
	full, err := fs.ListSnapshotsWith(ListOptions{IncludeSubagents: true, IncludeEmpty: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(full) <= len(want) {
		t.Fatalf("the fixture has no subagent rows: full %d, normal %d", len(full), len(want))
	}
	if got := WithoutSubagentRuns(full); !reflect.DeepEqual(got, want) {
		t.Fatalf("rows differ:\n got  %v\n want %v", ids(got), ids(want))
	}
}

func ids(rows []SessionListEntry) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.SessionID
	}
	return out
}
