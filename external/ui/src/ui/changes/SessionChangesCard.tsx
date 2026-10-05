import { useEffect, useState, useSyncExternalStore } from "react";
import { useT } from "../i18n/I18nProvider";
import {
  DEFAULT_SESSION_CHANGES,
  getSessionChangesEnabled,
  onSessionChangesChange,
} from "../chat/sessionChangesConfig";
import { revertSessionChanges } from "./api";
import { baseName, dirName } from "./sessionChangesText";
import { useSessionChanges } from "./useSessionChanges";
import { requestChangesToggle } from "./sessionChangesBus";
import type { ChangedFile } from "./types";

/** How many rows the card lists before the rest are only reachable in the viewer. */
const ROW_CAP = 8;

/**
 * Ctrl+S / Cmd+S. Matched on the physical key, because on the Russian layout
 * the same key reports "ы"; Shift and Alt variants stay the browser's.
 */
export function isChangesHotkey(e: KeyboardEvent): boolean {
  return (
    (e.ctrlKey || e.metaKey) && !e.altKey && !e.shiftKey && e.code === "KeyS"
  );
}

/**
 * Takes Ctrl+S / Cmd+S for the changed-files card while a chat is open. The
 * listener sits on the capture phase so the composer and the browser never see
 * the key: the page has nothing to save, and a "Save page" dialog would only be
 * in the way. A held key repeats; only the first press counts.
 */
function useChangesHotkey(): void {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (!isChangesHotkey(e)) {
        return;
      }
      e.preventDefault();
      if (!e.repeat) {
        requestChangesToggle();
      }
    };
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  }, []);
}

function ChangedRow(props: {
  file: ChangedFile;
  onOpen: (path: string) => void;
}) {
  const { t } = useT();
  const file = props.file;
  const dir = dirName(file.path);
  return (
    <button
      type="button"
      className={"changes-row changes-row--" + file.status}
      title={file.path}
      data-testid={`changes-row-${file.path}`}
      onClick={() => props.onOpen(file.path)}
    >
      <span className="changes-row-path">
        <span className="changes-row-name">{baseName(file.path)}</span>
        {dir ? <span className="changes-row-dir">{dir}</span> : null}
      </span>
      {file.binary ? (
        <span className="changes-row-binary">{t("changes.binary")}</span>
      ) : (
        <span className="changes-row-stat">
          <span className="changes-add">{"+" + file.additions}</span>
          <span className="changes-del">{"−" + file.deletions}</span>
        </span>
      )}
    </button>
  );
}

/**
 * The changed-files card, sitting under the last message the way the background
 * tasks chip does.
 *
 * It summarises what the whole session did to the workspace, which is why it
 * belongs at the bottom of the transcript rather than beside any one turn: the
 * numbers describe every turn above it together. Clicking a row opens the diff
 * viewer — or, inside an editor panel, hands the review to the plugin so the
 * diffs open in the IDE's own viewer.
 */
export function SessionChangesCard(props: {
  sessionId: string;
  generating: boolean;
  /** Finished tool calls in the transcript; open mid-turn, the card re-reads after each. */
  toolActivity?: number;
  /** Opens one file in the side drawer; also the fallback for a row click. */
  onOpenReview: (path?: string) => void;
  /** Opens the full review window, which shows every file at once. */
  onOpenViewer: () => void;
}) {
  const { t, tp } = useT();
  const enabled = useSyncExternalStore(
    onSessionChangesChange,
    getSessionChangesEnabled,
    () => DEFAULT_SESSION_CHANGES,
  );
  const { changes, reload, shown, manual } = useSessionChanges({
    sessionId: props.sessionId,
    enabled,
    generating: props.generating,
    toolActivity: props.toolActivity ?? 0,
  });
  useChangesHotkey();
  const [confirming, setConfirming] = useState(false);
  const [busy, setBusy] = useState(false);
  const [revertError, setRevertError] = useState("");

  if (!enabled || !shown) {
    return null;
  }
  if (changes.totals.files === 0) {
    // Nothing changed in this chat, so there is nothing to review - unless the
    // user asked for the card, and then the key has to visibly answer.
    return manual ? (
      <section
        className="changes-card changes-card--empty"
        data-testid="changes-card-empty"
      >
        <p className="changes-card-empty">{t("changes.empty")}</p>
      </section>
    ) : null;
  }

  // A row asks about one file, which the side drawer opens on.
  const openRow = (path: string) => {
    props.onOpenReview(path);
  };

  // The summary and Review ask about the change set, and the review window is
  // the only surface that shows one: the question they asked was "what
  // changed", and the answer to that is a list, not a file.
  const openAll = () => {
    props.onOpenViewer();
  };

  const revert = () => {
    setBusy(true);
    setRevertError("");
    void (async () => {
      const result = await revertSessionChanges(props.sessionId);
      setBusy(false);
      if (!result.ok) {
        setRevertError(result.message);
        return;
      }
      setConfirming(false);
      reload();
    })();
  };

  const rows = changes.files.slice(0, ROW_CAP);
  const hidden = changes.files.length - rows.length;
  const countLabel = tp("changes.card.files", changes.totals.files);

  return (
    <section className="changes-card" data-testid="session-changes-card">
      <div className="changes-card-head">
        <button
          type="button"
          className="changes-card-summary"
          data-testid="changes-card-open"
          onClick={openAll}
        >
          <span className="changes-card-title">{countLabel}</span>
          <span className="changes-card-stat">
            <span className="changes-add">
              {"+" + changes.totals.additions}
            </span>
            <span className="changes-del">
              {"−" + changes.totals.deletions}
            </span>
          </span>
        </button>
        <div className="changes-card-actions">
          {confirming ? (
            <>
              <span className="changes-confirm-text">
                {t("changes.revertConfirm")}
              </span>
              <button
                type="button"
                className="changes-action changes-action--danger"
                disabled={busy}
                data-testid="changes-revert-confirm"
                onClick={revert}
              >
                {busy ? t("changes.reverting") : t("changes.revertYes")}
              </button>
              <button
                type="button"
                className="changes-action"
                disabled={busy}
                data-testid="changes-revert-cancel"
                onClick={() => {
                  setConfirming(false);
                  setRevertError("");
                }}
              >
                {t("changes.revertNo")}
              </button>
            </>
          ) : (
            <>
              <button
                type="button"
                className="changes-action"
                title={t("changes.revertTitle")}
                data-testid="changes-revert"
                onClick={() => setConfirming(true)}
              >
                {t("changes.revert")}
              </button>
              <button
                type="button"
                className="changes-action changes-action--primary"
                data-testid="changes-review"
                onClick={openAll}
              >
                {t("changes.review")}
              </button>
            </>
          )}
        </div>
      </div>
      {revertError ? (
        <p role="alert" className="dv-note dv-note--error">
          {revertError}
        </p>
      ) : null}
      <div className="changes-card-rows">
        {rows.map((file) => (
          <ChangedRow key={file.path} file={file} onOpen={openRow} />
        ))}
        {hidden > 0 ? (
          <button
            type="button"
            className="changes-row changes-row--more"
            data-testid="changes-row-more"
            onClick={openAll}
          >
            {t("changes.moreFiles", { count: hidden })}
          </button>
        ) : null}
      </div>
    </section>
  );
}
