import { useEffect, useState } from "react";
import { useT } from "../i18n/I18nProvider";
import { setSettingsSectionHash } from "../scheduler/hashRoute";
import { fetchSubagentCatalog, trustSubagent } from "../settings/subagentsApi";

type NoticeState =
  | { kind: "checking" }
  | { kind: "idle" }
  | { kind: "needsApproval"; path: string }
  | { kind: "approving"; path: string }
  | { kind: "approved" }
  | { kind: "failed"; path: string; message: string };

/**
 * Shown under a spawn_agent row the runtime refused. Nothing here reads the
 * refusal text, which changes with the wording and the locale: the trigger is
 * structural (a failed call named spawn_agent, the agent name from the call's
 * own JSON arguments), and whether that definition is really awaiting approval
 * for this workspace is asked of the catalog. A spawn that failed for any other
 * reason therefore renders nothing at all.
 *
 * Approving never retries the spawn: the model decides what to do next, and a
 * silent re-run would start work the user only meant to permit.
 */
export function SubagentApprovalNotice(props: {
  agentName: string;
  /** Workspace of this session; the receipt is keyed by it. */
  workspacePath?: string | undefined;
}) {
  const { t } = useT();
  const [state, setState] = useState<NoticeState>({ kind: "checking" });
  const { agentName, workspacePath } = props;

  useEffect(() => {
    let cancelled = false;
    setState({ kind: "checking" });
    void (async () => {
      const res = await fetchSubagentCatalog(workspacePath);
      if (cancelled) {
        return;
      }
      if (!res.ok) {
        // The catalog is unreachable: there is nothing trustworthy to offer.
        setState({ kind: "idle" });
        return;
      }
      const entry = res.data.items.find((e) => e.name === agentName);
      setState(
        entry && entry.needs_approval
          ? { kind: "needsApproval", path: entry.path ?? "" }
          : { kind: "idle" },
      );
    })();
    return () => {
      cancelled = true;
    };
  }, [agentName, workspacePath]);

  if (state.kind === "checking" || state.kind === "idle") {
    return null;
  }

  const onApprove = (path: string) => {
    setState({ kind: "approving", path });
    void (async () => {
      const res = await trustSubagent(agentName, workspacePath);
      setState(
        res.ok
          ? { kind: "approved" }
          : { kind: "failed", path, message: res.error },
      );
    })();
  };

  const path = state.kind === "approved" ? "" : state.path;

  return (
    <div
      className="subagent-approval-notice"
      role="status"
      data-testid={`subagent-approval-${agentName}`}
    >
      <div className="subagent-approval-text">
        {state.kind === "approved" ? (
          t("messages.subagentApproval.approved", { name: agentName })
        ) : (
          <>
            {t("messages.subagentApproval.needed", { name: agentName })}
            {path ? (
              <>
                {" "}
                <code>{path}</code>
              </>
            ) : null}
          </>
        )}
        {state.kind === "failed" ? (
          <span className="subagent-approval-error">
            {t("messages.subagentApproval.failed", { error: state.message })}
          </span>
        ) : null}
      </div>
      {state.kind === "approved" ? null : (
        <div className="subagent-approval-actions">
          <button
            type="button"
            className="settings-btn settings-btn-approve"
            disabled={state.kind === "approving"}
            onClick={() => onApprove(path)}
            data-testid={`subagent-approval-approve-${agentName}`}
          >
            {t("messages.subagentApproval.approve")}
          </button>
          <button
            type="button"
            className="settings-btn"
            onClick={() => setSettingsSectionHash("subagents")}
            data-testid={`subagent-approval-settings-${agentName}`}
          >
            {t("messages.subagentApproval.openSettings")}
          </button>
        </div>
      )}
    </div>
  );
}
