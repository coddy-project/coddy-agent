import { useId, useRef, useState } from "react";
import type { SpawnAgentDetails } from "../chat/spawnAgentDisplay";
import { useT } from "../i18n/I18nProvider";
import type { BackgroundTask } from "../tasks/types";
import "./SpawnAgentCard.css";

export function SpawnAgentCard(props: {
  details: SpawnAgentDetails;
  backgroundTask?: BackgroundTask | undefined;
  onOpenSession?: (sessionId: string) => void;
}) {
  const { t } = useT();
  const [promptExpanded, setPromptExpanded] = useState(false);
  const promptRef = useRef<HTMLDivElement | null>(null);
  const childSessionId =
    props.backgroundTask?.agent?.session_id?.trim() || null;
  const promptId = useId();

  const collapsePrompt = () => {
    if (promptRef.current) {
      promptRef.current.scrollTop = 0;
    }
    setPromptExpanded(false);
  };

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
          <div className="spawn-agent-name">{props.details.agent}</div>
          {props.details.description ? (
            <div className="spawn-agent-description">
              {props.details.description}
            </div>
          ) : null}
        </div>
      </div>
      <div
        ref={promptRef}
        id={promptId}
        className={[
          "spawn-agent-prompt",
          promptExpanded
            ? "spawn-agent-prompt--scroll"
            : "spawn-agent-prompt--collapsed",
        ].join(" ")}
        aria-label={t("messages.spawnAgentPrompt")}
        aria-expanded={promptExpanded}
      >
        {props.details.prompt}
      </div>
      <button
        type="button"
        className="tool-overflow-toggle spawn-agent-prompt-toggle"
        aria-controls={promptId}
        aria-expanded={promptExpanded}
        onClick={() => {
          if (promptExpanded) {
            collapsePrompt();
          } else {
            setPromptExpanded(true);
          }
        }}
      >
        {promptExpanded ? t("messages.toolLess") : t("messages.toolMore")}
      </button>
      {props.details.model || props.details.timeoutSeconds !== undefined ? (
        <div className="spawn-agent-meta" data-testid="spawn-agent-meta">
          {props.details.model ? (
            <div className="spawn-agent-model">{props.details.model}</div>
          ) : null}
          {props.details.model && props.details.reasoning ? (
            <div className="spawn-agent-reasoning">
              {t("messages.spawnAgentReasoning", {
                reasoning:
                  props.details.reasoning.charAt(0).toUpperCase() +
                  props.details.reasoning.slice(1),
              })}
            </div>
          ) : null}
          {props.details.timeoutSeconds !== undefined ? (
            <div
              className="spawn-agent-timeout"
              title={t("messages.spawnAgentTimeoutHint")}
            >
              {t("messages.spawnAgentTimeout", {
                seconds: props.details.timeoutSeconds,
              })}
            </div>
          ) : null}
        </div>
      ) : null}
      {props.backgroundTask ? (
        <button
          type="button"
          className="spawn-agent-transcript"
          data-testid="spawn-agent-open-transcript"
          disabled={childSessionId === null}
          title={
            childSessionId === null
              ? t("tasks.openTranscriptUnavailable")
              : undefined
          }
          onClick={() => {
            if (childSessionId !== null) {
              props.onOpenSession?.(childSessionId);
            }
          }}
        >
          {t("tasks.openTranscript")}
        </button>
      ) : null}
    </section>
  );
}
