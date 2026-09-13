package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const utf8BOMLiteral = "\ufeff"

// windowsText is what an editor on Windows leaves behind.
func windowsText(body string) string { return strings.ReplaceAll(body, "\n", "\r\n") }

func TestNormalizeConfigSourceDropsCarriageReturnsAndTheByteOrderMark(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"unix text is left alone", "a: 1\nb: 2\n", "a: 1\nb: 2\n"},
		{"windows line endings", "a: 1\r\nb: 2\r\n", "a: 1\nb: 2\n"},
		{"carriage returns alone", "a: 1\rb: 2\r", "a: 1\nb: 2\n"},
		{"a byte order mark in front", utf8BOMLiteral + "a: 1\n", "a: 1\n"},
		{"both at once", utf8BOMLiteral + "a: 1\r\nb: 2\r\n", "a: 1\nb: 2\n"},
		{"a mark further in stays", "a: \"x" + utf8BOMLiteral + "\"\n", "a: \"x" + utf8BOMLiteral + "\"\n"},
		{"nothing at all", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(normalizeConfigSource([]byte(tc.in))); got != tc.want {
				t.Errorf("normalizeConfigSource(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNormalizeConfigSourceKeepsTheLineCount(t *testing.T) {
	body := "# one\r\n# two\r\n\r\nagent:\r\n  max_turns: 3\r\n"
	got := string(normalizeConfigSource([]byte(body)))
	if want := strings.Count(body, "\n"); strings.Count(got, "\n") != want {
		t.Fatalf("normalization changed the line count: %d, want %d", strings.Count(got, "\n"), want)
	}
}

func TestConfigLineEndingFollowsTheFile(t *testing.T) {
	cases := []struct {
		name string
		body string
		want string
	}{
		{"unix", "a: 1\nb: 2\n", "\n"},
		{"windows", "a: 1\r\nb: 2\r\n", "\r\n"},
		{"mixed, most lines windows", "a: 1\r\nb: 2\r\nc: 3\n", "\r\n"},
		{"mixed, most lines unix", "a: 1\r\nb: 2\nc: 3\n", "\n"},
		{"no line break at all", "a: 1", "\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := configLineEnding([]byte(tc.body)); got != tc.want {
				t.Errorf("configLineEnding(%q) = %q, want %q", tc.body, got, tc.want)
			}
		})
	}
}

func TestUTF16ConfigIsNamedRatherThanParsed(t *testing.T) {
	le := append([]byte{0xFF, 0xFE}, []byte{'a', 0, ':', 0}...)
	be := append([]byte{0xFE, 0xFF}, []byte{0, 'a', 0, ':'}...)
	if got := utf16Encoding(le); got != "UTF-16 LE" {
		t.Errorf("utf16Encoding(little endian) = %q", got)
	}
	if got := utf16Encoding(be); got != "UTF-16 BE" {
		t.Errorf("utf16Encoding(big endian) = %q", got)
	}
	if got := utf16Encoding([]byte("agent:\n")); got != "" {
		t.Errorf("utf16Encoding(utf-8) = %q, want the empty string", got)
	}

	home := t.TempDir()
	path := filepath.Join(home, "config.yaml")
	if err := os.WriteFile(path, le, 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := Check(CLIPaths{Home: home, Config: path})
	if err != nil {
		t.Fatal(err)
	}
	f := onlyError(t, rep)
	if !strings.Contains(f.Message, "UTF-16") || !strings.Contains(f.Fix, "UTF-8") {
		t.Fatalf("a UTF-16 file is not named as one: %+v", f)
	}
}

func TestCheckReadsAWindowsFileLikeAnyOther(t *testing.T) {
	body := withModeline(`providers:
  - name: a
    type: openai
models:
  - model: a/b
    max_tokens: 4096
agent:
  model: a/b
`)
	rep := checkYAML(t, utf8BOMLiteral+windowsText(body))
	if !rep.Valid() || len(rep.Findings) != 0 {
		t.Fatalf("a config written on Windows is not read like any other: %+v", rep.Findings)
	}
}

func TestCheckPlacesAWindowsFindingOnTheLineTheEditorShows(t *testing.T) {
	body := withModeline("logger:\n  level: verbose\n")
	rep := checkYAML(t, windowsText(body))
	f := onlyError(t, rep)
	if f.Line != 3 {
		t.Fatalf("the finding is on line %d, the editor shows level on line 3: %+v", f.Line, f)
	}
}

func TestLoadReadsAWindowsFile(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "config.yaml")
	body := utf8BOMLiteral + windowsText(withModeline("agent:\n  max_turns: 7\n"))
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Agent.MaxTurns != 7 {
		t.Fatalf("agent.max_turns = %d, want 7", cfg.Agent.MaxTurns)
	}
}

