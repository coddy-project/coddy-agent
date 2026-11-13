package webauth

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/platform"
)

func TestReplayGuardDamagedEntries(t *testing.T) {
	sum := sha256.Sum256([]byte("launch"))
	key := fmt.Sprintf("%x", sum)
	for name, content := range map[string]string{"null expiry": fmt.Sprintf(`{"%s":null}`, key), "zero expiry": fmt.Sprintf(`{"%s":"0001-01-01T00:00:00Z"}`, key), "bad digest": `{"bad":"2030-01-01T00:00:00Z"}`, "uppercase digest": fmt.Sprintf(`{"%s":"2030-01-01T00:00:00Z"}`, strings.ToUpper(key))} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "record")
			if err := os.WriteFile(path, []byte(content), 0600); err != nil {
				t.Fatal(err)
			}
			var g ReplayGuard
			g.SetFile(path)
			now := time.Unix(1000, 0)
			if ok, err := g.Use("launch", now.Add(time.Hour), now); ok || err == nil {
				t.Fatalf("accepted=%v err=%v", ok, err)
			}
			if err := os.WriteFile(path, []byte(`{}`), 0600); err != nil {
				t.Fatal(err)
			}
			if ok, err := g.Use("launch", now.Add(time.Hour), now); !ok || err != nil {
				t.Fatalf("recovery: %v %v", ok, err)
			}
		})
	}
}
func TestReplayGuardDirectorySyncFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "record")
	var g ReplayGuard
	g.SetFile(path)
	now := time.Unix(1000, 0)
	restore := platform.InjectDirSyncFault()
	ok, err := g.Use("launch", now.Add(time.Hour), now)
	restore()
	if ok || !errors.Is(err, platform.ErrWriteNotDurable) {
		t.Fatalf("accepted=%v err=%v", ok, err)
	}
	if data, err := os.ReadFile(path); err != nil || !strings.Contains(string(data), fmt.Sprintf("%x", sha256.Sum256([]byte("launch")))) {
		t.Fatalf("committed data=%s err=%v", data, err)
	}
	if ok, err := g.Use("launch", now.Add(time.Hour), now); ok || err != nil {
		t.Fatalf("duplicate accepted=%v err=%v", ok, err)
	}
	if ok, err := g.Use("new", now.Add(time.Hour), now); !ok || err != nil {
		t.Fatalf("recovery accepted=%v err=%v", ok, err)
	}
}
