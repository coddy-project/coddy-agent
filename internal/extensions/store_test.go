package extensions

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func archive(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for name, body := range files {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func fixture(t *testing.T) []byte {
	return archive(t, map[string]string{"manifest.json": `{"api":1,"id":"demo","name":"Demo","version":"1.0","ui":{"entry":"main.js","styles":["style.css"]}}`, "main.js": "export function activate(coddy) {}", "style.css": ".demo {}"})
}

func TestLifecycle(t *testing.T) {
	home := t.TempDir()
	s := NewStore(home)
	row, err := s.Install(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if row.Enabled {
		t.Fatal("installation executed code")
	}
	if _, err := s.Asset("demo", row.SHA256, "main.js"); err == nil {
		t.Fatal("disabled asset served")
	}
	if _, err := s.SetEnabled("demo", "stale", true); err == nil {
		t.Fatal("stale approval accepted")
	}
	if _, err := s.SetEnabled("demo", row.SHA256, true); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(home).Asset("demo", row.SHA256, "main.js"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Install(fixture(t)); err == nil {
		t.Fatal("existing package overwritten")
	}
	if _, err := s.SetEnabled("demo", row.SHA256, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Asset("demo", row.SHA256, "main.js"); err == nil {
		t.Fatal("disabled asset served")
	}
	if err := s.Remove("demo"); err != nil {
		t.Fatal(err)
	}
	rows, err := s.List()
	if err != nil || len(rows) != 0 {
		t.Fatalf("list: %v %v", rows, err)
	}
}

func TestArchiveValidation(t *testing.T) {
	for _, name := range []string{"..", "../escape.js", "/absolute.js", `a\b.js`, "A.js", "a.js"} {
		t.Run(name, func(t *testing.T) {
			files := map[string]string{"manifest.json": `{"api":1,"id":"demo","name":"Demo","version":"1","ui":{"entry":"main.js"}}`, "main.js": "export {}", name: "payload", "a.JS": "duplicate"}
			if _, err := NewStore(t.TempDir()).Install(archive(t, files)); err == nil {
				t.Fatal("unsafe archive accepted")
			}
		})
	}
}

func TestChangedBytesNeedApproval(t *testing.T) {
	home := t.TempDir()
	s := NewStore(home)
	row, err := s.Install(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetEnabled("demo", row.SHA256, true); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "extensions", "demo.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = bytes.Replace(data, []byte(row.SHA256), []byte("invalid"), 1)
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Asset("demo", row.SHA256, "main.js"); err == nil {
		t.Fatal("changed package served")
	}
}
