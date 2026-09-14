import { useCallback, useMemo, useState } from "react";
import { useT } from "../i18n/I18nProvider";
import { PermissionToolPreview } from "../chat/PermissionPromptPreview";
import { buildPermissionToolPreview } from "../chat/permissionToolPreview";
import { permissionOptionLabel } from "../chat/permissionOptionLabel";
import { submitPermissionChoice } from "../chat/permissionSubmit";
import type { BackgroundTask } from "./types";

/** Drops the relay's "[subagent <name>] " prefix from a forwarded prompt title. */
export function stripSubagentTitlePrefix(title: string | undefined): string {
  const raw = (title ?? "").trim();
  const close = raw.startsWith("[subagent ") ? raw.indexOf("]") : -1;
  return close >= 0 ? raw.slice(close + 1).trim() : raw;
}

/**
 * The permission prompt of a detached subagent, shown on the task that is
 * blocked on it.
 *
 * The parent turn that spawned this run has ended, so there is no chat stream
 * to put the prompt in - and the transcript it would land in did not ask the
 * question. The answer is posted against the child session, which is the one
 * waiting, through the same endpoint every other prompt uses.
 */
export function SubagentPermissionCard(props: {
  task: BackgroundTask;
  /** Refresh the task list so the answered prompt leaves the row. */
  onAnswered: () => void;
}) {
  const { t } = useT();
  const [submitting, setSubmitting] = useState(false);
  const pending = props.task.pending_permission;
  const { onAnswered } = props;

  // No transcript tool call to draw arguments from: the child's call lives in
  // the child's own transcript, so the prompt body is all there is. The relay
  // prefixes the title with "[subagent <name>] ", which defeats the tool-name
  // parse and would leave the preview header blank - and this card already
  // says whose prompt it is.
  const preview = useMemo(
    () =>
      pending
        ? buildPermissionToolPreview({
            ...pending,
            toolCall: {
              ...pending.toolCall,
              title: stripSubagentTitlePrefix(pending.toolCall?.title),
            },
          })
        : null,
    [pending, t],
  );

  const answer = useCallback(
    async (optionId: string) => {
      if (!pending) {
        return;
      }
      setSubmitting(true);
      try {
        await submitPermissionChoice(
          pending.sessionId,
          pending.toolCall.toolCallId,
          optionId,
        );
      } catch {
        // The run either got the answer or stays blocked until it times out;
        // either way the refreshed list tells the truth.
      } finally {
        setSubmitting(false);
        onAnswered();
      }
    },
    [pending, onAnswered],
  );

  if (!pending || !preview) {
    return null;
  }
  const agentName = (pending.agent_name || props.task.agent?.name || "").trim();

  return (
    <div
      className="bgtask-permission"
      role="group"
      aria-label={
        agentName
          ? t("tasks.permission.headNamed", { name: agentName })
          : t("tasks.permission.head")
      }
      data-testid={`bgtask-permission-${props.task.id}`}
    >
      <div className="bgtask-permission-head">
        {agentName
          ? t("tasks.permission.headNamed", { name: agentName })
          : t("tasks.permission.head")}
      </div>
      <PermissionToolPreview preview={preview} interactive={false} />
      <div className="bgtask-permission-actions">
        {(pending.options ?? []).map((opt) => (
          <button
            key={opt.optionId}
            type="button"
            className={
              opt.optionId === "reject"
                ? "permission-prompt-btn permission-prompt-btn--reject"
                : "permission-prompt-btn permission-prompt-btn--allow"
            }
            disabled={submitting}
            data-testid={`bgtask-permission-${opt.optionId}-${props.task.id}`}
            onClick={() => void answer(opt.optionId)}
          >
            {permissionOptionLabel(opt)}
          </button>
        ))}
      </div>
    </div>
  );
}
