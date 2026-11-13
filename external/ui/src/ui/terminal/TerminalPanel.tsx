import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import { useT } from "../i18n/I18nProvider";
import { workspaceScope } from "../chat/workspaceScope";
import { composerAutoFocusAllowed } from "../chat/composerFocus";
import {
  serverSnapshotShellStack,
  snapshotShellStack,
  subscribeShellStack,
} from "../shellBreakpoint";
import {
  clampTerminalFrame,
  defaultTerminalFrame,
  moveTerminalFrame,
  readTerminalFrame,
  readTerminalMaximized,
  resizeTerminalFrame,
  TERMINAL_EDGES,
  writeTerminalFrame,
  writeTerminalMaximized,
  type TerminalEdge,
  type TerminalFrame,
} from "./terminalFrame";

type TerminalSnapshot = {
  shell: string;
  output: string;
  running: boolean;
  exit_error?: string;
};

const HISTORY_KEY = "coddy.terminal.history.";
const HISTORY_LIMIT = 100;

function historyKey(sessionId: string): string {
  return `${HISTORY_KEY}${sessionId}`;
}

function savedHistory(sessionId: string): string[] {
  try {
    const value = JSON.parse(
      localStorage.getItem(historyKey(sessionId)) || "[]",
    );
    return Array.isArray(value)
      ? value.filter((item): item is string => typeof item === "string")
      : [];
  } catch {
    return [];
  }
}

function saveHistory(sessionId: string, history: string[]): void {
  try {
    localStorage.setItem(historyKey(sessionId), JSON.stringify(history));
  } catch {
    // A private or full storage must not stop the terminal from running.
  }
}

function viewport(): { vw: number; vh: number } {
  if (typeof window === "undefined") return { vw: 1280, vh: 800 };
  return { vw: window.innerWidth, vh: window.innerHeight };
}

/**
 * A chat-scoped shell in a window of its own. The server owns the process, so
 * closing and reopening the window keeps its cwd, exports and output.
 *
 * On a desktop it is a small window the person moves and resizes, and may send
 * full screen; where it stands is kept for this browser. On the stacked shell
 * it fills the screen, because a window to drag needs a pointer and a tablet
 * or a phone has neither the room nor the precision for one.
 */
