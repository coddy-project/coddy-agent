import type {
  CoddyQuestionItem,
  QuestionResolvedState,
} from "./questionTypes";
import { useT } from "../i18n/I18nProvider";

/** Collapsed summary card shown once a question prompt has been answered or skipped. */
export function QuestionResolvedCard({
  questions,
  resolved,
}: {
  questions: CoddyQuestionItem[];
  resolved: QuestionResolvedState;
}) {
  const { t } = useT();
  const sum = resolved.summaryLine.trim() || t("prompts.answered");
  return (
    <section
      className="question-prompt-frame"
      data-test="question_prompt_resolved"
    >
      <details className="question-prompt-card question-prompt-collapsed">
        <summary className="question-prompt-head question-prompt-head--stack">
          <div className="question-prompt-head-left">
            <span className="question-prompt-icon" aria-hidden />
            <span className="question-prompt-title">
              {t("prompts.questions")}
            </span>
          </div>
          <span className="question-prompt-summary-line">{sum}</span>
        </summary>
        <div className="question-prompt-body question-prompt-resolved-body">
          {resolved.skipped ? (
            <p className="question-prompt-skipped-note">
              {t("prompts.skipped")}
            </p>
          ) : null}
          {questions.map((q, qi) => {
            const parts = (resolved.answers[qi] ?? [])
              .map((s) => String(s).trim())
              .filter((s) => s.length > 0);
            const aText = parts.length > 0 ? parts.join(", ") : "-";
            return (
              <div
                key={`${qi}-${q.question}`}
                className={
                  qi === 0 ? undefined : "question-prompt-resolved-block"
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
