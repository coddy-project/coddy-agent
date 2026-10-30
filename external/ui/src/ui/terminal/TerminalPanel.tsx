import { useCallback, useEffect, useRef, useState } from "react";
import { useT } from "../i18n/I18nProvider";
import { workspaceScope } from "../chat/workspaceScope";

type TerminalSnapshot = {
  shell: string;
  output: string;
  running: boolean;
  exit_error?: string;
};

const HEIGHT_KEY = "coddy.terminal.height";
const HISTORY_KEY = "coddy.terminal.history.";
const HISTORY_LIMIT = 100;

function savedHeight(): number {
  const n = Number(localStorage.getItem(HEIGHT_KEY));
  return Number.isFinite(n) ? Math.min(560, Math.max(180, n)) : 280;
}

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

/** A chat-scoped shell under the conversation. The server owns the process, so
 * closing and reopening the panel keeps its cwd, exports and output. */
export function TerminalPanel(props: {
  sessionId: string;
  workspacePath: string;
  onClose: () => void;
}) {
  const { t } = useT();
  const [snapshot, setSnapshot] = useState<TerminalSnapshot | null>(null);
  const [command, setCommand] = useState("");
  const [history, setHistory] = useState<string[]>(() =>
    savedHistory(props.sessionId),
  );
  const [historyAt, setHistoryAt] = useState(0);
  const [error, setError] = useState("");
  const [height, setHeight] = useState(savedHeight);
  const outputRef = useRef<HTMLPreElement | null>(null);
  const inputRef = useRef<HTMLInputElement | null>(null);
  const scope = workspaceScope(props.sessionId, props.workspacePath);
  const url = `/coddy/sessions/${encodeURIComponent(props.sessionId)}/terminal`;

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

  const beginResize = (event: React.PointerEvent<HTMLDivElement>) => {
    event.preventDefault();
    const startY = event.clientY;
    const startHeight = height;
    const move = (e: PointerEvent) =>
      setHeight(Math.min(560, Math.max(180, startHeight + startY - e.clientY)));
    const up = (e: PointerEvent) => {
      const next = Math.min(
        560,
        Math.max(180, startHeight + startY - e.clientY),
      );
      setHeight(next);
      localStorage.setItem(HEIGHT_KEY, String(next));
      window.removeEventListener("pointermove", move);
      window.removeEventListener("pointerup", up);
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
  };

  return (
    <section
      className="terminal-panel"
      data-testid="terminal-panel"
      aria-label={t("terminal.title")}
      style={{ height }}
    >
      <div
        className="terminal-resize"
        role="separator"
        aria-orientation="horizontal"
        aria-label={t("terminal.resize")}
        onPointerDown={beginResize}
      />
      <header className="terminal-head">
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
          <button
            type="button"
            className="terminal-close"
            aria-label={t("terminal.close")}
            onClick={props.onClose}
          >
            ×
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
          autoFocus
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
