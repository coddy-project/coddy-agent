package tools_test

import (
	"path/filepath"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/tools"
)

func TestToolPathsEscapeCWD(t *testing.T) {
	cwd := "/home/proj/workspace"
	tests := []struct {
		tool     string
		argsJSON string
		want     bool
	}{
		{"read_file", `{"path":"README.md"}`, false},
		{"read_file", `{"path":"/etc/hosts"}`, true},
		{"read_file", `{"path":"../sibling/file.txt"}`, true},
		{"write_file", `{"path":"/tmp/x","content":""}`, true},
		{"list_dir", `{}`, false},
		{"list_dir", `{"path":"."}`, false},
		{"list_dir", `{"path":"/var/log"}`, true},
		{"search_files", `{"pattern":"foo"}`, false},
		{"search_files", `{"pattern":"foo","path":"src"}`, false},
		{"search_files", `{"pattern":"foo","path":"/usr"}`, true},
		{"run_command", `{"command":"rm -rf /"}`, false},
	}
	for _, tt := range tests {
		got := tools.ToolPathsEscapeCWD(tt.tool, tt.argsJSON, cwd)
		if got != tt.want {
			t.Errorf("%s %q: got %v want %v", tt.tool, tt.argsJSON, got, tt.want)
		}
	}
}

func TestPathEscapesCWD_insideNested(t *testing.T) {
	tmp := t.TempDir()
	inside := filepath.Join(tmp, "nested", "file.txt")
	if tools.PathEscapesCWD(inside, tmp) {
		t.Errorf("path under cwd should not escape: %s", inside)
	}
}

func TestPathEscapesCWD_absoluteOutside(t *testing.T) {
	tmp := t.TempDir()
	if !tools.PathEscapesCWD("/etc", tmp) {
		t.Error("expected absolute path outside cwd to count as escape")
	}
}
