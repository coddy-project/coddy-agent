import { useId } from "react";
import { useT } from "../i18n/I18nProvider";
import { SwitchField } from "./SwitchField";
import { SecretInput } from "./SecretInput";
import { landmarkAnchor } from "./settingsSections";

type Props = {
  value: Record<string, unknown>;
  onChange: (value: Record<string, unknown>) => void;
};
export function AuthenticationSection({ value, onChange }: Props) {
  const { t } = useT();
  const id = useId();
  const login = (
    value.login && typeof value.login === "object" ? value.login : {}
  ) as Record<string, unknown>;
  const update = (key: string, v: unknown) =>
    onChange({ ...value, login: { ...login, [key]: v } });
  return (
    <div className="settings-authentication">
      <SwitchField
        checked={login.enable !== false}
        onChange={(v) => update("enable", v)}
        label={t("settings.auth.enable")}
      />
      <fieldset
        className="settings-fieldset"
        data-settings-anchor={landmarkAnchor("httpserver", "account")}
      >
        <legend>{t("settings.auth.account")}</legend>
        {value.login_source === "env" && (
          <p className="settings-muted">{t("settings.auth.environment")}</p>
        )}
        <div className="settings-row">
          <label htmlFor={`${id}-user`}>{t("settings.auth.login")}</label>
          <input
            id={`${id}-user`}
            className="settings-input"
            value={String(login.user ?? "")}
            onChange={(e) => update("user", e.target.value)}
          />
        </div>
        <div className="settings-row">
          <label htmlFor={`${id}-password`}>
            {t("settings.auth.password")}
          </label>
          <SecretInput
            id={`${id}-password`}
            value={String(login.password ?? "")}
            onChange={(e) => update("password", e.target.value)}
            autoComplete="new-password"
          />
        </div>
        <p className="settings-muted">{t("settings.auth.passwordHint")}</p>
        <div className="settings-row">
          <label htmlFor={`${id}-ttl`}>{t("settings.auth.ttl")}</label>
          <input
            id={`${id}-ttl`}
            className="settings-input"
            type="number"
            min="0"
            value={String(login.session_ttl_hours ?? 0)}
            onChange={(e) =>
              update("session_ttl_hours", Number(e.target.value))
            }
          />
        </div>
      </fieldset>
    </div>
  );
}
