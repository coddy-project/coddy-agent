import {
  useCallback,
  useEffect,
  useMemo,
  useState,
  startTransition,
} from "react";

import type {
  CoddyQuestionItem,
  CoddyQuestionPayload,
  QuestionResolvedState,
} from "./questionTypes";
import { letterForOptionIndex } from "./questionTypes";

const HDR = "X-Coddy-Session-ID";

const OTHER_SENTINEL = "__coddy_other__";

/** Move focus back to the composer after answering a gate question. */
export function questionPromptFocusComposer(): void {
  window.requestAnimationFrame(() => {
    const el =
      document.querySelector<HTMLElement>(
        '[data-slot="composer"] textarea',
      ) ?? document.querySelector<HTMLElement>('[data-slot="composer"]');

    try {
      el?.focus?.({ preventScroll: true });
    } catch {
      try {
        el?.focus?.();
      } catch {
        // ignore DOM focus failures
      }
    }
    try {
      el?.scrollIntoView({ block: "nearest", inline: "nearest" });
    } catch {
      // ignore scroll failures
    }
  });
}

function buildAnswerRows(
  payload: CoddyQuestionPayload,
  multiSel: string[][],
  singleSel: string[],
  extraText: string[],
  skipped: boolean,
): string[][] {
  if (skipped) return payload.questions.map(() => []);
  const out: string[][] = [];
  for (let qi = 0; qi < payload.questions.length; qi++) {
    const q = payload.questions[qi];
    if (!q) {
      out.push([]);
      continue;
    }
    const cells: string[] = [];
    if (q.multiple) {
      const sel = multiSel[qi] || [];
      const wantsFree = sel.includes(OTHER_SENTINEL);
      for (const lab of sel) {
        if (lab !== OTHER_SENTINEL) cells.push(lab);
      }
      const ex = String(extraText[qi] ?? "").trim();
      if (wantsFree && ex.length > 0) cells.push(ex);
    } else {
      const sel = String(singleSel[qi] ?? "").trim();
      const ex = String(extraText[qi] ?? "").trim();
      if (sel === OTHER_SENTINEL) {
        if (ex.length > 0) cells.push(ex);
      } else if (sel) {
        cells.push(sel);
      }
    }
    out.push(cells);
  }
  return out;
}

function allAnsweredNonEmpty(rows: string[][]): boolean {
  return rows.every((r) =>
    [...r].map((x) => String(x).trim()).some((x) => x.length > 0),
  );
}

function readyToSubmit(args: {
  questions: CoddyQuestionItem[];
  multiSel: string[][];
  singleSel: string[];
  extraText: string[];
}): boolean {
  const minimalPayload: CoddyQuestionPayload = {
    sessionId: "",
    requestId: "",
    questions: args.questions,
  };

  const rows = buildAnswerRows(
    minimalPayload,
    args.multiSel,
    args.singleSel,
    args.extraText,
    false,
  );

  const n = args.questions.length;
  for (let qi = 0; qi < n; qi++) {
    const q = args.questions[qi];
    if (!q) return false;

    if (q.multiple) {
      const sel = args.multiSel[qi] || [];
      const wantsFree = sel.includes(OTHER_SENTINEL);
      const picks = sel.filter((l) => l !== OTHER_SENTINEL);
      const ex = String(args.extraText[qi] ?? "").trim();
      if (wantsFree && ex.length === 0) return false;
      const ok =
        picks.length > 0 || (wantsFree && ex.length > 0);
      if (!ok) return false;
    } else {
      const pick = String(args.singleSel[qi] ?? "").trim();
      if (pick === OTHER_SENTINEL) {
        if (String(args.extraText[qi] ?? "").trim().length === 0) {
          return false;
        }
      }
    }
  }

  return allAnsweredNonEmpty(rows);
}

function formatResolvedSummaryLine(
  questions: CoddyQuestionItem[],
  skipped: boolean,
  answersMatrix: string[][],
): string {
  if (!questions.length || skipped) {
    return "Skipped";
  }
  const parts: string[] = [];
  for (let qi = 0; qi < questions.length; qi++) {
    const q = questions[qi];
    if (!q) continue;
    const joined = [...(answersMatrix[qi] ?? [])]
      .map((s) => String(s).trim())
      .filter((s) => s.length > 0)
      .join(", ");
    const ansText = joined.length > 0 ? joined : "(no answer)";
    let stem = q.question.trim().replace(/\s+/g, " ");
    if (stem.length > 112) stem = `${stem.slice(0, 109)}...`;
    const qDisp = stem.endsWith("?") ? stem : `${stem}?`;
    parts.push(`${qDisp} ${ansText}`);
  }
  return parts.length > 0 ? parts.join(" · ") : "Answered";
}

