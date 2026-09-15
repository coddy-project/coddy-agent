import { useCallback, useMemo, useState } from "react";
import { useT } from "../i18n/I18nProvider";
import { PermissionToolPreview } from "./PermissionPromptPreview";
import { buildPermissionToolPreview } from "./permissionToolPreview";
import { permissionOptionLabel } from "./permissionOptionLabel";
import { submitPermissionChoice } from "./permissionSubmit";
import { isAwaitingPermission } from "../tasks/taskStatus";
import type { BackgroundTask } from "../tasks/types";

/** Drops the relay's "[subagent <name>] " prefix from a forwarded prompt title. */
export function stripSubagentTitlePrefix(title: string | undefined): string {
  const raw = (title ?? "").trim();
  const close = raw.startsWith("[subagent ") ? raw.indexOf("]") : -1;
  return close >= 0 ? raw.slice(close + 1).trim() : raw;
}

/**
 * The permission prompts of the background subagents of this chat, at the end
 * of the conversation, oldest question first.
 *
 * A background subagent asks after the turn that spawned it has ended, so no
 * chat stream carries its prompt: it waits on the subagent's task row, and the
 * chat of the parent session - the conversation the person is reading - shows
 * it here, the way Claude Code surfaces a background subagent's prompt in the
 * main session. Denying refuses that one call; the subagent carries on.
 */
export function SubagentPermissionCards(props: {
  tasks: BackgroundTask[];
  /** Re-read the task rows, so an answered prompt leaves the chat. */
  onAnswered: () => void;
}) {
  const waiting = props.tasks
    .filter(isAwaitingPermission)
    .sort((a, b) =>
      (a.pending_permission?.asked_at ?? "").localeCompare(
        b.pending_permission?.asked_at ?? "",
      ),
    );
  if (waiting.length === 0) {
    return null;
  }
  return (
    <div className="subagent-permissions" data-testid="subagent-permissions">
      {waiting.map((task) => (
        <SubagentPermissionCard
          key={task.id}
          task={task}
          onAnswered={props.onAnswered}
        />
      ))}
    </div>
  );
}

function SubagentPermissionCard(props: {
  task: BackgroundTask;
  onAnswered: () => void;
}) {
  const { t } = useT();
  const [submitting, setSubmitting] = useState(false);
  const pending = props.task.pending_permission;
  const { onAnswered } = props;

  // No transcript tool call to draw arguments from: the child's call lives in
  // the child's own transcript, so the prompt body is all there is. The relay
  // prefixes the title with "[subagent <name>] ", which defeats the tool-name
  // parse and would leave the preview header blank - and the card head already
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
        // either way the refreshed task rows tell the truth.
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
  const head = agentName
    ? t("chat.subagentPermission.headNamed", { name: agentName })
    : t("chat.subagentPermission.head");

  return (
    <div
      className="permission-prompt-frame"
      role="group"
      aria-label={head}
      data-testid={`subagent-permission-${props.task.id}`}
    >
      <div className="permission-prompt-card">
        <div className="permission-prompt-head">
          <span
            className="permission-prompt-icon permission-prompt-icon--question"
            aria-hidden
          >
            ?
          </span>
          <span className="permission-prompt-title">{head}</span>
          <code className="permission-prompt-tool-badge">
            {preview.toolName}
          </code>
        </div>
        <PermissionToolPreview preview={preview} />
        <div className="permission-prompt-actions">
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
              data-testid={`subagent-permission-${opt.optionId}-${props.task.id}`}
              onClick={() => void answer(opt.optionId)}
            >
              {permissionOptionLabel(opt)}
            </button>
          ))}
        </div>
      </div>
    </div>
  );
}
