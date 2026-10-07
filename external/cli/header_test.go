//go:build cli

package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

// The [Context] section lists what the session's prompt carries, and an
// instructions.files entry naming a file the session cannot read is listed
// there too, with the reason, instead of simply being absent from the list:
// several agents sharing one set of instructions by an absolute path is the
// case where a missing mount or a permission would otherwise go unnoticed.
func TestHeaderContextNamesUnreadInstructionFiles(t *testing.T) {
	cwd, shared := t.TempDir(), t.TempDir()
	readable := filepath.Join(shared, "house-style.md")
	if err := os.WriteFile(readable, []byte("HOUSE STYLE"), 0o644); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(shared, "infrastructure.md")
	cfg := &config.Config{Paths: config.Paths{Home: t.TempDir(), CWD: cwd}}
	cfg.Instructions.Files = []string{readable, missing, "docs/STYLE.md"}

	read, unread := standingContextFiles(cfg, cwd)
	if len(read) != 1 || !strings.HasSuffix(read[0], "house-style.md") {
		t.Fatalf("read = %q, want the shared file", read)
	}
	if len(unread) != 1 || !strings.Contains(unread[0], "infrastructure.md") || !strings.HasSuffix(unread[0], "(not read: does not exist)") {
		t.Fatalf("unread = %q, want the missing absolute entry with its reason", unread)
	}

	h := newHeader(newTheme("dark"))
	h.SetSections(read, unread, nil, 0, nil)
	rows := renderedRows(h, 200)
	if !rows["[Context]"] || !rows[unread[0]] || !rows[read[0]] {
		t.Fatalf("the header does not list both files under [Context]:\n%v", rows)
	}

	// Only unread files: the section is still there to say so.
	h.SetSections(nil, unread, nil, 0, nil)
	if rows := renderedRows(h, 200); !rows["[Context]"] || !rows[unread[0]] {
		t.Fatalf("an unread file alone is not shown:\n%v", rows)
	}
}
