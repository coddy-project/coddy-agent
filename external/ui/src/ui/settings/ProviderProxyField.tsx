import React, { type ChangeEvent } from "react";

import { SwitchField } from "./SwitchField";
import { useT } from "../i18n/I18nProvider";

/** Whether a providers[].proxy value is the keyword kw, in any case. */
export function isProviderProxyKeyword(
  value: string,
  kw: "none" | "inherit",
): boolean {
  return value.trim().toLowerCase() === kw;
}

/**
 * ProviderProxyField edits providers[].proxy, which picks one of three routes
 * for every request of the row: the system proxy (no value, or "inherit"), a
 * direct connection ("none"), or a proxy URL. The switch owns "none"; the URL
 * field owns the rest and is disabled while the switch is on. Turning the
 * switch off brings back the last value the row held that was not "none",
 * however it got there (typed, pasted, reloaded), for as long as the form is
 * open: the settings document has room for one value only, so a saved "none"
 * no longer knows the URL it replaced. The item form mounts afresh for every
 * row it edits, so that memory never crosses from one provider to another.
 */
export function ProviderProxyField(props: {
  value: unknown;
  onChange: (next: unknown) => void;
  /** Label and description of the URL field (the schema's, localized). */
  label: string;
  description?: string | undefined;
}) {
  const { value, onChange, label, description } = props;
  const { t } = useT();
  const stored = typeof value === "string" ? value : "";
  const direct = isProviderProxyKeyword(stored, "none");
  const beforeDirect = React.useRef(direct ? "" : stored);
  React.useEffect(() => {
    if (!direct) {
      beforeDirect.current = stored;
    }
  }, [direct, stored]);
  const urlText =
    direct || isProviderProxyKeyword(stored, "inherit") ? "" : stored;
  return (
    <>
      <SwitchField
        checked={direct}
        onChange={(on) => onChange(on ? "none" : beforeDirect.current)}
        label={t("settings.providerProxy.ignoreSystem")}
        description={t("settings.providerProxy.ignoreSystemDesc")}
        dataTestId="provider-proxy-direct"
      />
      <div className="settings-row">
        <span className="settings-label">{label}</span>
        {description ? (
          <p className="settings-field-desc">{description}</p>
        ) : null}
        <input
          className="settings-input"
          type="text"
          value={urlText}
          disabled={direct}
          placeholder={
            direct
              ? t("settings.providerProxy.placeholderDirect")
              : t("settings.providerProxy.placeholderSystem")
          }
          title={description}
          aria-label={label}
          data-testid="provider-proxy-url"
          onChange={(e: ChangeEvent<HTMLInputElement>) =>
            onChange(e.target.value)
          }
        />
      </div>
    </>
  );
}
