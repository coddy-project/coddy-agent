import { useEffect, useRef, useState } from "react";
import { useT } from "../i18n/I18nProvider";
import { useEscapeCloses } from "../components/useEscapeCloses";
import { searchFiles } from "../files/api";
import { openWorkspaceFile } from "../files/fileBus";
import {
  exportCanvas,
  isCanvasFile,
  linkCanvasFile,
  unlinkCanvas,
  type CanvasLink,
} from "./api";

type Form = "save" | "open";

function IconCanvasFile() {
  return (
    <svg
      width="18"
      height="18"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.8"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <rect x="3" y="4" width="7" height="6" rx="1.5" />
      <rect x="14" y="14" width="7" height="6" rx="1.5" />
      <path d="M10 7h3a2 2 0 0 1 2 2v5" />
    </svg>
  );
}

/**
 * The canvas's file menu in the window's head: save the chat's own board as
 * a .canvas file of the workspace (JSON Canvas, which Obsidian opens as it
 * is), open such a file as the board, and give the chat its own board back.
 */
export function CanvasFileMenu(props: {
  sessionId: string;
  link: CanvasLink | undefined;
  hasFrames: boolean;
  onChanged: () => void;
}) {
  const { t } = useT();
  const [menuOpen, setMenuOpen] = useState(false);
  const [form, setForm] = useState<Form | null>(null);
  const [path, setPath] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [found, setFound] = useState<string[]>([]);
  const inputRef = useRef<HTMLInputElement | null>(null);

  const closeAll = () => {
    setMenuOpen(false);
    setForm(null);
    setError("");
  };
  useEscapeCloses(menuOpen || form !== null, closeAll);

  // The .canvas files of the workspace, offered when a file is to be opened.
  useEffect(() => {
    if (form !== "open") return undefined;
    const ctl = new AbortController();
    searchFiles(props.sessionId, "", ".canvas", ctl.signal).then(
      (hits) =>
        setFound(
          hits
            .filter((h) => h.kind === "file" && isCanvasFile(h.path))
            .map((h) => h.path)
            .slice(0, 8),
        ),
      () => setFound([]),
    );
    return () => ctl.abort();
  }, [form, props.sessionId]);

  useEffect(() => {
    if (form) inputRef.current?.focus({ preventScroll: true });
  }, [form]);

  const startForm = (kind: Form) => {
    setMenuOpen(false);
    setError("");
    setPath(kind === "save" ? "canvas.canvas" : "");
    setForm(kind);
  };

  const submit = (target = path) => {
    const p = target.trim();
    if (!p || busy || !form) return;
    if (!isCanvasFile(p)) {
      setError(t("canvas.file.needsExtension"));
      return;
    }
    setBusy(true);
    setError("");
    const call =
      form === "save"
        ? exportCanvas(props.sessionId, p)
        : linkCanvasFile(props.sessionId, p);
    call.then(
      () => {
        setBusy(false);
        setForm(null);
        props.onChanged();
      },
      (err: unknown) => {
        setBusy(false);
        setError(err instanceof Error ? err.message : String(err));
      },
    );
  };

  const unlink = () => {
    setMenuOpen(false);
    void unlinkCanvas(props.sessionId).finally(props.onChanged);
  };

  return (
    <span className="canvas-file-host">
      <button
        type="button"
        className={"files-icon-btn" + (menuOpen || form ? " is-active" : "")}
        data-testid="canvas-file-menu"
        aria-label={t("canvas.file.menu")}
        title={t("canvas.file.menu")}
        aria-haspopup="menu"
        aria-expanded={menuOpen}
        onClick={() => {
          setForm(null);
          setMenuOpen((v) => !v);
        }}
      >
        <IconCanvasFile />
      </button>
      {menuOpen ? (
        <>
          <button
            type="button"
            className="mode-menu-backdrop"
            aria-hidden="true"
            tabIndex={-1}
            onMouseDown={(e) => {
              e.preventDefault();
              setMenuOpen(false);
            }}
          />
          <div className="mode-menu canvas-file-menu" role="menu">
            {props.link ? (
              <>
                <p className="canvas-file-menu-note">
                  {t("canvas.file.linkedTo", { file: props.link.path })}
                </p>
                <button
                  type="button"
                  role="menuitem"
                  className="mode-item"
                  data-testid="canvas-file-show"
                  onClick={() => {
                    setMenuOpen(false);
                    openWorkspaceFile(props.link!.path);
                  }}
                >
                  {t("canvas.file.showInFiles")}
                </button>
                <button
                  type="button"
                  role="menuitem"
                  className="mode-item"
                  data-testid="canvas-file-unlink"
                  onClick={unlink}
                >
                  {t("canvas.file.unlink")}
                </button>
              </>
            ) : (
              <>
                <button
                  type="button"
                  role="menuitem"
                  className="mode-item"
                  data-testid="canvas-file-save"
                  disabled={!props.hasFrames}
                  onClick={() => startForm("save")}
                >
                  {t("canvas.file.save")}
                </button>
                <button
                  type="button"
                  role="menuitem"
                  className="mode-item"
                  data-testid="canvas-file-open"
                  onClick={() => startForm("open")}
                >
                  {t("canvas.file.open")}
                </button>
              </>
            )}
          </div>
        </>
      ) : null}
      {form ? (
        <form
          className="canvas-file-form"
          data-testid="canvas-file-form"
          role="dialog"
          aria-label={t(
            form === "save" ? "canvas.file.save" : "canvas.file.open",
          )}
          onSubmit={(e) => {
            e.preventDefault();
            submit();
          }}
        >
          <label className="canvas-file-label" htmlFor="canvas-file-path">
            {t(
              form === "save"
                ? "canvas.file.saveLabel"
                : "canvas.file.openLabel",
            )}
          </label>
          <input
            ref={inputRef}
            id="canvas-file-path"
            className="canvas-file-input"
            data-testid="canvas-file-path"
            value={path}
            placeholder="design/board.canvas"
            autoComplete="off"
            spellCheck={false}
            onChange={(e) => setPath(e.target.value)}
          />
          {form === "open" && found.length > 0 ? (
            <ul className="canvas-file-found">
              {found.map((p) => (
                <li key={p}>
                  <button
                    type="button"
                    className="canvas-file-found-item"
                    onClick={() => {
                      setPath(p);
                      submit(p);
                    }}
                  >
                    {p}
                  </button>
                </li>
              ))}
            </ul>
          ) : null}
          <p className="canvas-file-hint">{t("canvas.file.hint")}</p>
          {error ? (
            <p
              className="canvas-file-error"
              role="alert"
              data-testid="canvas-file-error"
            >
              {error}
            </p>
          ) : null}
          <div className="canvas-popover-actions">
            <button
              type="button"
              className="canvas-popover-cancel"
              onClick={() => setForm(null)}
            >
              {t("canvas.cancel")}
            </button>
            <button
              type="submit"
              className="canvas-popover-add"
              data-testid="canvas-file-submit"
              disabled={busy || !path.trim()}
            >
              {t(
                form === "save"
                  ? "canvas.file.saveSubmit"
                  : "canvas.file.openSubmit",
              )}
            </button>
          </div>
        </form>
      ) : null}
    </span>
  );
}
