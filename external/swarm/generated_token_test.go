//go:build swarm

package swarm

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// A relay's generated token reaches its operator through a file only they can
// read. Stderr shows it only on a terminal: `coddy serve` in the background
// sends stderr to logs/serve.log and under systemd to the journal, both of
// which outlive the run.
func TestGeneratedTokenStaysOutOfTheLogs(t *testing.T) {
	for name, terminal := range map[string]bool{"a terminal": true, "a log file": false} {
		t.Run(name, func(t *testing.T) {
			home := t.TempDir()
			var logged, stderr bytes.Buffer
			log := slog.New(slog.NewTextHandler(&logged, nil))
			announceGeneratedToken(home, "s3cret-token", log, &stderr, terminal)

			if strings.Contains(logged.String(), "s3cret-token") {
				t.Fatalf("the log holds the token: %s", logged.String())
			}
			if got := strings.Contains(stderr.String(), "s3cret-token"); got != terminal {
				t.Fatalf("stderr holds the token = %v, want %v: %s", got, terminal, stderr.String())
			}
			path := filepath.Join(home, generatedTokenFile)
			if !strings.Contains(logged.String(), path) {
				t.Fatalf("the log does not name the file: %s", logged.String())
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if strings.TrimSpace(string(data)) != "s3cret-token" {
				t.Fatalf("the file holds %q", data)
			}
			if runtime.GOOS != "windows" {
				if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
					t.Fatalf("file mode = %v, %v; want 0600", st.Mode().Perm(), err)
				}
			}
		})
	}
}
