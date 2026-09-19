import { memo, useEffect, useRef, useState } from "react";

import {
  formatElapsedSeconds,
  stepShowsItsOwnClock,
  waitingStatusKey,
  type LiveStatusKind,
} from "../chat/liveStatus";
import { formatTurnTokens } from "../chat/turnProgress";
import { useT } from "../i18n/I18nProvider";

/** States that are blocked on the user, where a climbing step counter would be a lie. */
function isBlockedOnUser(kind: LiveStatusKind | undefined): boolean {
  return kind === "permission" || kind === "question";
}

function TypingDotsMessageImpl(props: {
  /** Omit to render bare dots (no live status available). */
  statusKind?: LiveStatusKind;
  statusKey?: string;
  /** Already truncated for display. */
  statusTarget?: string;
  /** Untruncated target for the title tooltip. */
  statusTargetFull?: string;
  /** When the current step started; the component stamps its own when omitted. */
  startedAtMs?: number;
  /**
   * When the turn started, on this machine's clock: the server's `turn_progress`, or the
   * creation time of the turn's user message. Omitted, the line counts from when it
   * appeared.
   */
  turnStartedAtMs?: number;
  /** Tokens the model has generated in this turn; the segment hides while there are none. */
  turnTokens?: number;
  /** Background tasks running right now; the segment hides at zero. */
  runningTasks?: number;
  /** Opens the Tasks panel from the running-tasks segment. */
  onOpenTasks?: () => void;
}) {
  const { t, tp } = useT();
  const showStatus = props.statusKind !== undefined;

  // Identity of the current step; a change means a new step and a fresh count.
  const identity = `${props.statusKind || ""}|${props.statusKey || ""}|${props.statusTarget || ""}`;
  const stampRef = useRef<{ id: string; at: number }>({
    id: identity,
    at: Date.now(),
  });
  if (stampRef.current.id !== identity) {
    // Derived from props, so it is stamped during render: the first frame has to count
    // from the right zero, not from the next tick.
    stampRef.current = { id: identity, at: Date.now() };
  }

  const counts = showStatus && !isBlockedOnUser(props.statusKind);
  const started = !counts
    ? null
    : typeof props.startedAtMs === "number" &&
        Number.isFinite(props.startedAtMs)
      ? props.startedAtMs
      : stampRef.current.at;

  // The row mounts when the turn starts generating and unmounts when it ends, so its
  // own first render is the best reading of the turn's start when nothing names one.
  const mountedAtRef = useRef(Date.now());
  const turnStarted = !showStatus
    ? null
    : typeof props.turnStartedAtMs === "number" &&
        Number.isFinite(props.turnStartedAtMs)
      ? props.turnStartedAtMs
      : mountedAtRef.current;

  const [nowMs, setNowMs] = useState(() => Date.now());

  // One ticker for both clocks, aligned to the turn's: two timers a fraction of a
  // second apart would make the line redraw twice a second for nothing.
  const tickFrom = turnStarted ?? started;
  useEffect(() => {
    if (tickFrom === null) {
      return;
    }
    setNowMs(Date.now());
    let interval = 0;
    // One tick per visible change, aligned to the second boundary so the number never
    // appears to skip or stall (a plain setInterval armed mid-second does).
    const phase = (((Date.now() - tickFrom) % 1000) + 1000) % 1000;
    const timeout = window.setTimeout(() => {
      setNowMs(Date.now());
      interval = window.setInterval(() => setNowMs(Date.now()), 1000);
    }, 1000 - phase);
    return () => {
      window.clearTimeout(timeout);
      if (interval) {
        window.clearInterval(interval);
      }
    };
  }, [tickFrom]);

  if (!showStatus) {
    return (
      <div className="msg-assistant-stack" data-testid="typing-dots">
        <div
          className="typing-dots"
          aria-label={t("messages.preparingResponse")}
          aria-live="polite"
        >
          <span className="typing-dots-dot" aria-hidden="true" />
          <span className="typing-dots-dot" aria-hidden="true" />
          <span className="typing-dots-dot" aria-hidden="true" />
        </div>
      </div>
    );
  }

  const elapsedMs = started === null ? null : Math.max(0, nowMs - started);
  // The model's own phases are covered by the turn clock; a second clock equal to the
  // first reads as a glitch. A step that runs something else keeps its own.
  const elapsed =
    elapsedMs === null || !stepShowsItsOwnClock(props.statusKind)
      ? ""
      : formatElapsedSeconds(elapsedMs);
  const turnElapsed =
    turnStarted === null
      ? ""
      : formatElapsedSeconds(Math.max(0, nowMs - turnStarted));
  const turnTokens =
    typeof props.turnTokens === "number" && props.turnTokens > 0
      ? Math.floor(props.turnTokens)
      : 0;
  const runningTasks =
    typeof props.runningTasks === "number" && props.runningTasks > 0
      ? Math.floor(props.runningTasks)
      : 0;

  // Only the ticking component knows how long the wait has run, so the waiting phrase is
  // chosen here rather than in deriveLiveStatus.
  const key =
    props.statusKind === "waiting" && elapsedMs !== null
      ? waitingStatusKey(elapsedMs)
      : props.statusKey || "status.waitingModel";
  const verb = t(key);
  const slow = key === "status.waitingSlow" || key === "status.waitingStuck";
  const target = props.statusTarget || "";
  const titleText = props.statusTargetFull || "";

  return (
    <div className="msg-assistant-stack" data-testid="typing-dots">
      <div className="typing-dots">
        {/* The status node must stay after the three dots: :nth-child(2)/(3) carry the
            bounce stagger, so prepending would silently break the animation. */}
        <span className="typing-dots-dot" aria-hidden="true" />
        <span className="typing-dots-dot" aria-hidden="true" />
        <span className="typing-dots-dot" aria-hidden="true" />
        <span
          className={
            "typing-dots-status" + (slow ? " typing-dots-status--slow" : "")
          }
          data-testid="typing-dots-status"
          {...(titleText ? { title: titleText } : {})}
        >
          {turnElapsed ? (
            <span className="typing-dots-turn">
              <span
                className="typing-dots-turn-item typing-dots-turn-time"
                data-testid="typing-dots-turn-elapsed"
                aria-hidden="true"
              >
                {turnElapsed}
              </span>
              {turnTokens > 0 ? (
                <span
                  className="typing-dots-turn-item"
                  data-testid="typing-dots-turn-tokens"
                  aria-hidden="true"
                >
                  {tp("status.turnTokens", turnTokens, {
                    shown: formatTurnTokens(turnTokens),
                  })}
                </span>
              ) : null}
              {runningTasks > 0 ? (
                <TurnTasksSegment
                  label={tp("tasks.running", runningTasks)}
                  {...(props.onOpenTasks
                    ? {
                        onOpen: props.onOpenTasks,
                        aria: t("tasks.openAria", {
                          label: tp("tasks.running", runningTasks),
                        }),
                      }
                    : {})}
                />
              ) : null}
            </span>
          ) : null}
          <span
            className="typing-dots-status-text"
            role="status"
            aria-live="polite"
            aria-atomic="true"
          >
            <span className="typing-dots-status-verb">{verb}</span>
            {target ? (
              <span className="typing-dots-status-target">{target}</span>
            ) : null}
          </span>
          {elapsed ? (
            <span
              className="typing-dots-status-time"
              data-testid="typing-dots-elapsed"
              aria-hidden="true"
            >
              {elapsed}
            </span>
          ) : null}
        </span>
      </div>
    </div>
  );
}

/** The running-tasks segment: a button when the panel can be opened from here. */
function TurnTasksSegment(props: {
  label: string;
  onOpen?: () => void;
  aria?: string;
}) {
  if (!props.onOpen) {
    return (
      <span
        className="typing-dots-turn-item"
        data-testid="typing-dots-turn-tasks"
      >
        {props.label}
      </span>
    );
  }
  // The middle dot that closes the segment belongs to the wrapper: inside the button
  // it would be part of the control, underlined on hover and clickable.
  return (
    <span className="typing-dots-turn-item">
      <button
        type="button"
        className="typing-dots-turn-tasks"
        data-testid="typing-dots-turn-tasks"
        {...(props.aria ? { "aria-label": props.aria } : {})}
        onClick={props.onOpen}
      >
        {props.label}
      </button>
    </span>
  );
}

export const TypingDotsMessage = memo(TypingDotsMessageImpl);
