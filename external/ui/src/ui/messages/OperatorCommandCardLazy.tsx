import { type ComponentProps, useCallback, useEffect, useState } from "react";

import {
  parseOperatorCommandArgs,
  submitOperatorCommandAction,
} from "../chat/operatorCommand";
import { useT } from "../i18n/I18nProvider";
import {
  dropOperatorCommandRecord,
  markOperatorCommandAnswered,
} from "../chat/operatorCommandSessionStore";

type CardModule = typeof import("./OperatorCommandCard");

const defaultImporter = () => import("./OperatorCommandCard");
let importer: () => Promise<CardModule> = defaultImporter;
let loaded: CardModule | null = null;
let loading: Promise<CardModule> | null = null;

/** Replaces the chunk import, for tests; null restores it. */
export function __setOperatorCommandCardImporter(
  fn: (() => Promise<CardModule>) | null,
) {
  importer = fn ?? defaultImporter;
  loaded = null;
  loading = null;
}

/**
 * Loads the operator command card's chunk. The card shows only when the agent
 * asks the operator to run something, so it stays out of app.js; once the
 * chunk is in, every later card renders at once. A load that fails is not
 * remembered, so the next attempt asks for the chunk again.
 */
export function loadOperatorCommandCard(): Promise<CardModule> {
  if (loaded) return Promise.resolve(loaded);
  loading ??= importer().then(
    (m) => {
      loaded = m;
      return m;
    },
    (err: unknown) => {
      loading = null;
      throw err;
    },
  );
  return loading;
}

type Props = ComponentProps<CardModule["OperatorCommandCard"]>;

/** How long the card's chunk may take before the fallback shows. */
const operatorCardSlowMs = 5000;

/**
 * The card, rendered as soon as its chunk is there (no Suspense boundary).
 * While the chunk does not load the request is not lost: the command stays
 * readable, a waiting request can be declined, and Retry asks again.
 */
export function OperatorCommandCardLazy(props: Props) {
  const [mod, setMod] = useState<CardModule | null>(loaded);
  const [failed, setFailed] = useState(false);
  const [attempt, setAttempt] = useState(0);
  // A chunk that neither loads nor fails (a stalled proxy) must not hide the
  // request: after a while the fallback shows, and the import goes on.
  useEffect(() => {
    if (mod || failed) return;
    const timer = setTimeout(() => setFailed(true), operatorCardSlowMs);
    return () => clearTimeout(timer);
  }, [mod, failed, attempt]);
  useEffect(() => {
    if (mod) return;
    let live = true;
    loadOperatorCommandCard().then(
      (m) => {
        if (live) setMod(m);
      },
      () => {
        if (live) setFailed(true);
      },
    );
    return () => {
      live = false;
    };
  }, [mod, attempt]);
  const retry = useCallback(() => {
    setFailed(false);
    setAttempt((n) => n + 1);
  }, []);
  if (mod) return <mod.OperatorCommandCard {...props} />;
  if (!failed) return null;
  return <OperatorCommandFallback {...props} onRetry={retry} />;
}

function OperatorCommandFallback(props: Props & { onRetry: () => void }) {
  const { t } = useT();
  const [declined, setDeclined] = useState(false);
  const [busy, setBusy] = useState(false);
  const [note, setNote] = useState("");
  const request = props.state?.request;
  const command =
    request?.command ?? parseOperatorCommandArgs(props.argsText)?.command ?? "";
  const sessionId = (request?.sessionId || props.sessionId || "").trim();
  const requestId = request?.requestId ?? props.state?.requestId ?? "";
  // The same rule as the card's: a request still waiting for its first
  // answer, on a call that is not over.
  const status = (props.status || "").toLowerCase();
  const waiting =
    !!request &&
    !props.state?.phase &&
    !props.state?.answered &&
    status !== "completed" &&
    status !== "failed" &&
    status !== "cancelled" &&
    !declined;
  // An answer like the card's: kept, so a reload or a later load of the card
  // does not offer it again, and a request the server no longer has goes.
  const decline = useCallback(async () => {
    setBusy(true);
    const res = await submitOperatorCommandAction(
      sessionId,
      requestId,
      "decline",
    );
    setBusy(false);
    if (res.ok) {
      setDeclined(true);
      setNote(t("operatorCommand.declined"));
      markOperatorCommandAnswered(sessionId, requestId, "decline");
    } else if (res.status === 409) {
      setDeclined(true);
      setNote(t("operatorCommand.alreadyAnswered"));
    } else if (res.status === 404) {
      setDeclined(true);
      setNote(t("operatorCommand.ended"));
      dropOperatorCommandRecord(sessionId, props.toolCallId);
    } else {
      setNote(t("operatorCommand.sendFailed"));
    }
  }, [props.toolCallId, requestId, sessionId, t]);
  return (
    <div
      className="permission-prompt-card operator-command-card"
      data-testid="operator-command-fallback"
    >
      <div className="permission-prompt-head operator-command-head">
        <span className="permission-prompt-title">
          {t("operatorCommand.title")}
        </span>
      </div>
      <pre className="tool-result-pre">{command}</pre>
      <p className="operator-command-note">{t("operatorCommand.loadFailed")}</p>
      {note ? (
        <p className="operator-command-note" role="alert">
          {note}
        </p>
      ) : null}
      <div className="permission-prompt-actions">
        <button
          type="button"
          className="btn"
          data-testid="operator-command-retry"
          onClick={props.onRetry}
        >
          {t("operatorCommand.retry")}
        </button>
        {waiting && sessionId && requestId ? (
          <button
            type="button"
            className="btn"
            data-testid="operator-command-decline"
            disabled={busy}
            onClick={() => void decline()}
          >
            {t("operatorCommand.decline")}
          </button>
        ) : null}
      </div>
    </div>
  );
}
