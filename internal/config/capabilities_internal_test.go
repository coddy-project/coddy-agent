package config

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
)

// models[model=X]/multimodal and /allow_reasoning_off are set, cleared and read
// through the staged-edit grammar like the other tri-state keys: a set writes
// the value (a false included), a delete returns the row to "absent".
func TestUCISetAndClearTheCapabilityKeys(t *testing.T) {
	paths := testPathConfig(t, `providers:
  - name: far
    type: coddy
    api_base: http://127.0.0.1:1
models:
  - model: far/coder
agent:
  model: far/coder
`)
	read := func() *ModelEntry {
		t.Helper()
		cfg, err := LoadWithPaths(paths)
		if err != nil {
			t.Fatal(err)
		}
		e := cfg.FindModelEntry("far/coder")
		if e == nil {
			t.Fatal("row lost")
		}
		return e
	}
	// The document holds the three states: a loaded row keeps multimodal as a
	// plain boolean, so only the file tells an absent key from a written false.
	// allow_reasoning_off keeps the difference on the row too
	// (AllowReasoningOffExplicit), and the two must agree.
	state := func(key, path string) string {
		t.Helper()
		got, err := ReadConfigPath(paths, path)
		if err != nil {
			t.Fatal(err)
		}
		file := "absent"
		if got.Exists {
			file = fmt.Sprint(got.Value)
		}
		e := read()
		if key == "allow_reasoning_off" {
			row := "absent"
			if e.AllowReasoningOffExplicit() {
				row = strconv.FormatBool(e.AllowReasoningOff)
			}
			if row != file {
				t.Fatalf("%s: the file says %s, the loaded row %s", path, file, row)
			}
		} else if e.Multimodal != (file == "true") {
			t.Fatalf("%s: the file says %s, the loaded row multimodal=%v", path, file, e.Multimodal)
		}
		return file
	}

	for _, key := range []string{"multimodal", "allow_reasoning_off"} {
		path := "models[model=far/coder]." + key
		if got := state(key, path); got != "absent" {
			t.Fatalf("%s before any command: %s", key, got)
		}
		for _, step := range []struct{ line, want string }{
			{"set " + path + "=true", "true"},
			{"set " + path + "=false", "false"},
		} {
			if _, err := CommitUCICommands(paths, mustParseUCI(t, step.line)); err != nil {
				t.Fatalf("%s: %v", step.line, err)
			}
			if got := state(key, path); got != step.want {
				t.Fatalf("%s: key is %s, want %s", step.line, got, step.want)
			}
		}
		got, err := ReadConfigPath(paths, path)
		if err != nil || !got.Exists || got.Value != false {
			t.Fatalf("ReadConfigPath(%s) = %#v, %v: a written false must read back as false", path, got, err)
		}
		if _, err := CommitUCICommands(paths, mustParseUCI(t, "delete "+path)); err != nil {
			t.Fatalf("delete %s: %v", path, err)
		}
		if got := state(key, path); got != "absent" {
			t.Fatalf("delete %s left the key %s, want absent", path, got)
		}
	}
}

// coddy -t accepts null for both keys, as it does for stream, and still refuses
// a value that is not a boolean.
func TestCheckAcceptsNullCapabilityKeys(t *testing.T) {
	rep := checkYAML(t, withModeline(`providers:
  - name: far
    type: coddy
    api_base: http://127.0.0.1:1
models:
  - model: far/coder
    multimodal: null
    allow_reasoning_off: null
  - model: far/vision
    multimodal: true
    allow_reasoning_off: false
agent:
  model: far/coder
`))
	if !rep.Valid() || len(rep.Findings) != 0 {
		t.Fatalf("null and boolean keys must produce no findings, got %+v", rep.Findings)
	}

	rep = checkYAML(t, withModeline(`providers:
  - name: far
    type: coddy
    api_base: http://127.0.0.1:1
models:
  - model: far/coder
    multimodal: maybe
agent:
  model: far/coder
`))
	errs := errorsOf(rep)
	if len(errs) == 0 || !strings.Contains(errs[0].Path, "multimodal") {
		t.Fatalf("a non-boolean multimodal must be an error at models[].multimodal, got %+v", rep.Findings)
	}
}
