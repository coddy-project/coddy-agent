import type { SpawnAgentDetails } from "../chat/spawnAgentDisplay";
import { useT } from "../i18n/I18nProvider";
import "./SpawnAgentCard.css";

export function SpawnAgentCard({ details }: { details: SpawnAgentDetails }) {
  const { t } = useT();
  return (
    <section
      className="spawn-agent-card"
      aria-label={t("messages.spawnAgentDetails")}
    >
      <div className="spawn-agent-header">
        <span className="spawn-agent-icon" aria-hidden="true">
          <svg
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth="1.6"
            strokeLinecap="round"
            strokeLinejoin="round"
          >
            <path d="M12 3v3M9 3h6M3 11v5m18-5v5" />
            <rect x="6" y="6" width="12" height="14" rx="4" />
            <path d="M9 11v1m6-1v1m-6 4h6" />
          </svg>
        </span>
        <div className="spawn-agent-identity">
          <div className="spawn-agent-name">{details.agent}</div>
          {details.description ? (
            <div className="spawn-agent-description">{details.description}</div>
          ) : null}
        </div>
        {details.timeoutSeconds !== undefined ? (
          <span
            className="spawn-agent-timeout"
            title={t("messages.spawnAgentTimeoutHint")}
          >
            <svg
              aria-hidden="true"
              viewBox="0 0 24 24"
              fill="none"
              stroke="currentColor"
              strokeWidth="1.6"
              strokeLinecap="round"
              strokeLinejoin="round"
            >
              <circle cx="12" cy="12" r="8" />
              <path d="M12 7v5l3 2" />
            </svg>
            {t("messages.spawnAgentTimeout", {
              seconds: details.timeoutSeconds,
            })}
          </span>
        ) : null}
      </div>
      <div
        className="spawn-agent-prompt"
        aria-label={t("messages.spawnAgentPrompt")}
      >
        {details.prompt}
      </div>
    </section>
  );
}
