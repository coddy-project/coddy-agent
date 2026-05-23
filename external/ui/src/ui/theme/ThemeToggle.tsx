import { useCallback, useSyncExternalStore } from "react";
import type { UiThemeMode } from "./themeCookie";
import { readAppliedUiTheme, setUiTheme } from "./uiTheme";

function subscribeTheme(onStoreChange: () => void): () => void {
  const obs = new MutationObserver(onStoreChange);
  obs.observe(document.documentElement, {
    attributes: true,
    attributeFilter: ["data-theme"],
  });
  return () => obs.disconnect();
}

export function ThemeToggle() {
  const mode = useSyncExternalStore(
    subscribeTheme,
    readAppliedUiTheme,
    () => "dark" as UiThemeMode,
  );

  const pick = useCallback((next: UiThemeMode) => {
    setUiTheme(next);
  }, []);

  return (
    <div className="settings-theme-block" data-testid="theme-toggle">
      <span className="settings-label" id="settings-theme-label">
        Appearance
      </span>
      <div
        className="settings-theme-segment"
        role="group"
        aria-labelledby="settings-theme-label"
      >
        <button
          type="button"
          className={`settings-theme-option${mode === "dark" ? " is-active" : ""}`}
          data-testid="theme-toggle-dark"
          aria-pressed={mode === "dark"}
          onClick={() => pick("dark")}
        >
          Dark
        </button>
        <button
          type="button"
          className={`settings-theme-option${mode === "light" ? " is-active" : ""}`}
          data-testid="theme-toggle-light"
          aria-pressed={mode === "light"}
          onClick={() => pick("light")}
        >
          Light
        </button>
      </div>
    </div>
  );
}