function rowLettersForQuestion(q: CoddyQuestionItem): readonly string[] {
  const opts = Math.max(q.options?.length ?? 0, 0);
  const total = opts + (q.custom ? 1 : 0);
  const list: string[] = [];
  for (let i = 0; i < Math.min(total, 26); i++) list.push(letterForOptionIndex(i));
  return list;
}

export type QuestionPromptSectionProps = {
  itemId: string;
  payload: CoddyQuestionPayload;
  resolved?: QuestionResolvedState | undefined;
  onResolved: (resolution: QuestionResolvedState) => void;
};

/** Inline gated questions for streaming question SSE followed by POST /coddy/sessions/{id}/question. */
export function QuestionPromptSection(props: QuestionPromptSectionProps) {
  const { itemId, payload, resolved, onResolved } = props;
  const qs = payload.questions;
  const n = qs.length;

  const questionsSig = JSON.stringify(
    qs.map((q) => ({
      qq: q.question,
      oo: q.options.map((o) => o.label),
      m: q.multiple === true,
      c: q.custom === true,
    })),
  );

  const [multiSel, setMultiSel] = useState<string[][]>(() => qs.map(() => []));
  const [singleSel, setSingleSel] = useState<string[]>(() =>
    qs.map(() => ""),
  );
  const [extraText, setExtraText] = useState<string[]>(() =>
    qs.map(() => ""),
  );
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    setMultiSel(qs.map(() => []));
    setSingleSel(qs.map(() => ""));
    setExtraText(qs.map(() => ""));
    setSubmitting(false);
  }, [itemId, payload.requestId, questionsSig]);

  const ready = useMemo(
    () =>
      readyToSubmit({
        questions: qs,
        multiSel,
        singleSel,
        extraText,
      }),
    [qs, multiSel, singleSel, extraText],
  );

  const submit = useCallback(
    async (skip: boolean) => {
      const sid = payload.sessionId.trim();
      const answersMatrix = buildAnswerRows(
        payload,
        multiSel,
        singleSel,
        extraText,
        skip,
      );
      const summaryLine = formatResolvedSummaryLine(qs, skip, answersMatrix);
      setSubmitting(true);
      try {
        try {
          await fetch(`/coddy/sessions/${encodeURIComponent(sid)}/question`, {
            method: "POST",
            headers: {
              "Content-Type": "application/json",
              [HDR]: sid,
            },
            body: JSON.stringify({
              requestId: payload.requestId,
              answers: answersMatrix,
            }),
          });
        } catch {
          // still unblock transcript even if POST fails transiently
        }
        startTransition(() => {
          onResolved({
            skipped: skip,
            answers: answersMatrix,
            summaryLine,
          });
        });
      } finally {
        setSubmitting(false);
      }

      questionPromptFocusComposer();
    },
    [extraText, multiSel, onResolved, payload, qs, singleSel],
  );

  useEffect(() => {
    if (resolved) return;
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return;
      e.preventDefault();
      if (submitting) return;
      void submit(true);
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [resolved, submit, submitting]);

  if (resolved) {
    const sum = resolved.summaryLine.trim() || "Answered";
    return (
      <section
        className="question-prompt-frame"
        data-test="question_prompt_resolved"
      >
        <details className="question-prompt-card question-prompt-collapsed">
          <summary className="question-prompt-head question-prompt-head--stack">
            <div className="question-prompt-head-left">
              <span className="question-prompt-icon" aria-hidden />
              <span className="question-prompt-title">Questions</span>
            </div>
            <span className="question-prompt-summary-line">{sum}</span>
          </summary>
          <div className="question-prompt-body question-prompt-resolved-body">
            {resolved.skipped ? (
              <p className="question-prompt-skipped-note">Skipped</p>
            ) : null}
            {qs.map((q, qi) => {
              const parts = (resolved.answers[qi] ?? [])
                .map((s) => String(s).trim())
                .filter((s) => s.length > 0);
              const aText = parts.length > 0 ? parts.join(", ") : "-";
              return (
                <div
                  key={`${qi}-${q.question}`}
                  className={
                    qi === 0
                      ? undefined
                      : "question-prompt-resolved-block"
                  }
                >
                  <div className="question-prompt-resolved-pair">
                    <div className="question-prompt-resolved-q">{q.question}</div>
                    <div className="question-prompt-resolved-a">{aText}</div>
                  </div>
                </div>
              );
            })}
          </div>
        </details>
      </section>
    );
  }

  return (
    <section className="question-prompt-frame" data-test="question_prompt_section">
      <div className="question-prompt-card">
        <div className="question-prompt-head">
          <div className="question-prompt-head-left">
            <span className="question-prompt-icon" aria-hidden />
            <h3 className="question-prompt-title">Questions</h3>
          </div>
        </div>

        <div className="question-prompt-body">
          {qs.map((q, qi) => {
            const letters = rowLettersForQuestion(q);
            return (
              <div
                key={`${qi}-${q.question}`}
                className={qi === 0 ? undefined : "question-prompt-resolved-block"}
              >
                <div className="question-prompt-qline">
                  {n > 1 ? (
                    <span className="question-prompt-qnum">{qi + 1}.</span>
                  ) : null}
                  <p className="question-prompt-qtext">{q.question}</p>
                </div>

                <ul className="question-prompt-rows" aria-label={`Options ${qi + 1}`}>
                  {q.options.map((op, oi) => {
                    const bubble = letters[oi];
                    if (!bubble) return null;
                    if (q.multiple) {
                      const checked =
                        (multiSel[qi] || []).indexOf(op.label) >= 0;
                      return (
                        <li key={op.label} className="question-prompt-li">
                          <label
                            className={
                              "question-prompt-row" +
                              (checked ? " question-prompt-row--active" : "")
                            }
                          >
                            <input
                              className="sr-only"
                              type="checkbox"
                              checked={checked}
                              disabled={submitting}
                              onChange={(e) => {
                                const on = e.target.checked;
                                setMultiSel((prev) => {
                                  const next = [...prev];
                                  const set = new Set(next[qi] || []);
                                  if (on) set.add(op.label);
                                  else set.delete(op.label);
                                  next[qi] = Array.from(set);
                                  return next;
                                });
                              }}
                            />
                            <span className="question-prompt-bubble">{bubble}</span>
                            <span className="question-prompt-row-text">
                              {op.label}
                              {op.description ? (
                                <span className="muted"> - {op.description}</span>
                              ) : null}
                            </span>
                          </label>
                        </li>
                      );
                    }
                    const picked = singleSel[qi] === op.label;
                    return (
                      <li key={op.label} className="question-prompt-li">
                        <label
                          className={
                            "question-prompt-row" +
                            (picked ? " question-prompt-row--active" : "")
                          }
                        >
                          <input
                            className="sr-only"
                            type="radio"
                            name={`${itemId}-pick-${qi}`}
                            checked={picked}
                            disabled={submitting}
                            onMouseDown={(e) => {
                              if (picked) {
                                e.preventDefault();
                                setSingleSel((prev) => {
                                  const nx = [...prev];
                                  nx[qi] = "";
                                  return nx;
                                });
                              }
                            }}
                            onChange={() => {
                              setSingleSel((prev) => {
                                const nx = [...prev];
                                nx[qi] = op.label;
                                return nx;
                              });
                            }}
                          />
                          <span className="question-prompt-bubble">{bubble}</span>
                          <span className="question-prompt-row-text">
                            {op.label}
                            {op.description ? (
                              <span className="muted"> - {op.description}</span>
                            ) : null}
                          </span>
                        </label>
                      </li>
                    );
                  })}

                  {!q.multiple && q.custom ? (
                    <li className="question-prompt-li">
                      <label
                        className={
                          "question-prompt-row question-prompt-row--other" +
                          (singleSel[qi] === OTHER_SENTINEL
                            ? " question-prompt-row--active"
                            : "")
                        }
                      >
                        <input
                          className="sr-only"
                          type="radio"
                          name={`${itemId}-pick-${qi}`}
                          checked={singleSel[qi] === OTHER_SENTINEL}
                          disabled={submitting}
                          onMouseDown={(e) => {
                            if (singleSel[qi] === OTHER_SENTINEL) {
                              e.preventDefault();
                              setSingleSel((prev) => {
                                const nx = [...prev];
                                nx[qi] = "";
                                return nx;
                              });
                              setExtraText((prev) => {
                                const nx = [...prev];
                                nx[qi] = "";
                                return nx;
                              });
                            }
                          }}
                          onChange={() => {
                            setSingleSel((prev) => {
                              const nx = [...prev];
                              nx[qi] = OTHER_SENTINEL;
                              return nx;
                            });
                          }}
                        />
                        <span className="question-prompt-bubble">
                          {letters[q.options.length] ?? "?"}
                        </span>
                        <input
                          className="question-prompt-other-input"
                          type="text"
                          value={extraText[qi] || ""}
                          autoComplete="off"
                          spellCheck={false}
                          disabled={submitting}
                          placeholder="Other…"
                          aria-label="Other, type your answer"
                          data-testid={`question-other-${qi}`}
                          onFocus={() => {
                            setSingleSel((prev) => {
                              const nx = [...prev];
                              nx[qi] = OTHER_SENTINEL;
                              return nx;
                            });
                          }}
                          onChange={(e) => {
                            const v = e.target.value;
                            setExtraText((prev) => {
                              const nx = [...prev];
                              nx[qi] = v;
                              return nx;
                            });
                          }}
                          onKeyDown={(e) => {
                            if (e.key === "Enter") {
                              e.preventDefault();
                            }
                          }}
                        />
                      </label>
                    </li>
                  ) : null}

                  {q.multiple && q.custom ? (
                    <li className="question-prompt-li">
                      <label
                        className={
                          "question-prompt-row question-prompt-row--other" +
                          ((multiSel[qi] || []).includes(OTHER_SENTINEL)
                            ? " question-prompt-row--active"
                            : "")
                        }
                      >
                        <input
                          className="sr-only"
                          type="checkbox"
                          checked={(multiSel[qi] || []).includes(
                            OTHER_SENTINEL,
                          )}
                          disabled={submitting}
                          onChange={(e) => {
                            const on = e.target.checked;
                            setMultiSel((prev) => {
                              const next = [...prev];
                              const set = new Set(next[qi] || []);
                              if (on) set.add(OTHER_SENTINEL);
                              else set.delete(OTHER_SENTINEL);
                              next[qi] = Array.from(set);
                              return next;
                            });
                          }}
                        />
                        <span className="question-prompt-bubble">
                          {letters[q.options.length] ?? "?"}
                        </span>
                        <input
                          className="question-prompt-other-input"
                          type="text"
                          value={extraText[qi] || ""}
                          autoComplete="off"
                          spellCheck={false}
                          disabled={submitting}
                          placeholder="Other…"
                          aria-label="Other, type your answer"
                          data-testid={`question-other-multi-${qi}`}
                          onFocus={() => {
                            setMultiSel((prev) => {
                              const next = [...prev];
                              const set = new Set(next[qi] || []);
                              set.add(OTHER_SENTINEL);
                              next[qi] = Array.from(set);
                              return next;
                            });
                          }}
                          onChange={(e) => {
                            const v = e.target.value;
                            setExtraText((prev) => {
                              const nx = [...prev];
                              nx[qi] = v;
                              return nx;
                            });
                          }}
                          onKeyDown={(e) => {
                            if (e.key === "Enter") {
                              e.preventDefault();
                            }
                          }}
                        />
                      </label>
                    </li>
                  ) : null}
                </ul>
              </div>
            );
          })}
        </div>

        <div className="question-prompt-foot">
          <button
            type="button"
            className="question-prompt-skip"
            disabled={submitting}
            data-testid="question-skip"
            onClick={() => void submit(true)}
          >
            Skip<span className="question-prompt-kbd">Esc</span>
          </button>
          <button
            type="button"
            className="question-prompt-continue"
            disabled={submitting || !ready}
            data-testid="question-submit"
            onClick={() => void submit(false)}
          >
            Continue<span className="question-prompt-continue-ic" aria-hidden>↵</span>
          </button>
        </div>
      </div>
    </section>
  );
}
