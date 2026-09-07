import { memo, useEffect, useRef, useState } from "react";

import {
  formatElapsedSeconds,
  waitingStatusKey,
  type LiveStatusKind,
} from "../chat/liveStatus";
import { useT } from "../i18n/I18nProvider";

/** States that are blocked on the user, where a climbing counter would be a lie. */
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
  startedAtMs?: number;
}) {
  const { t } = useT();
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

  const [nowMs, setNowMs] = useState(() => Date.now());

  useEffect(() => {
    if (started === null) {
      return;
    }
    setNowMs(Date.now());
    let interval = 0;
    // One tick per visible change, aligned to the second boundary so the number never
    // appears to skip or stall (a plain setInterval armed mid-second does).
    const align = 1000 - ((Date.now() - started) % 1000);
    const timeout = window.setTimeout(() => {
      setNowMs(Date.now());
      interval = window.setInterval(() => setNowMs(Date.now()), 1000);
    }, align);
    return () => {
      window.clearTimeout(timeout);
      if (interval) {
        window.clearInterval(interval);
      }
    };
  }, [started]);

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
  const elapsed = elapsedMs === null ? "" : formatElapsedSeconds(elapsedMs);

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

export const TypingDotsMessage = memo(TypingDotsMessageImpl);
