import { memo, useState } from "react";
import { useT } from "../i18n/I18nProvider";
import { useEscapeCloses } from "../components/useEscapeCloses";
import { HttpError } from "../files/api";
import { trustBoard, type CanvasFrame as Frame, type CanvasTrust } from "./api";

/**
 * A page of a board that came with the workspace and waits for the person: a
 * placeholder where its frame stands, naming the page and what it would read,
 * with the button that asks to run the board's pages. Nothing is loaded.
 */
export const CanvasHeldFrame = memo(function CanvasHeldFrame(props: {
  frame: Frame;
  dx: number;
  dy: number;
  policy: CanvasTrust["policy"] | undefined;
  onAsk: () => void;
}) {
  const { t } = useT();
  const { frame } = props;
  return (
    <div
      className="canvas-frame canvas-frame--held"
      style={{
        left: frame.x + props.dx,
        top: frame.y + props.dy,
        width: frame.width,
        height: frame.height,
      }}
      data-testid="canvas-frame-held"
      data-frame-id={frame.id}
    >
      <div className="canvas-held" role="group" aria-label={frame.path}>
        <p className="canvas-held-title">{t("canvas.held.title")}</p>
        <p className="canvas-held-page">
          <code>{frame.path}</code>
        </p>
        <p className="canvas-held-hint">
          {frame.root
            ? t("canvas.held.hint", { root: `${frame.root}/` })
            : t("canvas.held.hintAll")}
        </p>
        {props.policy === "deny" ? (
          <p className="canvas-held-hint" data-testid="canvas-held-deny">
            {t("canvas.held.deny")}
          </p>
        ) : (
          <button
            type="button"
            className="canvas-frame-back"
            data-testid="canvas-held-run"
            onPointerDown={(e) => e.stopPropagation()}
            onClick={props.onAsk}
          >
            {t("canvas.held.run")}
          </button>
        )}
      </div>
    </div>
  );
});

/**
 * The question before the pages of a board run: every page that waits, with
 * the folder it would read. The approval names exactly that list (the trust
 * digest), so a board whose pages changed meanwhile approves nothing and the
 * dialog says so.
 */
export function CanvasTrustDialog(props: {
  sessionId: string;
  file: string;
  trust: CanvasTrust;
  held: Frame[];
  onClose: () => void;
  /** The pages run: the board is read again and the dialog goes. */
  onDone: () => void;
  /** The list on screen is stale: the board is read again, the dialog stays
   *  with the new list. */
  onStale: () => void;
}) {
  const { t } = useT();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  useEscapeCloses(true, props.onClose);
  const pages = new Map<string, Frame>();
  for (const f of props.held) if (!pages.has(f.path)) pages.set(f.path, f);

  const run = () => {
    setBusy(true);
    setError("");
    trustBoard(props.sessionId, props.file, props.trust.digest).then(
      () => {
        setBusy(false);
        props.onDone();
      },
      (err: unknown) => {
        setBusy(false);
        if (err instanceof HttpError && err.status === 409) {
          setError(t("canvas.trust.moved"));
          props.onStale();
          return;
        }
        setError(
          t("canvas.trust.failed", {
            error: err instanceof Error ? err.message : String(err),
          }),
        );
      },
    );
  };

  return (
    <div
      className="canvas-trust"
      role="dialog"
      aria-modal="true"
      aria-labelledby="canvas-trust-title"
      data-testid="canvas-trust-dialog"
      onPointerDown={(e) => e.stopPropagation()}
    >
      <h2 id="canvas-trust-title" className="canvas-trust-title">
        {t("canvas.trust.title")}
      </h2>
      <p className="canvas-trust-intro">
        {t("canvas.trust.intro", { file: props.file })}
      </p>
      <ul className="canvas-trust-pages" data-testid="canvas-trust-pages">
        {[...pages.values()].map((f) => (
          <li key={f.path}>
            <code>{f.path}</code>
            <span className="canvas-trust-root">
              {f.root
                ? t("canvas.trust.reads", { root: `${f.root}/` })
                : t("canvas.trust.readsAll")}
            </span>
          </li>
        ))}
      </ul>
      {error ? (
        <p
          className="canvas-trust-error"
          role="alert"
          data-testid="canvas-trust-error"
        >
          {error}
        </p>
      ) : null}
      <div className="canvas-popover-actions">
        <button
          type="button"
          className="canvas-popover-cancel"
          onClick={props.onClose}
        >
          {t("canvas.cancel")}
        </button>
        <button
          type="button"
          className="canvas-popover-add"
          data-testid="canvas-trust-confirm"
          disabled={busy}
          onClick={run}
        >
          {t("canvas.trust.confirm")}
        </button>
      </div>
    </div>
  );
}
