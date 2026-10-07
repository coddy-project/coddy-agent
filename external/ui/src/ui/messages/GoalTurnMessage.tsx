import { memo, useLayoutEffect, useRef, useState } from "react";

import { Chevron } from "../components/Chevron";
import { TargetIcon } from "../components/TargetIcon";
import { goalTurnText, type GoalTurn } from "../chat/goal";
import { useT } from "../i18n/I18nProvider";

/**
 * Past this many characters the row's line is taken as cut before it is
 * measured (and where nothing is measured: no layout, no ResizeObserver).
 */
const GOAL_ROW_FULL_TEXT_CHARS = 140;

/**
 * GoalTurnMessage stands where a turn the session supervisor started for the
 * goal begins: one compact row ("Goal continuation 2 of 10: tests still
 * fail"), never a user bubble, since nobody typed the instruction the model
 * read. The items the last check left open fold out under it; so does the
 * whole text when the row's two lines cut it, as they do a long objective on a
 * phone. Styled like the thinking and compaction rows (DESIGN.md, Session
 * goal).
 */
export const GoalTurnMessage = memo(function GoalTurnMessage(props: {
  turn: GoalTurn;
  /** The transcript row id, stamped on the row so the transcript window can
   *  find it on screen. */
  rowId?: string;
}) {
  const { t, tp } = useT();
  const text = goalTurnText(props.turn, t);
  const remaining = props.turn.remaining;
  const textRef = useRef<HTMLSpanElement | null>(null);
  // Whether the line clamp cuts the text at the row's width now; a phone cuts
  // a line a desktop shows whole, so it is read from the layout.
  const [clamped, setClamped] = useState(false);
  const long = clamped || text.length > GOAL_ROW_FULL_TEXT_CHARS;
  const foldable = remaining.length > 0 || long;
  useLayoutEffect(() => {
    const el = textRef.current;
    if (!el) return undefined;
    const measure = () => setClamped(el.scrollHeight > el.clientHeight + 1);
    measure();
    if (typeof ResizeObserver === "undefined") return undefined;
    const ro = new ResizeObserver(measure);
    ro.observe(el);
    return () => ro.disconnect();
    // The span is a new node when the row turns into a fold.
  }, [text, foldable]);
  const head = (
    <span className="goal-turn-head">
      <TargetIcon className="goal-turn-icon" size={14} />
      <span
        ref={textRef}
        className="goal-turn-text"
        title={long ? text : undefined}
      >
        {text}
      </span>
      {remaining.length > 0 ? (
        <span className="goal-turn-count">
          {tp("goal.turn.remaining", remaining.length)}
        </span>
      ) : null}
    </span>
  );
  if (!foldable) {
    return (
      <div
        className="thinking-row goal-turn-row"
        data-row-id={props.rowId}
        data-testid="goal-turn-row"
        data-goal-kind={props.turn.kind}
      >
        <div className="thinking-summary goal-turn-summary">
          <span className="thinking-left">{head}</span>
        </div>
      </div>
    );
  }
  return (
    <div
      className="thinking-row goal-turn-row"
      data-row-id={props.rowId}
      data-testid="goal-turn-row"
      data-goal-kind={props.turn.kind}
    >
      <details className="thinking-details">
        <summary className="thinking-summary goal-turn-summary">
          <span className="thinking-left">
            <Chevron className="thinking-chevron" />
            {head}
          </span>
        </summary>
        <div className="thinking-body goal-turn-body">
          {long ? <p className="goal-turn-full">{text}</p> : null}
          {remaining.length > 0 ? (
            <>
              <div className="goal-turn-body-title">
                {t("goal.remainingTitle")}
              </div>
              <ul className="goal-turn-remaining">
                {remaining.map((r, i) => (
                  <li key={i}>{r}</li>
                ))}
              </ul>
            </>
          ) : null}
        </div>
      </details>
    </div>
  );
});
