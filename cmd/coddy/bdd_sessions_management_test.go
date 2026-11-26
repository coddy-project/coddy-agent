package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cucumber/godog"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

func saveManagedSession(store *session.FileStore, id, dir string, archived bool) error {
	if dir == "" {
		var err error
		dir, err = store.EnsureLayout(id)
		if err != nil {
			return err
		}
	}
	st := &session.State{ID: id, SessionDir: dir, CWD: store.Root, Mode: session.ModeAsk, Archived: archived}
	st.AddMessage(llm.Message{Role: llm.RoleUser, Content: "hello"})
	return store.Save(st)
}

func TestSessionsManagementFeature(t *testing.T) {
	suite := godog.TestSuite{ScenarioInitializer: func(sc *godog.ScenarioContext) {
		var root, method, target, input string
		var out, diagnostics bytes.Buffer
		sc.Step(`^the CLI has a saved session "([^"]+)"$`, func(id string) error {
			root, input, method, target = filepath.Join(t.TempDir(), "sessions"), "", "", ""
			return saveManagedSession(&session.FileStore{Root: root}, id, "", false)
		})
		sc.Step(`^I answer yes at the terminal$`, func() { input = "yes\n" })
		sc.Step(`^I run sessions "([^"]+)"$`, func(line string) error {
			out.Reset()
			diagnostics.Reset()
			return (sessionsCommand{out: &out, errOut: &diagnostics, in: strings.NewReader(input), interactive: input != "", mutate: func(_ context.Context, _, gotRoot, id, verb string, patch map[string]any) (json.RawMessage, error) {
				if gotRoot != root {
					return nil, fmt.Errorf("mutation root = %q, want %q", gotRoot, root)
				}
				method, target = verb, id
				if patch == nil {
					patch = map[string]any{}
				}
				patch["id"] = id
				return json.Marshal(patch)
			}}).run(append(strings.Fields(line), "--sessions-dir", root))
		})
		sc.Step(`^the JSON list contains "([^"]+)"$`, func(id string) error {
			var rows []struct {
				ID string `json:"id"`
			}
			if err := json.Unmarshal(out.Bytes(), &rows); err != nil {
				return err
			}
			if len(rows) != 1 || rows[0].ID != id {
				return fmt.Errorf("unexpected list: %s", &out)
			}
			return nil
		})
		sc.Step(`^the JSON object has "([^"]+)" equal to "([^"]+)"$`, func(key, value string) error {
			var obj map[string]any
			if err := json.Unmarshal(out.Bytes(), &obj); err != nil {
				return err
			}
			if obj[key] != value {
				return fmt.Errorf("expected %s=%q in %s", key, value, &out)
			}
			return nil
		})
		sc.Step(`^the manager receives "([^"]+)" for "([^"]+)"$`, func(verb, id string) error {
			if method != verb || target != id {
				return fmt.Errorf("manager received %s %q, want %s %q", method, target, verb, id)
			}
			return nil
		})
		sc.Step(`^the output reports deletion of "([^"]+)"$`, func(id string) error {
			if !strings.Contains(out.String(), "Deleted session "+id) || !strings.Contains(diagnostics.String(), "[y/N]") {
				return fmt.Errorf("unexpected stdout %q or prompt %q", &out, &diagnostics)
			}
			return nil
		})
	}, Options: &godog.Options{Format: "pretty", Paths: []string{"../../features/session_management_cli.feature"}, TestingT: t}}
	t.Setenv("CODDY_HOME", t.TempDir())
	t.Setenv("CODDY_CONFIG", "")
	t.Setenv("CODDY_SESSION_ID", "")
	if suite.Run() != 0 {
		t.Fatal("sessions management feature failed")
	}
}

func TestSessionsManagementHelp(t *testing.T) {
	if err := runSessions([]string{"show", "--help"}); err != nil {
		t.Fatal(err)
	}
}
