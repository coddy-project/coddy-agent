//go:build http

package httpserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func TestSessionListScanSelection(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "sess_example")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(map[string]any{"version": 1, "id": "sess_example", "pinned": true})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "session.json"), b, 0644); err != nil {
		t.Fatal(err)
	}
	fs := &session.FileStore{Root: root}
	base := session.ListOptions{Archived: session.ArchiveExclude, IncludeEmpty: true}
	tests := []struct {
		name   string
		mutate func(*session.ListOptions)
		scans  int
		ids    []string
	}{
		{"history", func(*session.ListOptions) {}, 1, []string{"sess_example"}},
		{"subagents", func(o *session.ListOptions) { o.IncludeSubagents = true }, 1, []string{"sess_example"}},
		{"archived", func(o *session.ListOptions) { o.Archived = session.ArchiveAll }, 2, []string{"sess_example"}},
		{"cwd", func(o *session.ListOptions) { o.CWD = root }, 2, nil},
		{"tags", func(o *session.ListOptions) { o.Tags = []string{"work"} }, 2, nil},
		{"origin", func(o *session.ListOptions) { o.Origin = session.OriginPrint }, 2, nil},
		{"print", func(o *session.ListOptions) { o.IncludePrintRuns = true }, 2, []string{"sess_example"}},
		{"scheduler", func(o *session.ListOptions) { o.IncludeSchedulerRuns = true }, 2, []string{"sess_example"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := base
			tt.mutate(&opts)
			calls := 0
			var gotOpts []session.ListOptions
			scan := func(o session.ListOptions) ([]session.SessionListEntry, error) {
				calls++
				gotOpts = append(gotOpts, o)
				return fs.ListSnapshotsWith(o)
			}
			rows, history, err := listSessionRows(scan, opts)
			if err != nil {
				t.Fatal(err)
			}
			var gotIDs []string
			for _, row := range rows {
				gotIDs = append(gotIDs, row.SessionID)
			}
			if !reflect.DeepEqual(gotIDs, tt.ids) {
				t.Fatalf("page IDs = %v, want %v", gotIDs, tt.ids)
			}
			global := session.ListOptions{Archived: session.ArchiveExclude, IncludeEmpty: true, IncludeSubagents: true}
			wantOpts := []session.ListOptions{global}
			if tt.scans == 2 {
				wantOpts = append(wantOpts, opts)
			}
			if !reflect.DeepEqual(gotOpts, wantOpts) {
				t.Fatalf("scan options = %#v, want %#v", gotOpts, wantOpts)
			}
			if len(history) != 1 {
				t.Fatalf("global history has %d rows, want 1", len(history))
			}
			if calls != tt.scans {
				t.Fatalf("scans = %d, want %d", calls, tt.scans)
			}
		})
	}
}

func TestSessionListScanErrors(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "sess_example")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "session.json"), []byte(`{"version":1,"id":"sess_example","pinned":true}`), 0644); err != nil {
		t.Fatal(err)
	}
	fs := &session.FileStore{Root: root}
	sentinel := errors.New("scan failed")
	for _, failAt := range []int{1, 2} {
		t.Run(fmt.Sprint(failAt), func(t *testing.T) {
			calls := 0
			scan := func(opts session.ListOptions) ([]session.SessionListEntry, error) {
				calls++
				rows, err := fs.ListSnapshotsWith(opts)
				if err != nil {
					return nil, err
				}
				if len(rows) != 1 {
					t.Fatalf("fixture scan returned %d rows", len(rows))
				}
				if calls == failAt {
					return nil, sentinel
				}
				return rows, nil
			}
			_, _, err := listSessionRows(scan, session.ListOptions{Archived: session.ArchiveAll, IncludeEmpty: true})
			if !errors.Is(err, sentinel) || calls != failAt {
				t.Fatalf("error %v, calls %d", err, calls)
			}
		})
	}
}
