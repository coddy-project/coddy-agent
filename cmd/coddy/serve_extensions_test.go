package main

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/extensions"
)

// extArchive builds a package ZIP the store accepts: a manifest.json of the
// given id and an entry script of the given bytes. The bytes are what an
// approval is for, so two builds of one id differ in digest.
func extArchive(t *testing.T, id, entryBody string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	for name, body := range map[string]string{
		"manifest.json": `{"api":1,"id":"` + id + `","name":"` + id + `","version":"1.0","ui":{"entry":"main.js"}}`,
		"main.js":       entryBody,
	} {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func extFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func extDigest(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// The flag collects one path per occurrence - an embedding names several
// packages - and typedServeFlags replays it that way for the daemon, because
// the flag package visits a flag once however often the operator typed it.
func TestAddExtensionFlagIsRepeatable(t *testing.T) {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	paths := addExtensionFlag(fs)
	if err := fs.Parse([]string{"--add-extension", "a.zip", "--add-extension=b.zip"}); err != nil {
		t.Fatal(err)
	}
	if got, want := []string(*paths), []string{"a.zip", "b.zip"}; !slices.Equal(got, want) {
		t.Fatalf("--add-extension collected %q, want %q", got, want)
	}
	if got, want := typedServeFlags(fs), []string{"-add-extension=a.zip", "-add-extension=b.zip"}; !slices.Equal(got, want) {
		t.Fatalf("typedServeFlags replayed %q, want %q", got, want)
	}
}

// A package named on the command line is installed and enabled before the
// server serves.
func TestInstallStartupExtensionsInstallsAndEnables(t *testing.T) {
	store := extensions.NewStore(t.TempDir())
	zipBytes := extArchive(t, "theme-lab", "export function activate() {}")
	pkg := extFile(t, t.TempDir(), "theme-lab.zip", zipBytes)
	var out bytes.Buffer
	installStartupExtensions(store, []string{pkg}, &out)

	rows, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("installed %d packages, want 1: %+v", len(rows), rows)
	}
	if rows[0].ID != "theme-lab" || !rows[0].Enabled {
		t.Fatalf("installed %+v, want theme-lab enabled", rows[0])
	}
	if rows[0].SHA256 != extDigest(zipBytes) {
		t.Fatalf("installed under digest %.12s, want %.12s", rows[0].SHA256, extDigest(zipBytes))
	}
	if !strings.Contains(out.String(), "installed and enabled") {
		t.Fatalf("no confirmation of the install in %q", out.String())
	}
}

// The same id and digest already installed and enabled is left exactly as it
// is - and the same file named twice in one command line changes nothing the
// second time. The same package installed but disabled is enabled.
func TestInstallStartupExtensionsIsIdempotent(t *testing.T) {
	store := extensions.NewStore(t.TempDir())
	zipBytes := extArchive(t, "demo", "export {}")
	pkg := extFile(t, t.TempDir(), "demo.zip", zipBytes)

	var out bytes.Buffer
	installStartupExtensions(store, []string{pkg}, &out)
	out.Reset()
	installStartupExtensions(store, []string{pkg}, &out)
	if out.Len() != 0 {
		t.Fatalf("the same id and digest again printed %q, want silence", out.String())
	}
	rows, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != "demo" || !rows[0].Enabled {
		t.Fatalf("after the repeat: %+v, want one enabled demo", rows)
	}

	if _, err := store.SetEnabled("demo", extDigest(zipBytes), false); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	installStartupExtensions(store, []string{pkg}, &out)
	if !strings.Contains(out.String(), "enabled extension") {
		t.Fatalf("a disabled package was not enabled: %q", out.String())
	}
	rows, err = store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || !rows[0].Enabled {
		t.Fatalf("after the enable: %+v, want one enabled demo", rows)
	}
}

// An approval is for the exact bytes: the same id under another digest is
// kept, and the new file is skipped with a warning naming the conflict.
func TestInstallStartupExtensionsNeverReplacesADifferentDigest(t *testing.T) {
	store := extensions.NewStore(t.TempDir())
	approved := extArchive(t, "demo", "export function activate() { return 1 }")
	approvedPath := extFile(t, t.TempDir(), "demo-approved.zip", approved)
	var out bytes.Buffer
	installStartupExtensions(store, []string{approvedPath}, &out)

	other := extArchive(t, "demo", "export function activate() { return 2 }")
	otherPath := extFile(t, t.TempDir(), "demo-other.zip", other)
	out.Reset()
	installStartupExtensions(store, []string{otherPath}, &out)

	warn := out.String()
	if !strings.Contains(warn, "warning: --add-extension "+otherPath) || !strings.Contains(warn, "different digest") || !strings.Contains(warn, `"demo"`) {
		t.Fatalf("the conflict was not warned about clearly: %q", warn)
	}
	rows, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].SHA256 != extDigest(approved) || !rows[0].Enabled {
		t.Fatalf("the approved package was replaced or disabled: %+v", rows)
	}
}

// A missing file and a file that is not a package warn and the startup goes on
// to the packages that do work - and when every file fails the handler still
// returns, so the server runs and the exit code stays 0.
func TestInstallStartupExtensionsWarnsAndContinues(t *testing.T) {
	store := extensions.NewStore(t.TempDir())
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.zip")
	bad := extFile(t, dir, "bad.zip", []byte("not a zip"))
	good := extFile(t, dir, "good.zip", extArchive(t, "good", "export {}"))

	var out bytes.Buffer
	installStartupExtensions(store, []string{missing, bad, good}, &out)
	warn := out.String()
	if !strings.Contains(warn, "warning: --add-extension "+missing) {
		t.Fatalf("the missing file was not warned about: %q", warn)
	}
	if !strings.Contains(warn, "warning: --add-extension "+bad) {
		t.Fatalf("the invalid package was not warned about: %q", warn)
	}
	rows, err := store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].ID != "good" || !rows[0].Enabled {
		t.Fatalf("the startup did not continue past the bad files: %+v", rows)
	}

	out.Reset()
	installStartupExtensions(store, []string{missing, bad}, &out)
	if !strings.Contains(out.String(), "warning: --add-extension "+missing) ||
		!strings.Contains(out.String(), "warning: --add-extension "+bad) {
		t.Fatalf("every file failing still needs both warnings: %q", out.String())
	}
	rows, err = store.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("a failed file installed something: %+v", rows)
	}
}