func TestLoadNamesAUTF16File(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "config.yaml")
	raw := append([]byte{0xFF, 0xFE}, []byte{'a', 0, 'g', 0}...)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil || !strings.Contains(err.Error(), "UTF-16") {
		t.Fatalf("Load error = %v, want one naming UTF-16", err)
	}
}

// A save must not accumulate blank lines on a file an editor on Windows wrote: the
// carriage return used to stay inside every comment and the encoder wrote it back out
// as a line of its own, so each save pushed the file further apart.
func TestSaveOfAWindowsFileKeepsItsShapeAndLineEndings(t *testing.T) {
	body := windowsText(`# yaml-language-server: $schema=https://coddy.dev/config.schema.json
# Workstation setup.

agent:
  # the model this box runs
  max_turns: 3
`)
	cfg := &Config{}
	cfg.Agent.MaxTurns = 4

	out, err := MarshalConfigYAMLPreservingComments(cfg, []byte(body))
	if err != nil {
		t.Fatalf("MarshalConfigYAMLPreservingComments: %v", err)
	}
	saved := string(out)
	for _, want := range []string{"# Workstation setup.", "# the model this box runs"} {
		if !strings.Contains(saved, want) {
			t.Errorf("the save dropped %q:\n%s", want, saved)
		}
	}
	if got, want := countBlankLines(saved), countBlankLines(body); got != want {
		t.Errorf("the save turned %d blank lines into %d:\n%q", want, got, saved)
	}
	if strings.Count(saved, "\n") != strings.Count(saved, "\r\n") {
		t.Errorf("the save did not keep the Windows line endings:\n%q", saved)
	}

	// And the second save is the same file again, not a further stretched one.
	again, err := MarshalConfigYAMLPreservingComments(cfg, out)
	if err != nil {
		t.Fatalf("second MarshalConfigYAMLPreservingComments: %v", err)
	}
	if string(again) != saved {
		t.Errorf("saving twice changed the file:\n%q\n%q", saved, string(again))
	}
}

func countBlankLines(body string) int {
	n := 0
	for _, line := range strings.Split(strings.ReplaceAll(body, "\r\n", "\n"), "\n") {
		if strings.TrimSpace(line) == "" {
			n++
		}
	}
	return n
}

func TestAtomicWriteKeepsTheLineEndingsOfTheFileItReplaces(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("agent:\r\n  max_turns: 3\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := AtomicWriteConfigYAML(path, []byte("agent:\n  max_turns: 4\n")); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(raw); got != "agent:\r\n  max_turns: 4\r\n" {
		t.Fatalf("the write did not keep the Windows line endings: %q", got)
	}

	// A file that was not there yet is written the way it was rendered.
	fresh := filepath.Join(dir, "new.yaml")
	if err := AtomicWriteConfigYAML(fresh, []byte("agent:\n  max_turns: 4\n")); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile(fresh)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(raw); got != "agent:\n  max_turns: 4\n" {
		t.Fatalf("a new file was not written as rendered: %q", got)
	}
}
