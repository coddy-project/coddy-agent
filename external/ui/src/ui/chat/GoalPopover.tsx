import {
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type CSSProperties,
  type RefObject,
} from "react";
import { createPortal } from "react-dom";

import { Chevron } from "../components/Chevron";
import { TargetIcon } from "../components/TargetIcon";
import { useConfirm } from "../components/useConfirm";
import { useEscapeCloses } from "../components/useEscapeCloses";
import { useT } from "../i18n/I18nProvider";
import { composerAutoFocusAllowed } from "./composerFocus";
import {
  GOAL_OBJECTIVE_MAX,
  GOAL_RESUME_PROMPT,
  goalCanPause,
  goalCanResume,
  goalSetPrompt,
  goalStatusKey,
  goalTone,
  goalVerdictKey,
  type GoalChecklistItem,
  type SessionGoal,
} from "./goal";
import { formatElapsedSeconds } from "./liveStatus";
import { formatTurnTokens } from "./turnProgress";

/** What the goal popover can do; the shell (App.tsx) owns the requests. */
export type GoalActions = {
  /** `PATCH /coddy/sessions/{id}/goal {"status":"paused"}`; false when it failed. */
  pause: () => Promise<boolean>;
  /** `DELETE /coddy/sessions/{id}/goal`; false when it failed. */
  clear: () => Promise<boolean>;
  /**
   * Sends a `/goal` prompt the way the composer sends one: `/goal resume`, or
   * `/goal <objective>`, which sets (or replaces) the goal and starts working
   * on it at once.
   */
  sendPrompt: (text: string) => void;
};

type FloatRect = {
  left: number;
  width: number;
  top?: number;
  bottom?: number;
  maxHeight: number;
};

/** Objective length as the server counts it: runes, not UTF-16 units. */
function objectiveLength(text: string): number {
  return Array.from(text.trim()).length;
}

function ChecklistIcon(props: { status: string }) {
  return (
    <svg
      className="goal-check-icon"
      viewBox="0 0 16 16"
      width="14"
      height="14"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
      focusable="false"
    >
      {props.status === "met" ? (
        <>
          <circle cx="8" cy="8" r="6.25" />
          <path d="M5.2 8.2l1.9 1.9 3.7-4" />
        </>
      ) : props.status === "not_met" ? (
        <circle cx="8" cy="8" r="6.25" />
      ) : (
        <circle cx="8" cy="8" r="6.25" strokeDasharray="2.4 2.2" />
      )}
    </svg>
  );
}

/**
 * The level the check runs at: the one the goal chose, else the model's
 * default; "default" when the model configures none, nothing for a model
 * without levels. An older server sends only the goal's own choice.
 */
function checkerLevel(
  goal: SessionGoal,
  t: (key: string, params?: Record<string, string | number>) => string,
): string {
  const level = goal.checkReasoning || goal.reasoning;
  return level === "default" ? t("goal.reasoningDefault") : level;
}

function checklistStatusKey(status: string): string {
  if (status === "met") return "goal.item.met";
  if (status === "not_met") return "goal.item.notMet";
  return "goal.item.unverified";
}

function ChecklistRow(props: { item: GoalChecklistItem }) {
  const { t } = useT();
  const { item } = props;
  const cls = `goal-check-item goal-check-item--${item.status === "met" || item.status === "not_met" ? item.status : "unverified"}`;
  const label = (
    <>
      <ChecklistIcon status={item.status} />
      <span className="sr-only">{t(checklistStatusKey(item.status))}: </span>
      <span className="goal-check-text">{item.text}</span>
    </>
  );
  if (!item.evidence) {
    return (
      <li className={cls}>
        <span className="goal-check-line">{label}</span>
      </li>
    );
  }
  // Evidence folds out on a tap as well as a click: nothing here waits for a
  // hover a phone does not have.
  return (
    <li className={cls}>
      <details className="goal-check-details">
        <summary
          className="goal-check-line"
          title={t("goal.item.evidenceHint")}
        >
          {label}
          <Chevron className="goal-check-chevron" />
        </summary>
        <p className="goal-evidence">{item.evidence}</p>
      </details>
    </li>
  );
}

