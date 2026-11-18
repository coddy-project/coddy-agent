# Closing the Desktop Window to the Tray Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make the desktop shell's close button minimise to a visible tray when enabled, while preserving normal close and explicit quit behavior.

**Architecture:** Add a close-request operation to `WindowHandle`, where the attached settings and tray are known. Let `SettingsStore` depend on a small tray interface that reports visibility, and let the shell call the close-request operation through an interface assertion without changing its routes or payloads.

**Tech Stack:** Go, godog/Gherkin, existing desktop test doubles, Markdown documentation.

---

### Task 1: Specify close behavior at the shell boundary

**Files:**
- Modify: `features/desktop_window.feature`
- Modify: `external/desktop/bdd_desktop_window_test.go`
- Modify: `external/desktop/export_test.go`

- [ ] **Step 1: Add recording tray and close-request test support**

Add a test-only tray implementation with `Show`, `Hide` and `Visible` methods. Add a `SettingsStore` helper that binds a window and that tray together. Extend the existing scenario state with the tray and the last close-action result.

```go
type TrayPresence struct{ visible bool }

func NewTrayPresence(visible bool) *TrayPresence { return &TrayPresence{visible: visible} }
func (t *TrayPresence) Show() error              { t.visible = true; return nil }
func (t *TrayPresence) Hide()                    { t.visible = false }
func (t *TrayPresence) Visible() bool            { return t.visible }
```

- [ ] **Step 2: Write failing BDD scenarios**

Add scenarios with these observable steps:

```gherkin
Scenario: Closing the shell minimises to a visible tray
  Given a desktop window with a visible tray icon
  When the operator closes the window from the shell
  Then the window is minimised
  And the window is not closed

Scenario: Closing the shell without a tray ends the window
  Given a desktop window without a tray icon
  When the operator closes the window from the shell
  Then the window is closed once
  And the window is not minimised
```

Bind `MenuWindow` to `WindowHandle`, attach `SettingsStore`, serve `shell.Handler(handle)`, and POST `{"action":"close"}` to `/desktop/window`. Assert the existing recording window's `Hidden()` and `Closed()` counters.

- [ ] **Step 3: Run the new scenarios and verify red**

Run:

```bash
go test -tags=desktop ./external/desktop -run '^TestDesktopWindowFeature$' -count=1
```

Expected: the visible-tray scenario fails because the current close action closes the window rather than minimising it.

Keep the red tests uncommitted until the implementation passes the repository's pre-commit lint gate.

### Task 2: Implement close-to-tray and preserve explicit quit

**Files:**
- Modify: `external/desktop/window.go`
- Modify: `external/desktop/settings.go`
- Modify: `external/desktop/shell/shell.go`
- Modify: `external/desktop/tray_linux.go`
- Modify: `external/desktop/tray_other.go`
- Test: `external/desktop/window_close_test.go`

- [ ] **Step 1: Write failing unit tests for fallback and quit**

Create external-package tests that bind a recording window, settings and `TrayPresence`. Cover a saved tray setting with an invisible tray and explicit quit with a visible tray:

```go
func TestCloseRequestWithAnUnavailableTrayCloses(t *testing.T) {
    // Given Tray=true and Visible=false, RequestClose must call Close once
    // and must not call Minimise.
}

func TestQuitWithAVisibleTrayCloses(t *testing.T) {
    // Given Tray=true and Visible=true, PickMenu(shell.MenuQuit) must call
    // Close once and must not call Minimise.
}
```

The quit test needs the existing test-only quit binding to accept a recording function. No real D-Bus connection is used.

- [ ] **Step 2: Run unit tests and verify red**

Run:

```bash
go test -tags=desktop ./external/desktop -run 'TestCloseRequestWithAnUnavailableTrayCloses|TestQuitWithAVisibleTrayCloses' -count=1
```

Expected: compilation fails because `RequestClose` and tray visibility do not exist yet.

- [ ] **Step 3: Add tray visibility behind the existing store**

Define the store's dependency:

```go
type trayControl interface {
    Show() error
    Hide()
    Visible() bool
}
```

Change the store field and `bind` parameter to `trayControl`. Implement `Visible` on the Linux tray by returning `t != nil && t.conn != nil` under its mutex. Implement `Visible() bool { return false }` on the unsupported tray. Add:

```go
func (s *SettingsStore) canCloseToTray() bool {
    if s == nil {
        return false
    }
    s.mu.Lock()
    tray, enabled := s.tray, s.cur.Tray
    s.mu.Unlock()
    return enabled && tray != nil && tray.Visible()
}
```

- [ ] **Step 4: Add the close-request operation**

Add to `WindowHandle`:

```go
func (h *WindowHandle) RequestClose() error {
    h.mu.Lock()
    settings := h.settings
    h.mu.Unlock()
    if settings != nil && settings.canCloseToTray() {
        return h.Minimise()
    }
    return h.Close()
}
```

In `serveWindowAction`, handle `close` through a local interface:

```go
type closeRequester interface { RequestClose() error }

case "close":
    if requester, ok := ctl.(closeRequester); ok {
        err = requester.RequestClose()
    } else {
        err = ctl.Close()
    }
```

Leave `SettingsStore.PickMenu(shell.MenuQuit)` on `win.Close()`.

- [ ] **Step 5: Run the focused tests and verify green**

Run:

```bash
go test -tags=desktop ./external/desktop/... -count=1
```

Expected: all desktop and shell tests pass, including both new BDD scenarios and both unit tests.

- [ ] **Step 6: Commit the implementation**

```bash
git add external/desktop
git commit -m "feat(desktop): close the window to a visible tray"
```

### Task 3: Document and verify the change

**Files:**
- Modify: `docs/surfaces/desktop.md`
- Modify: `docs/ru/surfaces/desktop.md`
- Modify: `docs/plans/desktop-close-to-tray.md` only if implementation evidence requires correcting the design record's planned test shape

- [ ] **Step 1: Update both desktop guides**

State that the shell's close button minimises when the tray setting is enabled and visible, closes the application otherwise, and never replaces **Quit Coddy**. Keep the macOS icon report and system-frame close behavior out of scope. Restamp only after the Russian translation has been reviewed as the repository requires.

- [ ] **Step 2: Run generation and documentation checks**

Run:

```bash
make docs
make docs-check
```

Expected: both commands exit 0 with no stale generated file or translation stamp.

- [ ] **Step 3: Run the full verification gate**

Run:

```bash
make test
make lint
```

Expected: both commands exit 0. If the environment lacks Node.js, install the documented toolchain first or report the exact blocked command rather than claiming a pass.

- [ ] **Step 4: Cross-review and verify findings**

Create a brief for the uncommitted diff or the branch against `main`, including the intent and excluding generated assets. Run the configured `/crossreview` roster. Verify every finding against the code and tests, apply only confirmed fixes, and run the focused tests again after any fix.

- [ ] **Step 5: Commit documentation and verified review fixes**

```bash
git add docs external/desktop features
git commit -m "docs(desktop): describe closing the window to the tray"
```
