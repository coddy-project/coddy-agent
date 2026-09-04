import {
  useCallback,
  useEffect,
  useMemo,
  useState,
  startTransition,
} from "react";

import type {
  CoddyQuestionPayload,
  QuestionResolvedState,
} from "./questionTypes";
import {
  OTHER_SENTINEL,
  buildAnswerRows,
  formatResolvedSummaryLine,
  readyToSubmit,
  rowLettersForQuestion,
} from "./questionAnswers";
import { questionPromptFocusComposer } from "./questionPromptFocus";
import { QuestionResolvedCard } from "./QuestionResolvedCard";
import { useT } from "../i18n/I18nProvider";

const HDR = "X-Coddy-Session-ID";

export type QuestionPromptSectionProps = {
  itemId: string;
  payload: CoddyQuestionPayload;
  resolved?: QuestionResolvedState | undefined;
  onResolved: (resolution: QuestionResolvedState) => void;
};

/** Inline gated questions for streaming question SSE followed by POST /coddy/sessions/{id}/question. */
export function QuestionPromptSection(props: QuestionPromptSectionProps) {
  const { t } = useT();
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
    return <QuestionResolvedCard questions={qs} resolved={resolved} />;
  }

  return (
    <section className="question-prompt-frame" data-test="question_prompt_section">
      <div className="question-prompt-card">
        <div className="question-prompt-head">
          <div className="question-prompt-head-left">
            <span className="question-prompt-icon" aria-hidden />
            <h3 className="question-prompt-title">{t("prompts.questions")}</h3>
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

                <ul className="question-prompt-rows" aria-label={t("prompts.optionsAriaLabel", { index: qi + 1 })}>
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
                          placeholder={t("prompts.otherPlaceholder")}
                          aria-label={t("prompts.otherAriaLabel")}
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
                          placeholder={t("prompts.otherPlaceholder")}
                          aria-label={t("prompts.otherAriaLabel")}
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
            {t("prompts.skip")}
            <span className="question-prompt-kbd">Esc</span>
          </button>
          <button
            type="button"
            className="question-prompt-continue"
            disabled={submitting || !ready}
            data-testid="question-submit"
            onClick={() => void submit(false)}
          >
            {t("prompts.continue")}
            <span className="question-prompt-continue-ic" aria-hidden>↵</span>
          </button>
        </div>
      </div>
    </section>
  );
}