export function TerminalPanel(props: {
  sessionId: string;
  workspacePath: string;
  onClose: () => void;
}) {
  const { t } = useT();
  const stacked = useSyncExternalStore(
    subscribeShellStack,
    snapshotShellStack,
    serverSnapshotShellStack,
  );
  const [snapshot, setSnapshot] = useState<TerminalSnapshot | null>(null);
  const [command, setCommand] = useState("");
  const [history, setHistory] = useState<string[]>(() =>
    savedHistory(props.sessionId),
  );
  const [historyAt, setHistoryAt] = useState(0);
  const [error, setError] = useState("");
  const [maximized, setMaximized] = useState(readTerminalMaximized);
  const [frame, setFrame] = useState<TerminalFrame>(() => {
    const { vw, vh } = viewport();
    const saved = readTerminalFrame();
    return saved
      ? clampTerminalFrame(saved, vw, vh)
      : defaultTerminalFrame(vw, vh);
  });
  const outputRef = useRef<HTMLPreElement | null>(null);
  const inputRef = useRef<HTMLInputElement | null>(null);
  const scope = workspaceScope(props.sessionId, props.workspacePath);
  const url = `/coddy/sessions/${encodeURIComponent(props.sessionId)}/terminal`;
  // The window floats only where it can be dragged; full screen is one frame
  // the stylesheet owns, so neither the stacked shell nor a maximized window
  // is positioned from here.
  const floating = !stacked && !maximized;

  const refresh = useCallback(async () => {
    const response = await fetch(url, { headers: scope.headers });
    if (!response.ok) throw new Error(await response.text());
    setSnapshot((await response.json()) as TerminalSnapshot);
    setError("");
  }, [url, props.sessionId, props.workspacePath]);

  useEffect(() => {
    let live = true;
    const poll = async () => {
      try {
        await refresh();
      } catch (err) {
        if (live) setError(err instanceof Error ? err.message : String(err));
      }
    };
    void poll();
    const timer = window.setInterval(() => void poll(), 450);
    return () => {
      live = false;
      window.clearInterval(timer);
    };
  }, [refresh]);

  useEffect(() => {
    const el = outputRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [snapshot?.output]);

  useLayoutEffect(() => {
    if (composerAutoFocusAllowed()) inputRef.current?.focus();
  }, []);

  // A window the viewport outgrew comes back inside it rather than off screen.
  useEffect(() => {
    if (typeof window === "undefined") return;
    const onResize = () => {
      const { vw, vh } = viewport();
      setFrame((prev) => clampTerminalFrame(prev, vw, vh));
    };
    window.addEventListener("resize", onResize);
    return () => window.removeEventListener("resize", onResize);
  }, []);

  const send = async () => {
    const input = command.trimEnd();
    if (!input) return;
    setCommand("");
    setHistory((prev) => {
      const next = [...prev.filter((x) => x !== input), input].slice(
        -HISTORY_LIMIT,
      );
      saveHistory(props.sessionId, next);
      return next;
    });
    setHistoryAt(0);
    try {
      const response = await fetch(url, {
        method: "POST",
        headers: { "Content-Type": "application/json", ...scope.headers },
        body: JSON.stringify({ input }),
      });
      if (!response.ok) throw new Error(await response.text());
      setSnapshot((await response.json()) as TerminalSnapshot);
      setError("");
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      inputRef.current?.focus();
    }
  };

  const restart = async () => {
    await fetch(url, { method: "DELETE", headers: scope.headers });
    setSnapshot(null);
    setError("");
    await refresh();
    inputRef.current?.focus();
  };

  const toggleMaximized = () => {
    setMaximized((prev) => {
      writeTerminalMaximized(!prev);
      return !prev;
    });
  };

  /** Follows the pointer until it is released, then keeps where it ended. */
  const drag = (
    event: React.PointerEvent,
    step: (dx: number, dy: number, vw: number, vh: number) => TerminalFrame,
  ) => {
    // event.button is 0 for the primary button and absent where the
    // browser has no PointerEvent at all; only a secondary one cancels.
    if (!floating || event.button > 0) return;
    event.preventDefault();
    const startX = event.clientX;
    const startY = event.clientY;
    let last = frame;
    const move = (e: PointerEvent) => {
      const { vw, vh } = viewport();
      last = step(e.clientX - startX, e.clientY - startY, vw, vh);
      setFrame(last);
    };
    const up = () => {
      writeTerminalFrame(last);
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
      window.removeEventListener("pointercancel", up);
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
    window.addEventListener("pointercancel", up);
  };

  const beginMove = (event: React.PointerEvent<HTMLElement>) => {
    // A press on one of the head's buttons is that button's, not the window's.
    if ((event.target as HTMLElement).closest("button")) return;
    const start = frame;
    drag(event, (dx, dy, vw, vh) => moveTerminalFrame(start, dx, dy, vw, vh));
  };

  const beginResize = (edge: TerminalEdge) => (event: React.PointerEvent) => {
    const start = frame;
    drag(event, (dx, dy, vw, vh) =>
      resizeTerminalFrame(start, edge, dx, dy, vw, vh),
    );
  };

  return (
    <section
      className={[
        "terminal-panel",
        stacked ? "terminal-panel--full" : "",
        !stacked && maximized ? "terminal-panel--maximized" : "",
      ]
        .filter(Boolean)
        .join(" ")}
      data-testid="terminal-panel"
      role="dialog"
      aria-label={t("terminal.title")}
      style={
        floating
          ? {
              left: `${frame.x}px`,
              top: `${frame.y}px`,
              width: `${frame.w}px`,
              height: `${frame.h}px`,
            }
          : undefined
      }
    >
      {floating
        ? TERMINAL_EDGES.map((edge) => (
            <div
              key={edge}
              className={`terminal-grip terminal-grip--${edge}`}
              data-testid={`terminal-grip-${edge}`}
              role="separator"
              aria-label={t("terminal.resize")}
              onPointerDown={beginResize(edge)}
            />
          ))
        : null}
      <header
        className="terminal-head"
        data-testid="terminal-head"
        onPointerDown={floating ? beginMove : undefined}
        onDoubleClick={stacked ? undefined : toggleMaximized}
      >
        <div className="terminal-title-row">
          <span
            className="terminal-status"
            data-running={snapshot?.running ?? false}
          />
          <strong>{t("terminal.title")}</strong>
          <span className="terminal-shell" title={snapshot?.shell || ""}>
            {snapshot?.shell?.split(/[\\/]/).pop() || t("terminal.starting")}
          </span>
        </div>
        <div className="terminal-actions">
          <button type="button" onClick={() => void restart()}>
            {t("terminal.restart")}
          </button>
          {stacked ? null : (
            <button
              type="button"
              className="terminal-icon-btn"
              aria-label={
                maximized ? t("terminal.restore") : t("terminal.maximize")
              }
              aria-pressed={maximized}
              onClick={toggleMaximized}
            >
              {maximized ? <RestoreIcon /> : <MaximizeIcon />}
            </button>
          )}
          <button
            type="button"
            className="terminal-icon-btn terminal-close"
            aria-label={t("terminal.close")}
            onClick={props.onClose}
          >
            <CloseIcon />
          </button>
        </div>
      </header>
      <pre ref={outputRef} className="terminal-output" aria-live="polite">
        {snapshot?.output || t("terminal.ready")}
        {snapshot?.exit_error ? `\n${snapshot.exit_error}` : ""}
      </pre>
      {error ? (
        <div className="terminal-error" role="alert">
          {error}
        </div>
      ) : null}
      <form
        className="terminal-input-row"
        onSubmit={(e) => {
          e.preventDefault();
          void send();
        }}
      >
        <span aria-hidden>❯</span>
        <input
          ref={inputRef}
          value={command}
          aria-label={t("terminal.command")}
          placeholder={t("terminal.placeholder")}
          spellCheck={false}
          autoComplete="off"
          onChange={(e) => {
            setCommand(e.target.value);
            setHistoryAt(0);
          }}
          onKeyDown={(e) => {
            if (e.key !== "ArrowUp" && e.key !== "ArrowDown") return;
            e.preventDefault();
            const delta = e.key === "ArrowUp" ? 1 : -1;
            const next = Math.min(
              history.length,
              Math.max(0, historyAt + delta),
            );
            setHistoryAt(next);
            setCommand(next === 0 ? "" : history[history.length - next] || "");
          }}
        />
      </form>
    </section>
  );
}

const iconProps = {
  className: "terminal-icon",
  width: 14,
  height: 14,
  viewBox: "0 0 14 14",
  fill: "none",
  stroke: "currentColor",
  strokeWidth: 1.4,
  strokeLinecap: "round" as const,
  strokeLinejoin: "round" as const,
  "aria-hidden": true,
};

function MaximizeIcon() {
  return (
    <svg {...iconProps}>
      <rect x="2.2" y="2.2" width="9.6" height="9.6" rx="1.4" />
    </svg>
  );
}

function RestoreIcon() {
  return (
    <svg {...iconProps}>
      <rect x="2.2" y="4.6" width="7.2" height="7.2" rx="1.3" />
      <path d="M4.8 4.6V3.5a1.3 1.3 0 0 1 1.3-1.3h4.4a1.3 1.3 0 0 1 1.3 1.3v4.4a1.3 1.3 0 0 1-1.3 1.3H9.4" />
    </svg>
  );
}

function CloseIcon() {
  return (
    <svg {...iconProps}>
      <path d="m3.4 3.4 7.2 7.2M10.6 3.4l-7.2 7.2" />
    </svg>
  );
}
