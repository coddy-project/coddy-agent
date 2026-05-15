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
		{"read", `{"filePath":"README.md"}`, false},
		{"read", `{"filePath":"/etc/hosts"}`, true},
		{"read", `{"filePath":"../sibling/file.txt"}`, true},
		{"write", `{"filePath":"/tmp/x","content":""}`, true},
		{"read", `{"filePath":"."}`, false},
		{"read", `{"filePath":"/var/log"}`, true},
		{"grep", `{"pattern":"foo"}`, false},
		{"grep", `{"pattern":"foo","path":"src"}`, false},
		{"grep", `{"pattern":"foo","path":"/usr"}`, true},
		{"glob", `{"pattern":"*.go"}`, false},
		{"glob", `{"pattern":"*.go","path":"/usr"}`, true},
		{"run_command", `{"command":"rm -rf /"}`, false},
		{"mkdir", `{"path":"../evil"}`, true},
		{"mv", `{"src":"a","dst":"../out"}`, true},
		{"mv", `{"src":"../a","dst":"b"}`, true},
		{"mv", `{"src":"a","dst":"b"}`, false},
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
