import { useEffect, useMemo, useState } from "react";
import { useT } from "../i18n/I18nProvider";
import { PermissionToolPreview } from "../chat/PermissionPromptPreview";
import { diffPreviewFromPatch } from "../chat/permissionToolPreview";
import { fetchSessionChangeFile, fetchSessionChanges } from "./api";
import { baseName, dirName, statusKey } from "./sessionChangesText";
import { EMPTY_SESSION_CHANGES, type SessionChanges } from "./types";

/**
 * The session's edits in the dock beside the chat: the changed files on top,
 * the unified diff of the selected one under them. The chat header's views
 * menu opens it, and so does a file row of the card under the transcript.
 *
 * Desktop and browser have no native diff viewer to hand the review to, so this
 * is where they read it. Editor panels normally never open this — the card
 * routes them to the plugin instead — but it stays reachable as the fallback
 * when no plugin answers.
 */
export function SessionChangesPanel(props: {
  open: boolean;
  sessionId: string;
  /** Preselects one file, e.g. when the user clicked its row on the card. */
  initialPath?: string | undefined;
  onClose: () => void;
}) {
  const { t, tp } = useT();
  const [changes, setChanges] = useState<SessionChanges>(EMPTY_SESSION_CHANGES);
  const [selected, setSelected] = useState<string>(props.initialPath || "");
  const [patch, setPatch] = useState<string>("");
  const [detailError, setDetailError] = useState<string>("");
  const [truncated, setTruncated] = useState(false);
  const [loading, setLoading] = useState(false);

  const { open, sessionId, initialPath } = props;

  useEffect(() => {
    if (!open || !sessionId.trim()) {
      return;
    }
    let cancelled = false;
    setLoading(true);
    void (async () => {
      const res = await fetchSessionChanges(sessionId);
      if (cancelled) {
        return;
      }
      setLoading(false);
      if (!res.ok) {
        return;
      }
      setChanges(res.data);
      // Fall back to the first file so the viewer never opens blank.
      const wanted = initialPath || "";
      const exists = res.data.files.some((f) => f.path === wanted);
      const first = res.data.files.length > 0 ? res.data.files[0]!.path : "";
      setSelected(exists ? wanted : first);
    })();
    return () => {
      cancelled = true;
    };
  }, [open, sessionId, initialPath]);

  useEffect(() => {
    setPatch("");
    setTruncated(false);
    setDetailError("");
    if (!open || !sessionId.trim() || !selected) {
      return;
    }
    let cancelled = false;
    setDetailError("");
    void (async () => {
      const res = await fetchSessionChangeFile(sessionId, selected);
      if (cancelled) {
        return;
      }
      if (res.ok) {
        setPatch(res.data.patch || "");
        setTruncated(res.data.truncated);
      } else {
        setPatch("");
        setDetailError(res.message);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [open, sessionId, selected]);

  const current = changes.files.find((f) => f.path === selected) || null;
  const preview = useMemo(
    () => (patch ? diffPreviewFromPatch(patch, selected) : null),
    [patch, selected],
  );

  if (!open) {
    return null;
  }

  return (
    <aside
      className="changes-panel"
      aria-label={t("changes.panelTitle")}
      data-testid="changes-panel"
    >
      <div className="sessions-head changes-panel-head">
        <span>{t("changes.panelTitle")}</span>
        <button
          type="button"
          className="sessions-close"
          aria-label={t("changes.closePanel")}
          data-testid="changes-panel-close"
          onClick={props.onClose}
        >
          ×
        </button>
      </div>

      <div className="changes-panel-summary">
        <span>{tp("changes.card.files", changes.totals.files)}</span>
        <span className="changes-card-stat">
          <span className="changes-add">{"+" + changes.totals.additions}</span>
          <span className="changes-del">{"−" + changes.totals.deletions}</span>
        </span>
      </div>

      <div className="changes-panel-body">
        <div className="changes-panel-list">
          {changes.files.map((file) => (
            <button
              key={file.path}
              type="button"
              className={[
                "changes-row",
                "changes-row--" + file.status,
                file.path === selected ? "is-active" : "",
              ]
                .filter(Boolean)
                .join(" ")}
              title={file.path}
              data-testid={`changes-panel-row-${file.path}`}
              onClick={() => setSelected(file.path)}
            >
              <span className="changes-row-path">
                <span className="changes-row-name">{baseName(file.path)}</span>
                {dirName(file.path) ? (
                  <span className="changes-row-dir">{dirName(file.path)}</span>
                ) : null}
              </span>
              {file.binary ? (
                <span className="changes-row-binary">
                  {t("changes.binary")}
                </span>
              ) : (
                <span className="changes-row-stat">
                  <span className="changes-add">{"+" + file.additions}</span>
                  <span className="changes-del">{"−" + file.deletions}</span>
                </span>
              )}
            </button>
          ))}
          {!loading && changes.files.length === 0 ? (
            <div className="sessions-empty" data-testid="changes-panel-empty">
              {t("changes.empty")}
            </div>
          ) : null}
        </div>

        <div className="changes-panel-diff">
          {/* The path is not repeated here: the preview bar below already carries
              it next to the line counts. */}
          {current ? (
            <div className="changes-diff-head">
              <span
                className={"changes-badge changes-badge--" + current.status}
              >
                {t(statusKey(current.status))}
              </span>
              {truncated || current.truncated ? (
                <span className="changes-badge changes-badge--truncated">
                  {t("changes.truncated")}
                </span>
              ) : null}
            </div>
          ) : null}
          {current && current.binary ? (
            <div className="sessions-empty">{t("changes.binaryBody")}</div>
          ) : detailError ? (
            <div className="sessions-empty" data-testid="changes-detail-error">
              {detailError}
            </div>
          ) : preview ? (
            <PermissionToolPreview preview={preview} interactive={false} />
          ) : null}
        </div>
      </div>
    </aside>
  );
}
