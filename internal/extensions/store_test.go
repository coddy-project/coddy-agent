package extensions

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

func TestManifestPermissions(t *testing.T) {
	all := `"ui.page","ui.alter","sessions.read","sessions.assets.write","sessions.title","sessions.prompt","workspace.switch","events.publish","kv","config.read","llm.complete"`
	tooMany := "[" + strings.Repeat(`"kv",`, 32) + `"kv"` + "]"
	overlong := "[" + strings.Repeat("a", 65) + "]"
	for _, tc := range []struct {
		name  string
		array string // raw JSON array, empty for an absent key
		ok    bool
	}{
		{name: "every known scope", array: "[" + all + "]", ok: true},
		{name: "absent", ok: true},
		{name: "empty", array: "[]", ok: true},
		{name: "unknown scope", array: `["sessions.delete"]`},
		{name: "malformed scope", array: `["sessions..read"]`},
		{name: "uppercase scope", array: `["UI.Page"]`},
		{name: "empty scope", array: `[""]`},
		{name: "scope over 64 characters", array: overlong},
		{name: "more than 32 scopes", array: tooMany},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest := `{"api":1,"id":"demo","name":"Demo","version":"1","ui":{"entry":"main.js"}}`
			if tc.array != "" {
				manifest = `{"api":1,"id":"demo","name":"Demo","version":"1","permissions":` + tc.array + `,"ui":{"entry":"main.js"}}`
			}
			files := map[string]string{"manifest.json": manifest, "main.js": "export {}"}
			_, err := NewStore(t.TempDir()).Install(archive(t, files))
			if tc.ok && err != nil {
				t.Fatal(err)
			}
			if !tc.ok && err == nil {
				t.Fatal("invalid permissions accepted")
			}
		})
	}
}

func TestManifestStyles(t *testing.T) {
	for _, tc := range []struct {
		name  string
		field string
		array string
		ok    bool
	}{
		{name: "styles of the archive", field: "styles", array: `["style.css"]`, ok: true},
		{name: "compat_styles of the archive", field: "compat_styles", array: `["legacy.css"]`, ok: true},
		{name: "missing style", field: "styles", array: `["missing.css"]`},
		{name: "missing compat style", field: "compat_styles", array: `["missing.css"]`},
		{name: "non-CSS style", field: "styles", array: `["main.js"]`},
		{name: "non-CSS compat style", field: "compat_styles", array: `["main.js"]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			manifest := `{"api":1,"id":"demo","name":"Demo","version":"1","ui":{"entry":"main.js","` + tc.field + `":` + tc.array + `}}`
			files := map[string]string{"manifest.json": manifest, "main.js": "export {}", "style.css": ".demo {}", "legacy.css": ".legacy {}"}
			_, err := NewStore(t.TempDir()).Install(archive(t, files))
			if tc.ok && err != nil {
				t.Fatal(err)
			}
			if !tc.ok && err == nil {
				t.Fatal("invalid stylesheet accepted")
			}
		})
	}
}

func TestEntryJSONRoundTrip(t *testing.T) {
	home := t.TempDir()
	s := NewStore(home)
	files := map[string]string{
		"manifest.json": `{"api":1,"id":"demo","name":"Demo","version":"1","permissions":["ui.page","sessions.read"],"ui":{"entry":"main.js","styles":["style.css"],"compat_styles":["legacy.css"]}}`,
		"main.js":       "export {}", "style.css": ".demo {}", "legacy.css": ".legacy {}",
	}
	row, err := s.Install(archive(t, files))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(row.Permissions, ",") != "ui.page,sessions.read" {
		t.Fatalf("permissions: %v", row.Permissions)
	}
	if strings.Join(row.UI.CompatStyles, ",") != "legacy.css" {
		t.Fatalf("compat styles: %v", row.UI.CompatStyles)
	}
	// The declaration is stored with the package record: a fresh store lists it
	// again, beside ui.
	rows, err := NewStore(home).List()
	if err != nil || len(rows) != 1 {
		t.Fatalf("list: %v %v", rows, err)
	}
	if strings.Join(rows[0].Permissions, ",") != "ui.page,sessions.read" || strings.Join(rows[0].UI.CompatStyles, ",") != "legacy.css" {
		t.Fatalf("listed: %#v", rows[0])
	}
	// The JSON the HTTP API returns round-trips through the entry struct.
	data, err := json.Marshal(row)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{`"permissions":["ui.page","sessions.read"]`, `"compat_styles":["legacy.css"]`} {
		if !strings.Contains(string(data), field) {
			t.Fatalf("wire shape: %s", data)
		}
	}
	var back Entry
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if strings.Join(back.Permissions, ",") != "ui.page,sessions.read" || strings.Join(back.UI.CompatStyles, ",") != "legacy.css" {
		t.Fatalf("round trip: %#v", back)
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

func TestListSkipsUnreadableRecords(t *testing.T) {
	home := t.TempDir()
	s := NewStore(home)
	row, err := s.Install(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, "extensions")
	for name, body := range map[string]string{
		"notes.json":    "not a record",
		"half.json":     "{",
		"mismatch.json": `{"archive":null,"sha256":"x","enabled":false}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != row.ID {
		t.Fatalf("list: %v", rows)
	}
}

func TestReadIsNotServedFromAStaleCache(t *testing.T) {
	home := t.TempDir()
	s := NewStore(home)
	row, err := s.Install(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetEnabled("demo", row.SHA256, true); err != nil {
		t.Fatal(err)
	}
	// This read fills the derived cache. The enabled flag still has to come
	// from the record rather than from what the cache holds.
	if _, err := s.Asset("demo", row.SHA256, "main.js"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetEnabled("demo", row.SHA256, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Asset("demo", row.SHA256, "main.js"); err == nil {
		t.Fatal("a cached bundle kept serving after it was disabled")
	}
	// A record rewritten behind the store's back is picked up at once: the
	// cache is keyed by the archive bytes, never by the id alone.
	next := archive(t, map[string]string{"manifest.json": `{"api":1,"id":"demo","name":"Demo","version":"1.0","ui":{"entry":"main.js"}}`, "main.js": "export function activate(coddy) { return 1 }"})
	stored, err := json.Marshal(record{Archive: next, SHA256: digest(next), Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "extensions", "demo.json"), stored, 0600); err != nil {
		t.Fatal(err)
	}
	body, err := s.Asset("demo", digest(next), "main.js")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "return 1") {
		t.Fatalf("served the cached bytes: %q", body)
	}
	// The flag of that same record is honoured just as at once.
	stored, err = json.Marshal(record{Archive: next, SHA256: digest(next), Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "extensions", "demo.json"), stored, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Asset("demo", digest(next), "main.js"); err == nil {
		t.Fatal("a cached bundle kept serving after the record disabled it")
	}
}
