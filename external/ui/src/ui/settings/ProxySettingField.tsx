import React, { type ChangeEvent } from "react";

import { FieldHint } from "./FieldHint";
import { SwitchField } from "./SwitchField";
import { useT } from "../i18n/I18nProvider";

/** Whether a proxy setting is the keyword kw, in any case. */
export function isProxyKeyword(
  value: string,
  kw: "none" | "inherit",
): boolean {
  return value.trim().toLowerCase() === kw;
}

/**
 * ProxySettingField edits a proxy setting - providers[].proxy, or
 * gateways.telegram.proxy, which reads the same way - that picks one of three
 * routes for every request it covers: the system proxy (no value, or
 * "inherit"), a direct connection ("none"), or a proxy URL. The switch owns "none"; the URL
 * field owns the rest and is disabled while the switch is on. Turning the
 * switch off brings back the last value the row held that was not "none",
 * however it got there (typed, pasted, reloaded), for as long as the form is
 * open: the settings document has room for one value only, so a saved "none"
 * no longer knows the URL it replaced. The item form mounts afresh for every
 * row it edits, so that memory never crosses from one provider to another.
 */
export function ProxySettingField(props: {
  value: unknown;
  onChange: (next: unknown) => void;
  /** Label and description of the URL field (the schema's, localized). */
  label: string;
  description?: string | undefined;
  /** Dictionary key of the switch description, naming whose requests go direct. */
  switchDescriptionKey?: string | undefined;
}) {
  const { value, onChange, label, description } = props;
  const switchDescriptionKey =
    props.switchDescriptionKey ?? "settings.providerProxy.ignoreSystemDesc";
  const { t } = useT();
  const stored = typeof value === "string" ? value : "";
  const direct = isProxyKeyword(stored, "none");
  const beforeDirect = React.useRef(direct ? "" : stored);
  React.useEffect(() => {
    if (!direct) {
      beforeDirect.current = stored;
    }
  }, [direct, stored]);
  const urlText =
    direct || isProxyKeyword(stored, "inherit") ? "" : stored;
  return (
    <>
      <SwitchField
        checked={direct}
        onChange={(on) => onChange(on ? "none" : beforeDirect.current)}
        label={t("settings.providerProxy.ignoreSystem")}
        description={t(switchDescriptionKey)}
        dataTestId="proxy-setting-direct"
      />
      <div className="settings-row">
        <span className="settings-label">
          {label}
          {description ? <FieldHint text={description} /> : null}
        </span>
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
          aria-label={label}
          data-testid="proxy-setting-url"
          onChange={(e: ChangeEvent<HTMLInputElement>) =>
            onChange(e.target.value)
          }
        />
      </div>
    </>
  );
}
