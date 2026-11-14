//go:build desktop

package shell

import (
	"strings"
	"testing"
)

func shellSource(t *testing.T, name string) string {
	t.Helper()
	b, err := assets.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func requireShellSource(t *testing.T, source string, fragments ...string) {
	t.Helper()
	for _, fragment := range fragments {
		if !strings.Contains(source, fragment) {
			t.Errorf("shell source does not contain %q", fragment)
		}
	}
}

func TestOnlyCtrlTabWalksTheTabs(t *testing.T) {
	source := shellSource(t, "shell.js")
	requireShellSource(t, source,
		`if (e.code === "Tab" && e.ctrlKey) {`,
		`step(e.shiftKey ? -1 : 1);`,
	)
	if strings.Contains(source, `e.code === "Tab" && chordModifier(e)`) {
		t.Fatal("Alt+Tab and Cmd+Tab would be taken from the window manager")
	}
}

func TestPendingAndFailedFramesStillReachTheChat(t *testing.T) {
	source := shellSource(t, "shell.js")
	if strings.Contains(source, "if (tab.workspacePending) return frame || null;") {
		t.Fatal("a pending workspace leaves the tab without a frame")
	}
	requireShellSource(t, source,
		"const FRAME_LOAD_TIMEOUT_MS =",
		"const FRAME_LOAD_RETRIES =",
		"function navigateFrame(tab, frame)",
		`frame.addEventListener("error", () => retryFrame(tab.uid, frame));`,
		`if (frame.contentWindow.location.href === "about:blank") return;`,
		`if (location.href === "about:blank") return null;`,
		`if (!hash || hash === "#") return null;`,
	)
}

func TestPendingPromptsAreQuestionMarks(t *testing.T) {
	script := shellSource(t, "shell.js")
	requireShellSource(t, script,
		`permission: "Permission required",`,
		`question: "Question pending",`,
		`if (row && row.permissionPending) return "permission";`,
		`if (row && row.questionPending) return "question";`,
		`span.textContent = "?";`,
	)
	style := shellSource(t, "shell.css")
	requireShellSource(t, style,
		".dot.is-permission,",
		".dot.is-question {",
		"rgba(196, 181, 253, 0.95)",
	)
}

func TestTurnStartsAreShownBeforeTheNextSessionPoll(t *testing.T) {
	source := shellSource(t, "shell.js")
	requireShellSource(t, source,
		"const locallyBusy = new Map();",
		"function watchTurnStarts(tabUid, frame)",
		`url.pathname === "/v1/responses"`,
		"locallyBusy.set(tabUid, Date.now())",
		"Date.now() - started >= SESSIONS_TICK_MS",
		"renderStrip();",
		"refreshSessions();",
		"locallyBusy.delete(tab.uid)",
	)
}

func TestFramelessWindowEdgesResizeByPointer(t *testing.T) {
	script := shellSource(t, "shell.js")
	requireShellSource(t, script,
		"function resizeWindowFrom(e)",
		`askWindow("resize-begin")`,
		`askWindow("resize-by", { direction, x: dx, y: dy })`,
		`askWindow("move-end")`,
	)
	markup := shellSource(t, "index.html")
	requireShellSource(t, markup,
		`id="resize-edges"`,
		`data-direction="nw"`,
		`data-direction="se"`,
	)
	style := shellSource(t, "shell.css")
	requireShellSource(t, style,
		".resize-edge {",
		"cursor: nw-resize;",
		"cursor: se-resize;",
	)
}

func TestTabsReorderByPointerWithoutChangingClicks(t *testing.T) {
	source := shellSource(t, "shell.js")
	requireShellSource(t, source,
		"function dragTabFrom(e, tab, button)",
		"function moveTab(tabUid, index)",
		`button.addEventListener("pointerdown", (e) => dragTabFrom(e, tab, button));`,
		"if (e.button !== 0 || e.target.closest",
		"Math.abs(dx) < DRAG_THRESHOLD_PX",
		"tabs.splice(from, 1)",
		"tabs.splice(index, 0, tab)",
		"save();",
		"renderStrip();",
	)
}

func TestPendingPromptsOutrankTheLocalBusyMark(t *testing.T) {
	// A prompt that waits for the operator outranks the busy mark a send just
	// set: the mark lives for the whole turn, and a waited turn is exactly
	// when the question sign has to show.
	source := shellSource(t, "shell.js")
	start := strings.Index(source, "function stateOf(tab)")
	if start < 0 {
		t.Fatal("stateOf is not in the shell source")
	}
	end := strings.Index(source[start:], "function stateDot")
	if end < 0 {
		t.Fatal("stateDot is not in the shell source")
	}
	body := source[start : start+end]
	prompt := strings.Index(body, `return "permission"`)
	busy := strings.Index(body, `locallyBusy.has(tab.uid)`)
	if prompt < 0 || busy < 0 {
		t.Fatal("stateOf no longer checks a pending prompt or the local busy mark")
	}
	if busy < prompt {
		t.Fatal("the local busy mark outranks a pending prompt, so the question sign never shows during a waited turn")
	}
}

func TestLocalBusyDiesWithoutASession(t *testing.T) {
	// A send from a fresh tab can fail before the session it would start is
	// ever created; the busy mark it set must be collected by age anyway, not
	// kept forever because tab.session is still empty.
	source := shellSource(t, "shell.js")
	requireShellSource(t, source, "if (!locallyBusy.has(tab.uid)) continue;")
	if strings.Contains(source, `if (!locallyBusy.has(tab.uid) || !tab.session) continue;`) {
		t.Fatal("a tab without a session keeps its busy mark forever")
	}
}

func TestACancelledTabDragDoesNotSwallowClicks(t *testing.T) {
	// The suppress flag exists to eat the click a finished drag produces; a
	// cancelled drag produces none, so the flag must be spent there or the
	// tab's next real click is ignored.
	source := shellSource(t, "shell.js")
	start := strings.Index(source, "const stop = (m, drop) => {")
	if start < 0 {
		t.Fatal("the drag's stop handler is not in the shell source")
	}
	body := source[start : start+900]
	if !strings.Contains(body, "suppressTabClickUid = null") {
		t.Fatal("a cancelled drag leaves suppressTabClickUid set, swallowing the next click")
	}
}

func TestManualReloadRestoresTheFrameRetryBudget(t *testing.T) {
	// The watchdog's two tries are spent by earlier failures; a manual reload
	// that leaves them spent arms a timer that cannot retry anything.
	source := shellSource(t, "shell.js")
	start := strings.Index(source, "function reloadActiveFrame()")
	if start < 0 {
		t.Fatal("reloadActiveFrame is not in the shell source")
	}
	body := source[start : start+800]
	if !strings.Contains(body, "frameLoadAttempts.set(tab.uid, 0)") {
		t.Fatal("a manual reload does not reset the frame load retry budget")
	}
}
