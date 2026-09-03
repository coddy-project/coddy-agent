import { useT } from "../i18n/I18nProvider";
import { sameTabInAppNavClick } from "../nav/sameTabInAppNav";
import { appNavHrefSession } from "../scheduler/hashRoute";
import type { SubagentTranscriptMeta } from "./subagentTranscript";

/**
 * Stands in for the composer on a subagent transcript. A child session is the
 * record of one delegated run and the server refuses prompts against it
 * (409), so the SPA offers nothing to type into and points back at the parent
 * chat, which is where the operator steers the work.
 */
export function SubagentReadOnlyNotice(props: {
  meta: SubagentTranscriptMeta;
  /** Same-tab open of the parent chat; modifier clicks fall through to the href. */
  onOpenSession?: (sessionId: string) => void;
}) {
  const { t } = useT();
  const name = props.meta.name.trim();
  const parent = props.meta.parentSessionId.trim();
  const onOpen = props.onOpenSession;

  return (
    <div
      className="subagent-readonly-notice"
      role="note"
      data-testid="subagent-readonly-notice"
    >
      <span className="subagent-readonly-text">
        {name
          ? t("chat.subagentReadOnly.notice", { name })
          : t("chat.subagentReadOnly.noticeUnnamed")}
      </span>
      {parent ? (
        <a
          className="subagent-readonly-link"
          href={appNavHrefSession(parent)}
          data-testid="subagent-readonly-parent-link"
          onClick={(ev) => {
            if (!onOpen) {
              return;
            }
            sameTabInAppNavClick(ev, () => onOpen(parent));
          }}
        >
          {t("chat.subagentReadOnly.openParent")}
        </a>
      ) : null}
    </div>
  );
}
