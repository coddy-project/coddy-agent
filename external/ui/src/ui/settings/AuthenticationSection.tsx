import { useId } from "react";
import { useT } from "../i18n/I18nProvider";
import { SwitchField } from "./SwitchField";
import { SecretInput } from "./SecretInput";
import { fieldAnchor } from "./SchemaForm";
import { landmarkAnchor } from "./settingsSections";

export const AUTHENTICATION_FIELD_TEXTS: Record<string, string> = {
  enable: "settings.auth.enable",
  user: "settings.auth.login",
  password: "settings.auth.password",
  session_ttl_hours: "settings.auth.ttl",
};

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
      <div data-settings-anchor={fieldAnchor("httpserver.login", "enable")}>
        <SwitchField
          checked={login.enable !== false}
          onChange={(v) => update("enable", v)}
          label={t(AUTHENTICATION_FIELD_TEXTS.enable!)}
        />
      </div>
      <fieldset
        className="settings-fieldset"
        data-settings-anchor={landmarkAnchor("httpserver", "account")}
      >
        <legend>{t("settings.auth.account")}</legend>
        {value.login_source === "env" && (
          <p className="settings-muted">{t("settings.auth.environment")}</p>
        )}
        <div
          className="settings-row"
          data-settings-anchor={fieldAnchor("httpserver.login", "user")}
        >
          <label htmlFor={`${id}-user`}>
            {t(AUTHENTICATION_FIELD_TEXTS.user!)}
          </label>
          <input
            id={`${id}-user`}
            className="settings-input"
            value={String(login.user ?? "")}
            onChange={(e) => update("user", e.target.value)}
          />
        </div>
        <div
          className="settings-row"
          data-settings-anchor={fieldAnchor("httpserver.login", "password")}
        >
          <label htmlFor={`${id}-password`}>
            {t(AUTHENTICATION_FIELD_TEXTS.password!)}
          </label>
          <SecretInput
            id={`${id}-password`}
            value={String(login.password ?? "")}
            onChange={(e) => update("password", e.target.value)}
            autoComplete="new-password"
          />
        </div>
        <p className="settings-muted">{t("settings.auth.passwordHint")}</p>
        <div
          className="settings-row"
          data-settings-anchor={fieldAnchor(
            "httpserver.login",
            "session_ttl_hours",
          )}
        >
          <label htmlFor={`${id}-ttl`}>
            {t(AUTHENTICATION_FIELD_TEXTS.session_ttl_hours!)}
          </label>
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
