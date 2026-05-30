import { useCallback, useState, startTransition } from "react";

import { CodeBlockCopyButton } from "../messages/CodeBlockCopyButton";
import {
  permissionPromptDetail,
  permissionPromptTitle,
} from "./permissionPromptDisplay";
import type {
  CoddyPermissionPayload,
  PermissionResolvedState,
} from "./permissionTypes";
import { questionPromptFocusComposer } from "./QuestionPromptSection";

const HDR = "X-Coddy-Session-ID";

export type PermissionPromptSectionProps = {
  itemId: string;
  payload: CoddyPermissionPayload;
  resolved?: PermissionResolvedState | undefined;
  onResolved: (resolution: PermissionResolvedState) => void;
};

/** Inline permission gate for streaming permission SSE + POST /coddy/sessions/{id}/permission. */
export function PermissionPromptSection(props: PermissionPromptSectionProps) {
  const { payload, resolved, onResolved } = props;
  const [submitting, setSubmitting] = useState(false);

  if (resolved) {
    return null;
  }

  const title = permissionPromptTitle(payload);
  const detail = permissionPromptDetail(payload);

  const choose = useCallback(
    async (optionId: string, label: string) => {
      const sid = payload.sessionId.trim();
      const tcid = payload.toolCall.toolCallId.trim();
      setSubmitting(true);
      try {
        try {
          await fetch(
            `/coddy/sessions/${encodeURIComponent(sid)}/permission`,
            {
              method: "POST",
              headers: {
                "Content-Type": "application/json",
                [HDR]: sid,
              },
              body: JSON.stringify({ toolCallId: tcid, optionId }),
            },
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
          <span className="permission-prompt-title">{title}</span>
        </div>
        {detail ? (
          <div className="permission-prompt-quote-wrap">
            <CodeBlockCopyButton
              textToCopy={detail}
              dataTestId="permission-prompt-copy"
            />
            <pre className="permission-prompt-quote-text">{detail}</pre>
          </div>
        ) : null}
        <div className="permission-prompt-actions">
          {payload.options.map((opt) => {
            const isReject = opt.optionId === "reject";
            return (
              <button
                key={opt.optionId}
                type="button"
                className={
                  isReject
                    ? "permission-prompt-btn permission-prompt-btn--reject"
                    : "permission-prompt-btn permission-prompt-btn--allow"
                }
                disabled={submitting}
                onClick={() => void choose(opt.optionId, opt.name)}
              >
                {opt.name}
              </button>
            );
          })}
        </div>
      </div>
    </div>
  );
}
