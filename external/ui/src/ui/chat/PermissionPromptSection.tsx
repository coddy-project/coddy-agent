import { useCallback, useMemo, useState, startTransition } from "react";

import { PermissionToolPreview } from "./PermissionPromptPreview";
import {
  buildPermissionToolPreview,
  type PermissionToolCallContext,
} from "./permissionToolPreview";
import type {
  CoddyPermissionPayload,
  PermissionResolvedState,
} from "./permissionTypes";
import { questionPromptFocusComposer } from "./QuestionPromptSection";
import { permissionOptionLabel } from "./permissionOptionLabel";
import { submitPermissionChoice } from "./permissionSubmit";
import { useT } from "../i18n/I18nProvider";

/**
 * A choice that switches the whole session (#292) is not one more "allow":
 * bypass wears the danger tone and accept-edits the amber one, so the button
 * that stops every later prompt is never picked for the one in front of it.
 */
export function sessionSwitchClass(optionId: string): string {
  if (optionId === "allow_session_bypass") {
    return "permission-prompt-btn--session-bypass";
  }
  if (optionId === "allow_session_accept_edits") {
    return "permission-prompt-btn--session-edits";
  }
  return "";
}

export type PermissionPromptSectionProps = {
  itemId: string;
  payload: CoddyPermissionPayload;
  toolCall?: PermissionToolCallContext | undefined;
  resolved?: PermissionResolvedState | undefined;
  onResolved: (resolution: PermissionResolvedState) => void;
};

/** Inline permission gate for streaming permission SSE + POST /coddy/sessions/{id}/permission. */
export function PermissionPromptSection(props: PermissionPromptSectionProps) {
  const { t } = useT();
  const { payload, resolved, onResolved, toolCall } = props;
  const [submitting, setSubmitting] = useState(false);
  const preview = useMemo(
    () => buildPermissionToolPreview(payload, toolCall),
    [payload, toolCall, t],
  );

  const choose = useCallback(
    async (optionId: string, label: string) => {
      setSubmitting(true);
      try {
        try {
          await submitPermissionChoice(
            payload.sessionId,
            payload.toolCall.toolCallId,
            optionId,
          );
        } catch {
          // still unblock transcript on transient network errors
        }
        startTransition(() => {
          onResolved({ optionId, summaryLine: label });
        });
      } finally {
        setSubmitting(false);
      }
      questionPromptFocusComposer();
    },
    [onResolved, payload],
  );

  if (resolved) {
    return null;
  }

  return (
    <div
      className="permission-prompt-frame"
      data-testid="permission-prompt-card"
    >
      <div className="permission-prompt-card">
        <div className="permission-prompt-head">
          <span
            className="permission-prompt-icon permission-prompt-icon--question"
            aria-hidden
          >
            ?
          </span>
          <span className="permission-prompt-title">{preview.title}</span>
          <code className="permission-prompt-tool-badge">
            {preview.toolName}
          </code>
        </div>
        <PermissionToolPreview preview={preview} />
        <div className="permission-prompt-actions">
          {payload.options.map((opt) => {
            const isReject = opt.optionId === "reject";
            return (
              <button
                key={opt.optionId}
                type="button"
                className={[
                  "permission-prompt-btn",
                  isReject
                    ? "permission-prompt-btn--reject"
                    : "permission-prompt-btn--allow",
                  sessionSwitchClass(opt.optionId),
                ]
                  .filter(Boolean)
                  .join(" ")}
                disabled={submitting}
                onClick={() =>
                  void choose(opt.optionId, permissionOptionLabel(opt))
                }
              >
                {permissionOptionLabel(opt)}
              </button>
            );
          })}
        </div>
      </div>
    </div>
  );
}
