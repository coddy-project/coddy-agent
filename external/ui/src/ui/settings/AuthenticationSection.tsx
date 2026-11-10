import { useId, useRef, useState } from "react";
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
  const [enrolling, setEnrolling] = useState(false);
  const [secret, setSecret] = useState("");
  const [qr, setQr] = useState("");
  const [code, setCode] = useState("");
  const [codes, setCodes] = useState<string[]>([]);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const setupSequence = useRef(0);
  const setup = async (input?: string) => {
    const sequence = ++setupSequence.current;
    setQr("");
    setError("");
    try {
      const response = await fetch("/coddy/auth/totp/setup", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(input === undefined ? {} : { secret: input }),
      });
      if (!response.ok) throw new Error("setup");
      const result = (await response.json()) as {
        secret: string;
        qr_png: string;
      };
      if (sequence !== setupSequence.current) return;
      setSecret(result.secret);
      setQr(result.qr_png);
    } catch {
      if (sequence === setupSequence.current)
        setError(t("settings.auth.setupError"));
    }
  };
  const verify = async () => {
    if (!/^\d{6}$/.test(code) || !secret || !qr || busy) return;
    setBusy(true);
    setError("");
    try {
      const response = await fetch("/coddy/auth/totp/enable", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ secret, code }),
      });
      if (!response.ok) throw new Error("verify");
      const result = (await response.json()) as { codes: string[] };
      setCodes(result.codes);
      setEnrolling(false);
      onChange({
        ...value,
        login: {
          ...login,
          totp_configured: true,
          backup_codes_configured: true,
          backup_codes_count: result.codes.length,
        },
      });
    } catch {
      setError(t("settings.auth.verifyError"));
    } finally {
      setBusy(false);
    }
  };
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
      <fieldset className="settings-fieldset auth-enrollment">
        <legend>{t("settings.auth.authenticator")}</legend>
        <SwitchField
          checked={enrolling || login.totp_configured === true}
          disabled={login.totp_configured === true || busy}
          label={t("settings.auth.requireCode")}
          onChange={(next) => {
            setEnrolling(next);
            if (next) void setup();
            else {
              setupSequence.current++;
              setQr("");
              setSecret("");
            }
          }}
        />
        {enrolling && (
          <div className="auth-enrollment-grid">
            {qr && (
              <img
                className="auth-enrollment-qr"
                src={qr}
                alt={t("settings.auth.qr")}
              />
            )}
            <div className="auth-enrollment-fields">
              <div className="settings-row">
                <label htmlFor={`${id}-secret`}>
                  {t("settings.auth.secret")}
                </label>
                <SecretInput
                  id={`${id}-secret`}
                  value={secret}
                  autoComplete="off"
                  onChange={(event) => {
                    const next = event.target.value;
                    setSecret(next);
                    if (next.trim()) void setup(next);
                    else {
                      setupSequence.current++;
                      setQr("");
                    }
                  }}
                />
              </div>
              <div className="settings-row">
                <label htmlFor={`${id}-code`}>{t("settings.auth.code")}</label>
                <input
                  id={`${id}-code`}
                  className="settings-input"
                  inputMode="numeric"
                  autoComplete="one-time-code"
                  maxLength={6}
                  value={code}
                  onChange={(event) =>
                    setCode(event.target.value.replace(/\D/g, "").slice(0, 6))
                  }
                />
              </div>
              <button
                type="button"
                className="settings-btn settings-btn-primary"
                disabled={!/^\d{6}$/.test(code) || !qr || busy}
                onClick={() => void verify()}
              >
                {t("settings.auth.verify")}
              </button>
            </div>
          </div>
        )}
        {error && (
          <p className="settings-error" role="alert">
            {error}
          </p>
        )}
        {codes.length > 0 && (
          <div className="auth-enrollment-codes">
            {codes.map((item) => (
              <code key={item}>{item}</code>
            ))}
          </div>
        )}
      </fieldset>
    </div>
  );
}
