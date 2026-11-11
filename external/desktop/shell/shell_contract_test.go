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
		`if (row.permissionPending) return "permission";`,
		`if (row.questionPending) return "question";`,
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
