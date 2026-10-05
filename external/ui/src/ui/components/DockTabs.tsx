import { useT } from "../i18n/I18nProvider";
import type { RightDockTab } from "./useRightDock";

/**
 * Tasks, Changes and Files share the shell's one right dock. `tab` names the
 * active face; `onTab` asks the shell to change its route and content.
 */
export function DockTabs(props: {
  tab: RightDockTab;
  onTab: (tab: RightDockTab) => void;
}) {
  const { t } = useT();
  const tabs = [
    { id: "tasks" as const, label: t("tasks.panelTitle") },
    { id: "changes" as const, label: t("changes.panelTitle") },
    { id: "files" as const, label: t("files.title") },
  ];
  return (
    <span
      className="dock-tabs"
      role="tablist"
      aria-label={t("changes.dockTabs")}
    >
      {tabs.map((tab) => (
        <button
          key={tab.id}
          type="button"
          role="tab"
          aria-selected={props.tab === tab.id}
          className={"dock-tab" + (props.tab === tab.id ? " is-active" : "")}
          data-testid={`dock-tab-${tab.id}`}
          onClick={() => props.onTab(tab.id)}
        >
          {tab.label}
        </button>
      ))}
    </span>
  );
}
