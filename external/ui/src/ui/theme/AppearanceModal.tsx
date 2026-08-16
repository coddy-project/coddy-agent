import { useCallback, useState, useSyncExternalStore } from "react";
import { useT } from "../i18n/I18nProvider";
import { themeLabel, setLocale } from "../i18n/i18n";
import {
  readUiLocaleCookie,
  writeUiLocaleCookie,
  clearUiLocaleCookie,
  mapSystemLocaleToSupported,
  readNavigatorLanguage,
  type UiLocale,
} from "../i18n/localeCookie";
import { isUiLocale, UI_LOCALES, UI_LOCALE_IDS } from "../i18n/locales";
import { UI_THEME_IDS, LIGHT_THEMES, type UiThemeMode } from "./themeCookie";
import { readAppliedUiTheme, setUiTheme } from "./uiTheme";

function subscribeTheme(onStoreChange: () => void): () => void {
  const obs = new MutationObserver(onStoreChange);
  obs.observe(document.documentElement, {
    attributes: true,
    attributeFilter: ["data-theme"],
  });
  return () => obs.disconnect();
}

/** Accent colours shown in each theme's swatch — approximates the CSS --accent + canvas background. */
const SWATCH_COLORS: Record<
  UiThemeMode,
  { bg: string; accent: string; text: string }
> = {
  dark: { bg: "#121212", accent: "#9333ea", text: "#ffffff" },
  light: { bg: "#f8f8fa", accent: "#7c3aed", text: "#18181b" },
  midnight: { bg: "#0d1117", accent: "#5865f2", text: "#e6edf3" },
  "solarized-dark": { bg: "#002b36", accent: "#268bd2", text: "#839496" },
  monokai: { bg: "#272822", accent: "#fd971f", text: "#f8f8f2" },
  nord: { bg: "#2e3440", accent: "#88c0d0", text: "#eceff4" },
  "rose-pine": { bg: "#191724", accent: "#c4a7e7", text: "#e0def4" },
};

function ThemeSwatch(props: {
  id: UiThemeMode;
  active: boolean;
  onClick: () => void;
}) {
  const { id, active, onClick } = props;
  const label = themeLabel(id);
  const colors = SWATCH_COLORS[id];
  const isLight = LIGHT_THEMES.has(id);

  return (
    <button
      type="button"
      className={`appearance-swatch${active ? " is-active" : ""}`}
      aria-pressed={active}
      aria-label={label}
      data-testid={`theme-swatch-${id}`}
      onClick={onClick}
    >
      <span
        className="appearance-swatch-preview"
        style={{ background: colors.bg }}
        aria-hidden
      >
        <span
          className="appearance-swatch-bar"
          style={{
            background: isLight ? "rgba(0,0,0,0.06)" : "rgba(255,255,255,0.06)",
          }}
        />
        <span
          className="appearance-swatch-dot"
          style={{ background: colors.accent }}
        />
        <span className="appearance-swatch-lines" aria-hidden>
          <span
            style={{ background: colors.text, opacity: 0.45, width: "60%" }}
          />
          <span
            style={{ background: colors.text, opacity: 0.25, width: "40%" }}
          />
          <span
            style={{ background: colors.accent, opacity: 0.55, width: "50%" }}
          />
        </span>
      </span>
      <span
        className="appearance-swatch-label"
        style={{ color: colors.text, background: colors.bg }}
      >
        {label}
      </span>
    </button>
  );
}

type LocaleChoice = "auto" | UiLocale;

function readLocaleChoice(): LocaleChoice {
  const stored = readUiLocaleCookie();
  return stored === null ? "auto" : stored;
}

/** AppearanceLanguagePicker — language select under the theme grid.
 * "Auto" resolves from navigator.language and stores no cookie; registered
 * locale choices persist `coddy_ui_lang`. Purely client-side (no config save). */
export function AppearanceLanguagePicker() {
  const { t } = useT();
  // `choice` (Auto vs explicit locale) is cookie-derived. Local state keeps the
  // controlled select accurate even when Auto resolves to the active locale and
  // the locale store therefore has no change to publish.
  const [choice, setChoice] = useState<LocaleChoice>(() => readLocaleChoice());

  const pick = useCallback((c: string) => {
    if (c === "auto") {
      clearUiLocaleCookie();
      setLocale(mapSystemLocaleToSupported(readNavigatorLanguage()));
      setChoice(c);
      return;
    }

    if (!isUiLocale(c)) {
      return;
    }

    writeUiLocaleCookie(c);
    setLocale(c);
    setChoice(c);
  }, []);

  const options: { id: LocaleChoice; label: string }[] = [
    { id: "auto", label: t("appearance.locale.auto") },
    ...UI_LOCALE_IDS.map((id) => ({
      id,
      label: t(UI_LOCALES[id].labelKey),
    })),
  ];

  return (
    <div
      className="appearance-lang-block"
      data-testid="appearance-language-picker"
    >
      <label
        className="appearance-section-label"
        htmlFor="appearance-language-select"
      >
        {t("appearance.languageLabel")}
      </label>
      <div className="appearance-language-select-wrap">
        <select
          id="appearance-language-select"
          className="appearance-language-select"
          value={choice}
          data-testid="appearance-language-select"
          onChange={(event) => pick(event.currentTarget.value)}
        >
          {options.map((opt) => (
            <option key={opt.id} value={opt.id}>
              {opt.label}
            </option>
          ))}
        </select>
      </div>
    </div>
  );
}

/** AppearanceThemePicker renders just the theme swatch grid (no panel chrome) so it
 * can be embedded as a Settings tab. Theme selection applies immediately and is
 * client-side only (no config save). The language picker sits right under it. */
export function AppearanceThemePicker() {
  const { t } = useT();
  const current = useSyncExternalStore(
    subscribeTheme,
    readAppliedUiTheme,
    () => "dark" as UiThemeMode,
  );

  const pick = useCallback((id: UiThemeMode) => {
    setUiTheme(id);
  }, []);

  return (
    <div
      className="appearance-sheet-body"
      data-testid="appearance-theme-picker"
    >
      <p className="appearance-section-label">{t("appearance.themeLabel")}</p>
      <div
        className="appearance-swatch-grid"
        role="group"
        aria-label={t("appearance.themeGroupAria")}
      >
        {UI_THEME_IDS.map((id) => (
          <ThemeSwatch
            key={id}
            id={id}
            active={current === id}
            onClick={() => pick(id)}
          />
        ))}
      </div>
      <AppearanceLanguagePicker />
    </div>
  );
}
