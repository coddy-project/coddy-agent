package fs

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestToolCallPaths(t *testing.T) {
	cwd := filepath.Join(string(filepath.Separator)+"proj", "repo")
	abs := func(parts ...string) string {
		p, err := filepath.Abs(filepath.Join(append([]string{cwd}, parts...)...))
		if err != nil {
			t.Fatal(err)
		}
		return p
	}

	cases := []struct {
		name string
		tool string
		args string
		want []string
	}{
		{"read", "read", `{"path":"internal/agent/react.go"}`, []string{abs("internal", "agent", "react.go")}},
		{"glob", "glob", `{"pattern":"**/*.go","path":"internal"}`, []string{abs("internal")}},
		{"grep", "grep", `{"pattern":"func","path":"internal/rules"}`, []string{abs("internal", "rules")}},
		{"print_tree", "print_tree", `{"path":"docs","depth":2}`, []string{abs("docs")}},
		{"edit", "edit", `{"path":"a.go","oldString":"x","newString":"y"}`, []string{abs("a.go")}},
		{"write", "write", `{"path":"a.go","content":"x"}`, []string{abs("a.go")}},
		{"apply_patch", "apply_patch", `{"path":"a.go","patch":"..."}`, []string{abs("a.go")}},
		{"mkdir", "mkdir", `{"path":"newdir"}`, []string{abs("newdir")}},
		{"rmdir", "rmdir", `{"path":"olddir"}`, []string{abs("olddir")}},
		{"touch", "touch", `{"path":"a.go"}`, []string{abs("a.go")}},
		{"rm", "rm", `{"path":"a.go"}`, []string{abs("a.go")}},
		{"mv both ends", "mv", `{"src":"a/x.go","dst":"b/x.go"}`, []string{abs("a", "x.go"), abs("b", "x.go")}},
		{"mv src only", "mv", `{"src":"a/x.go"}`, []string{abs("a", "x.go")}},

		// glob and grep declare path as optional.
		{"glob without path", "glob", `{"pattern":"**/*.go"}`, nil},
		{"grep with blank path", "grep", `{"pattern":"func","path":"   "}`, nil},

		// Not filesystem tools.
		{"run_command", "run_command", `{"command":"go test ./internal/rules"}`, nil},
		{"mcp tool", "server__do_thing", `{"path":"a.go"}`, nil},
		{"unknown tool", "coddy_todo_write", `{"path":"a.go"}`, nil},

		// Malformed or missing input.
		{"malformed json", "read", `{"path":`, nil},
		{"empty args", "read", ``, nil},
		{"missing path", "read", `{}`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ToolCallPaths(tc.tool, tc.args, cwd)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("ToolCallPaths(%q, %q) = %v, want %v", tc.tool, tc.args, got, tc.want)
			}
		})
	}
}

func TestToolCallPathsKeepsAbsoluteAndNeedsCWD(t *testing.T) {
	absPath, err := filepath.Abs(filepath.Join(string(filepath.Separator)+"elsewhere", "x.go"))
	if err != nil {
		t.Fatal(err)
	}
	got := ToolCallPaths("read", `{"path":`+quote(absPath)+`}`, filepath.Join(string(filepath.Separator)+"proj", "repo"))
	if len(got) != 1 || got[0] != absPath {
		t.Fatalf("absolute path not preserved: %v, want [%s]", got, absPath)
	}
	if got := ToolCallPaths("read", `{"path":"a.go"}`, ""); got != nil {
		t.Fatalf("without cwd a relative path cannot be resolved, got %v", got)
	}
}

// quote renders s as a JSON string literal (paths carry backslashes on Windows).
func quote(s string) string {
	var b []byte
	b = append(b, '"')
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' || s[i] == '"' {
			b = append(b, '\\')
		}
		b = append(b, s[i])
	}
	return string(append(b, '"'))
}
