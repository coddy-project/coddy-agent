import { useT } from "../i18n/I18nProvider";

export function BranchNavigator(props: {
  userMessageIndex: number;
  currentIndex: number;
  total: number;
  sessions: Array<{ sessionId: string; preview?: string }>;
  onSwitch: (sessionId: string) => void;
}) {
  const { t } = useT();
  const { currentIndex, total, sessions, onSwitch } = props;
  const hasPrev = currentIndex > 0;
  const hasNext = currentIndex < total - 1;

  return (
    <div className="branch-nav" data-testid="branch-nav">
      <button
        type="button"
        className="branch-nav-btn"
        disabled={!hasPrev}
        aria-label={t("chat.branchPrev")}
        data-testid="branch-nav-prev"
        onClick={() => {
          const s = sessions[currentIndex - 1];
          if (s) onSwitch(s.sessionId);
        }}
      >
        ‹
      </button>
      <span
        className="branch-nav-label"
        aria-label={t("chat.branchLabel", {
          current: currentIndex + 1,
          total,
        })}
        data-testid="branch-nav-label"
      >
        {currentIndex + 1}/{total}
      </span>
      <button
        type="button"
        className="branch-nav-btn"
        disabled={!hasNext}
        aria-label={t("chat.branchNext")}
        data-testid="branch-nav-next"
        onClick={() => {
          const s = sessions[currentIndex + 1];
          if (s) onSwitch(s.sessionId);
        }}
      >
        ›
      </button>
    </div>
  );
}
