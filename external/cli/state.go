//go:build cli

package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

// consoleState is the console's own remembered choices. It lives in a small
// file under CODDY_HOME so each surface (console, web, telegram) carries its
// own memory instead of sharing the configured default.
type consoleState struct {
	LastModel string `json:"last_model,omitempty"`
}

func consoleStatePath(home string) string {
	return filepath.Join(home, "console-state.json")
}

// loadConsoleState reads the state file; a missing or corrupt file reads as
// empty (the console then falls back to the alphabetical-first pick).
func loadConsoleState(home string) consoleState {
	var st consoleState
	if strings.TrimSpace(home) == "" {
		return st
	}
	data, err := os.ReadFile(consoleStatePath(home))
	if err != nil {
		return st
	}
	_ = json.Unmarshal(data, &st)
	return st
}

// saveConsoleState writes the state file atomically (tmp + rename), the way
// the other <home> state files are written.
func saveConsoleState(home string, st consoleState) {
	if strings.TrimSpace(home) == "" {
		return
	}
	path := consoleStatePath(home)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	data, err := json.Marshal(st)
	if err != nil {
		return
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".console-state-*.tmp")
	if err != nil {
		return
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return
	}
	if err := tmp.Close(); err != nil {
		return
	}
	_ = os.Rename(tmpPath, path)
}

// pickInitialModel chooses the model a freshly created session starts on:
// the surface's remembered pick when the session still offers it, else the
// alphabetically first offered id (the surface's very first pick).
func pickInitialModel(stored string, offered []string) string {
	stored = strings.TrimSpace(stored)
	for _, id := range offered {
		if id == stored && id != "" {
			return stored
		}
	}
	ids := append([]string(nil), offered...)
	sort.Strings(ids)
	for _, id := range ids {
		if strings.TrimSpace(id) != "" {
			return id
		}
	}
	return ""
}

// rememberModel records the model the operator picked on this console, so the
// next new session starts on it. Turn-scoped and flag-scoped choices never
// reach here: a pick is remembered only when it changes the session's model.
func (a *App) rememberModel(id string) {
	id = strings.TrimSpace(id)
	if id == "" {
		return
	}
	home := a.config().Paths.Home
	st := loadConsoleState(home)
	if st.LastModel == id {
		return
	}
	st.LastModel = id
	saveConsoleState(home, st)
}

// applyInitialModel stamps the console's initial model on a session that was
// just created. It is only ever called on a proven-new session (never on a
// resumed or reopened one): the remembered pick, or the alphabetical first on
// the console's first use.
func (a *App) applyInitialModel(res *acp.SessionNewResult) {
	if res == nil {
		return
	}
	offered, current := modelOptionValues(res.ConfigOptions)
	initial := pickInitialModel(loadConsoleState(a.config().Paths.Home).LastModel, offered)
	if initial == "" || initial == current {
		return
	}
	sessionID := res.SessionID
	// A worker, like setModel: the change is written to the session bundle,
	// and JoinWorkers lets that write finish before the process exits.
	a.workers.Add(1)
	go func() {
		defer a.workers.Done()
		if _, err := a.mgr.HandleSessionSetConfigOption(context.Background(), acp.SessionSetConfigOptionParams{
			SessionID: sessionID, ConfigID: "model", Value: initial,
		}); err != nil {
			_ = a.Sender().SendSessionUpdate(sessionID, statusErr{msg: "model: " + err.Error()})
		}
	}()
}

// modelOptionValues extracts the "model" config option's offered values and
// current value from a session/new result - the list the new session itself
// advertises, which is what a remote console must pick from too.
func modelOptionValues(opts []acp.ConfigOption) (values []string, current string) {
	for _, opt := range opts {
		if opt.ID != "model" {
			continue
		}
		for _, v := range opt.Options {
			values = append(values, v.Value)
		}
		return values, opt.CurrentValue
	}
	return nil, ""
}
