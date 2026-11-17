import { useT } from "../i18n/I18nProvider";

export function ViewedProgress({
  viewed,
  total,
}: {
  viewed: number;
  total: number;
}) {
  const { t } = useT();
  const complete = total > 0 && viewed === total;
  const fraction = total > 0 ? viewed / total : 0;
  const title = t("changes.viewer.viewedSummaryTitle", { viewed, total });
  return (
    <span
      className={"edits-viewed-summary" + (complete ? " is-complete" : "")}
      data-testid="edits-viewed-summary"
      title={title}
      role="img"
      aria-label={title}
    >
      <svg viewBox="0 0 16 16" width="16" height="16" aria-hidden="true">
        <circle className="edits-viewed-track" cx="8" cy="8" r="5.5" />
        <circle
          className="edits-viewed-value"
          cx="8"
          cy="8"
          r="5.5"
          pathLength={1}
          strokeDasharray={1}
          strokeDashoffset={1 - fraction}
          transform="rotate(-90 8 8)"
        />
      </svg>
      <span>
        {viewed} / {total}{" "}
        <span className="edits-viewed-summary-label">
          {t("changes.viewer.viewedSummaryLabel")}
        </span>
      </span>
    </span>
  );
}