/**
 * GoalPopover shows the session goal and what can be done with it: the
 * objective, where the supervisor stands and why, its last check, the
 * checklist, the numbers, and Pause, Resume, Edit and Clear. Without a goal it
 * offers to set one. Anchored over the chip (or the composer) on a desktop, a
 * bottom sheet over a scrim on the stacked shell (DESIGN.md, Session goal).
 */
export function GoalPopover(props: {
  open: boolean;
  onClose: () => void;
  goal: SessionGoal | null;
  /** Stacked shell: a bottom sheet instead of an anchored panel. */
  useSheet: boolean;
  /** What the panel is placed from on a desktop: the chip, else the composer. */
  anchorRef: RefObject<HTMLElement | null>;
  /** The control that toggles the popover: a press on it is not outside. */
  toggleRef?: RefObject<HTMLElement | null>;
  /** A turn is running: the prompts (Resume, Save, Set) wait until it ends. */
  generating: boolean;
  actions: GoalActions;
}) {
  const { t, tp } = useT();
  const confirm = useConfirm();
  const goal = props.goal;
  const panelRef = useRef<HTMLDivElement | null>(null);
  const textareaRef = useRef<HTMLTextAreaElement | null>(null);
  const fieldId = useId();
  const [floatRect, setFloatRect] = useState<FloatRect | null>(null);
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState("");
  const [busy, setBusy] = useState(false);
  const [confirming, setConfirming] = useState(false);
  const [error, setError] = useState("");
  const showForm = !goal || editing;

  // A goal cleared elsewhere while the form was open for it leaves the set
  // form; a goal that arrives meanwhile ends the edit of an empty one.
  useEffect(() => {
    if (!goal && editing) setEditing(false);
  }, [goal, editing]);

  const measure = () => {
    if (props.useSheet || !props.open) {
      setFloatRect(null);
      return;
    }
    const el = props.anchorRef.current;
    if (!el) {
      setFloatRect(null);
      return;
    }
    const r = el.getBoundingClientRect();
    const vw = window.innerWidth;
    const vh = window.innerHeight;
    const width = Math.min(400, Math.max(260, vw - 24));
    const left = Math.max(12, Math.min(r.left, vw - width - 12));
    const above = r.top - 20;
    const below = vh - r.bottom - 20;
    if (above >= 280 || above >= below) {
      setFloatRect({
        left,
        width,
        bottom: vh - r.top + 8,
        maxHeight: Math.max(160, above),
      });
    } else {
      setFloatRect({
        left,
        width,
        top: r.bottom + 8,
        maxHeight: Math.max(160, below),
      });
    }
  };

  useLayoutEffect(() => {
    if (!props.open) {
      setFloatRect(null);
      return;
    }
    measure();
    if (props.useSheet) return;
    window.addEventListener("resize", measure);
    window.addEventListener("scroll", measure, { passive: true });
    return () => {
      window.removeEventListener("resize", measure);
      window.removeEventListener("scroll", measure);
    };
    // measure reads the props it needs at call time.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [props.open, props.useSheet, props.anchorRef]);

  // Opening puts the focus in the popover: the field of the set form where a
  // keyboard is at hand (a touch screen would open its keyboard over half the
  // page for it), the panel itself otherwise, so Tab starts from there.
  useEffect(() => {
    if (!props.open) return;
    const id = window.setTimeout(() => {
      if (showForm && composerAutoFocusAllowed() && textareaRef.current) {
        textareaRef.current.focus();
        return;
      }
      panelRef.current?.focus();
    }, 0);
    return () => window.clearTimeout(id);
    // Only on opening and on entering the form.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [props.open, editing]);

  // Closing hands the focus back to the chip when it was inside the popover.
  useEffect(() => {
    if (!props.open) return;
    const panel = panelRef;
    const toggle = props.toggleRef;
    return () => {
      const active = document.activeElement;
      if (
        toggle?.current &&
        (!active || active === document.body || panel.current?.contains(active))
      ) {
        toggle.current.focus();
      }
    };
  }, [props.open, props.toggleRef]);

  // Escape leaves the edit first, then the popover. While the confirmation
  // dialog asks, the key is the dialog's.
  useEscapeCloses(props.open && !confirming, () => {
    if (editing && goal) {
      setEditing(false);
      setError("");
      return;
    }
    props.onClose();
  });

  useEffect(() => {
    if (!props.open || props.useSheet) return;
    const onPointer = (ev: MouseEvent) => {
      if (confirming) return;
      const target = ev.target as Node | null;
      if (!target) return;
      if (panelRef.current?.contains(target)) return;
      if (props.toggleRef?.current?.contains(target)) return;
      props.onClose();
    };
    window.addEventListener("mousedown", onPointer);
    return () => window.removeEventListener("mousedown", onPointer);
  }, [props.open, props.useSheet, props.onClose, props.toggleRef, confirming]);

  if (!props.open) {
    return null;
  }

  const startEdit = () => {
    setDraft(goal?.objective ?? "");
    setError("");
    setEditing(true);
  };

  const run = async (
    action: () => Promise<boolean>,
    failKey: string,
    closeOnSuccess: boolean,
  ) => {
    setBusy(true);
    setError("");
    let ok = false;
    try {
      ok = await action();
    } catch {
      ok = false;
    }
    setBusy(false);
    if (!ok) {
      setError(t(failKey));
      return;
    }
    if (closeOnSuccess) props.onClose();
  };

  const onPause = () =>
    void run(props.actions.pause, "goal.error.pause", false);

  const onClear = async () => {
    setConfirming(true);
    let ok = false;
    try {
      ok = await confirm({
        title: t("goal.clearConfirm.title"),
        message: t("goal.clearConfirm.message"),
        confirmLabel: t("goal.clear"),
        variant: "danger",
      });
    } finally {
      setConfirming(false);
    }
    if (!ok) return;
    await run(props.actions.clear, "goal.error.clear", true);
  };

  const onResume = () => {
    props.actions.sendPrompt(GOAL_RESUME_PROMPT);
    props.onClose();
  };

  const draftLength = objectiveLength(draft);
  const tooLong = draftLength > GOAL_OBJECTIVE_MAX;
  const canSubmit =
    draft.trim() !== "" && !tooLong && !props.generating && !busy;
  const submit = () => {
    if (!canSubmit) return;
    props.actions.sendPrompt(
      goalSetPrompt(
        draft,
        goal ? { model: goal.model, reasoning: goal.reasoning } : undefined,
      ),
    );
    setEditing(false);
    props.onClose();
  };

  const status = goal ? t(goalStatusKey(goal.status)) : "";
  const tone = goal ? goalTone(goal.status) : "muted";
  const check = goal?.lastCheck ?? null;

  const head = (
    <div className="sessions-head goal-popover-head">
      <span className="goal-popover-title">
        <TargetIcon className="goal-popover-icon" size={15} />
        <span>{t("goal.title")}</span>
      </span>
      {goal ? (
        <span
          className={`goal-status-badge goal-tone-${tone}`}
          data-testid="goal-popover-status"
        >
          {status}
        </span>
      ) : null}
      <button
        type="button"
        className="sessions-close goal-popover-close"
        aria-label={t("goal.close")}
        data-testid="goal-popover-close"
        onClick={() => props.onClose()}
      >
        ×
      </button>
    </div>
  );

  const form = (
    <form
      className="goal-form"
      data-testid="goal-form"
      onSubmit={(e) => {
        e.preventDefault();
        submit();
      }}
    >
      {!goal ? <p className="goal-empty">{t("goal.empty")}</p> : null}
      <label className="goal-form-label" htmlFor={fieldId}>
        {t("goal.objective")}
      </label>
      <textarea
        id={fieldId}
        ref={textareaRef}
        className="goal-form-input"
        data-testid="goal-objective-input"
        rows={4}
        value={draft}
        placeholder={t("goal.objectivePlaceholder")}
        aria-invalid={tooLong ? true : undefined}
        onChange={(e) => setDraft(e.target.value)}
        onKeyDown={(e) => {
          // Cmd/Ctrl+Enter submits from the keyboard; Enter alone is a newline,
          // an objective is often more than one line.
          if (
            e.key === "Enter" &&
            (e.metaKey || e.ctrlKey) &&
            !e.nativeEvent.isComposing
          ) {
            e.preventDefault();
            submit();
          }
        }}
      />
      <div className="goal-form-foot">
        <span
          className={`goal-form-count${tooLong ? " goal-form-count--over" : ""}`}
          data-testid="goal-objective-count"
        >
          {t("goal.objectiveCount", {
            count: draftLength,
            max: GOAL_OBJECTIVE_MAX,
          })}
        </span>
        <span className="goal-form-buttons">
          {goal ? (
            <button
              type="button"
              className="goal-btn"
              onClick={() => {
                setEditing(false);
                setError("");
              }}
            >
              {t("common.cancel")}
            </button>
          ) : null}
          <button
            type="submit"
            className="goal-btn goal-btn--primary"
            data-testid="goal-submit"
            disabled={!canSubmit}
          >
            {goal ? t("goal.save") : t("goal.set")}
          </button>
        </span>
      </div>
      {tooLong ? (
        <p className="goal-error" role="alert">
          {t("goal.objectiveTooLong", { max: GOAL_OBJECTIVE_MAX })}
        </p>
      ) : null}
      <p className="goal-note">
        {goal ? t("goal.saveHint") : t("goal.setHint")}
      </p>
    </form>
  );

  // The model the next check runs on and its reasoning level.
  const checker = goal
    ? goal.checkModel ||
      goal.model ||
      goal.lastCheck?.model ||
      (goal.reasoning ? t("goal.checkerDefault") : "")
    : "";
  const level = goal ? checkerLevel(goal, t) : "";
  const details = goal ? (
    <>
      <section className="goal-section">
        <div
          className="goal-objective"
          data-testid="goal-objective"
          tabIndex={0}
          aria-label={t("goal.objective")}
        >
          {goal.objective}
        </div>
        {goal.statusReason ? (
          <p className="goal-reason" data-testid="goal-status-reason">
            {goal.statusReason}
          </p>
        ) : null}
      </section>
      {check ? (
        <section className="goal-section" data-testid="goal-last-check">
          <h3 className="goal-section-title">{t("goal.lastCheck")}</h3>
          <div className="goal-check-head">
            <span
              className={`goal-verdict goal-verdict--${check.verdict === "met" ? "met" : check.verdict === "needs_user" || check.verdict === "impossible" ? "alert" : "open"}`}
            >
              {t(goalVerdictKey(check.verdict))}
            </span>
            {check.verified ? (
              <span className="goal-verified" data-testid="goal-verified">
                {t("goal.verified")}
              </span>
            ) : null}
          </div>
          {check.reason ? (
            <p className="goal-check-reason">{check.reason}</p>
          ) : null}
          {check.remaining.length > 0 ? (
            <>
              <div className="goal-subtitle">{t("goal.remainingTitle")}</div>
              <ul className="goal-remaining">
                {check.remaining.map((r, i) => (
                  <li key={i}>{r}</li>
                ))}
              </ul>
            </>
          ) : null}
        </section>
      ) : null}
      {goal.checklist.length > 0 ? (
        <section className="goal-section" data-testid="goal-checklist">
          <h3 className="goal-section-title">{t("goal.checklist")}</h3>
          <ul className="goal-checklist">
            {goal.checklist.map((item, i) => (
              <ChecklistRow key={i} item={item} />
            ))}
          </ul>
        </section>
      ) : null}
      <dl className="goal-numbers" data-testid="goal-numbers">
        <div>
          <dt>{t("goal.continuations")}</dt>
          <dd>
            {goal.maxContinuations > 0
              ? t("goal.ofLimit", {
                  value: goal.continuations,
                  limit: goal.maxContinuations,
                })
              : String(goal.continuations)}
          </dd>
        </div>
        <div>
          <dt>{t("goal.checks")}</dt>
          <dd>{goal.checks}</dd>
        </div>
        <div>
          <dt>{t("goal.activeTime")}</dt>
          <dd>{formatElapsedSeconds(goal.activeMs)}</dd>
        </div>
        {goal.tokensUsed > 0 || goal.tokenBudget > 0 ? (
          <div>
            <dt>{t("goal.tokens")}</dt>
            <dd>
              {goal.tokenBudget > 0
                ? t("goal.ofLimit", {
                    value: formatTurnTokens(goal.tokensUsed),
                    limit: formatTurnTokens(goal.tokenBudget),
                  })
                : tp("goal.tokensUsed", goal.tokensUsed, {
                    shown: formatTurnTokens(goal.tokensUsed),
                  })}
            </dd>
          </div>
        ) : null}
        {checker ? (
          // A model id does not fit one number's column: it starts a row of
          // the grid and spans two, so its level falls into the third, on
          // the same column lines as the numbers above.
          <div className="goal-numbers-checker">
            <dt>{t("goal.checkedBy")}</dt>
            <dd data-testid="goal-checker">{checker}</dd>
          </div>
        ) : null}
        {checker && level ? (
          <div>
            <dt>{t("goal.reasoning")}</dt>
            <dd data-testid="goal-reasoning">{level}</dd>
          </div>
        ) : null}
      </dl>
    </>
  ) : null;

  const needsTurn = !goal || editing || goalCanResume(goal);
  const actions =
    goal && !editing ? (
      <div className="goal-popover-actions" data-testid="goal-actions">
        {goalCanPause(goal) ? (
          <button
            type="button"
            className="goal-btn"
            data-testid="goal-pause"
            disabled={busy}
            onClick={onPause}
          >
            {t("goal.pause")}
          </button>
        ) : null}
        {goalCanResume(goal) ? (
          <button
            type="button"
            className="goal-btn goal-btn--primary"
            data-testid="goal-resume"
            disabled={busy || props.generating}
            onClick={onResume}
          >
            {t("goal.resume")}
          </button>
        ) : null}
        <button
          type="button"
          className="goal-btn"
          data-testid="goal-edit"
          disabled={busy}
          onClick={startEdit}
        >
          {t("goal.edit")}
        </button>
        <button
          type="button"
          className="goal-btn goal-btn--danger"
          data-testid="goal-clear"
          disabled={busy}
          onClick={() => void onClear()}
        >
          {t("goal.clear")}
        </button>
      </div>
    ) : null;

  const menuStyle: CSSProperties | undefined =
    !props.useSheet && floatRect
      ? {
          left: floatRect.left,
          width: floatRect.width,
          maxHeight: floatRect.maxHeight,
          ...(floatRect.top !== undefined ? { top: floatRect.top } : {}),
          ...(floatRect.bottom !== undefined
            ? { bottom: floatRect.bottom }
            : {}),
        }
      : undefined;

  const panel = (
    <div
      ref={panelRef}
      className={[
        "goal-popover",
        props.useSheet ? "goal-popover--sheet" : "goal-popover--portal",
        goal ? `goal-tone-${tone}` : "",
      ]
        .filter(Boolean)
        .join(" ")}
      role="dialog"
      aria-label={t("goal.title")}
      data-testid="goal-popover"
      tabIndex={-1}
      style={menuStyle}
    >
      <div className="slash-menu-surface" aria-hidden />
      {head}
      <div className="goal-popover-body">
        {showForm ? form : details}
        {error ? (
          <p className="goal-error" role="alert" data-testid="goal-error">
            {error}
          </p>
        ) : null}
        {props.generating && needsTurn ? (
          <p className="goal-note" data-testid="goal-busy-note">
            {t("goal.waitsForTurn")}
          </p>
        ) : null}
      </div>
      {actions}
    </div>
  );

  return createPortal(
    props.useSheet ? (
      <>
        <button
          type="button"
          className="slash-sheet-backdrop"
          aria-label={t("goal.close")}
          tabIndex={-1}
          data-testid="goal-popover-backdrop"
          onMouseDown={(e) => {
            e.preventDefault();
            if (!confirming) props.onClose();
          }}
        />
        {panel}
      </>
    ) : (
      panel
    ),
    document.body,
  );
}
