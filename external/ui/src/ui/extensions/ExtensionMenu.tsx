import { useState, useSyncExternalStore } from "react";
import { useExtensions } from "./ExtensionsProvider";
import { useT } from "../i18n/I18nProvider";
import { appNavHrefSettingsSection } from "../scheduler/hashRoute";

export function ExtensionIcon({ className }: { className?: string }) {
  return (
    <svg
      className={className}
      width="18"
      height="18"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.75"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
    >
      <path d="M4 4h6a3 3 0 0 1 6 0h4v6a3 3 0 0 1 0 6v4h-6a3 3 0 0 0-6 0H4v-6a3 3 0 0 0 0-6z" />
    </svg>
  );
}

/** The rail entry remains useful before an extension is installed: it opens
 * the dedicated settings tab. Installed pages and dynamic commands join it. */
export function ExtensionMenu({
  className,
  wide,
}: {
  className: string;
  wide?: boolean | undefined;
}) {
  const context = useExtensions();
  return context ? (
    <LiveMenu context={context} className={className} wide={wide} />
  ) : null;
}
function LiveMenu({
  context,
  className,
  wide,
}: {
  context: NonNullable<ReturnType<typeof useExtensions>>;
  className: string;
  wide: boolean | undefined;
}) {
  const { t } = useT();
  const [error, setError] = useState("");
  const state = useSyncExternalStore(
    context.runtime.subscribe,
    context.runtime.snapshot,
  );
  const items = [
    ...state.pages.map((page) => ({
      key: `page:${page.key}`,
      title: page.title,
      run: () => page.api.ui.openPage(page.id),
    })),
    ...state.menu.map((item) => ({
      key: `menu:${item.key}`,
      title: item.title,
      run: item.onSelect,
    })),
  ];
  if (!items.length)
    return (
      <a
        className={className}
        href={appNavHrefSettingsSection("extensions")}
        aria-label={t("extensions.title")}
        title={t("extensions.title")}
        data-testid="nav-extensions"
      >
        <ExtensionIcon className="rail-svg rail-nav-hit-svg" />
        {wide ? (
          <span className="rail-nav-label">{t("extensions.title")}</span>
        ) : null}
      </a>
    );
  return (
    <div
      className={`${className} extension-menu-host`}
      title={t("extensions.title")}
    >
      <ExtensionIcon className="rail-svg rail-nav-hit-svg" />
      {wide ? (
        <span className="rail-nav-label">{t("extensions.title")}</span>
      ) : null}
      <select
        className="extension-menu-select"
        aria-label={t("extensions.title")}
        data-testid="nav-extensions"
        value=""
        onChange={(e) => {
          const key = e.target.value;
          if (key === "settings") {
            window.location.hash = appNavHrefSettingsSection("extensions");
            return;
          }
          try {
            items.find((item) => item.key === key)?.run();
          } catch (e) {
            setError(String(e));
          }
        }}
      >
        <option value="" disabled>
          {t("extensions.title")}
        </option>
        {items.map((item) => (
          <option key={item.key} value={item.key}>
            {item.title}
          </option>
        ))}
        <option value="settings">{t("extensions.manage")}</option>
      </select>
      {error ? <span role="alert">{error}</span> : null}
    </div>
  );
}
