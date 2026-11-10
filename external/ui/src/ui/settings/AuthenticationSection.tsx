import {
  useEffect,
  useId,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import {
  changeEnrollment,
  enrollmentSnapshot,
  subscribeEnrollment,
  verifyEnrollment,
} from "./authEnrollmentStore";
import { useConfirm } from "../components/useConfirm";
import { environmentKey, getEnv } from "../env/remoteEnv";
import { copyText } from "../clipboard";
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
  const confirm = useConfirm();
  const id = useId();
  const [enrolling, setEnrolling] = useState(false);
  const [secret, setSecret] = useState("");
  const [qr, setQr] = useState("");
  const [code, setCode] = useState("");
  const {
    codes,
    busy,
    error: verifyError,
    configured,
  } = useSyncExternalStore(
    subscribeEnrollment,
    enrollmentSnapshot,
    enrollmentSnapshot,
  );
  const [error, setError] = useState("");

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
  const verify = () => {
    if (!/^\d{6}$/.test(code) || !secret || !qr || busy) return;
    setupSequence.current++;
    void verifyEnrollment(secret, code);
  };
  const login = (
    value.login && typeof value.login === "object" ? value.login : {}
  ) as Record<string, unknown>;
  const latest = useRef({ value, onChange });
  latest.current = { value, onChange };
  useEffect(() => {
    if (configured === null) return;
    setEnrolling(false);
    if (!configured) {
      setupSequence.current++;
      setSecret("");
      setQr("");
      setCode("");
    }
    const current = latest.current;
    const account = (current.value.login ?? {}) as Record<string, unknown>;
    if (
      account.totp_configured === configured &&
      account.backup_codes_count === codes.length
    )
      return;
    current.onChange({
      ...current.value,
      login: {
        ...account,
        totp_configured: configured,
        backup_codes_configured: configured,
        backup_codes_count: codes.length,
      },
    });
  }, [codes, configured]);
  const change = async (operation: "disable" | "regenerate") => {
    const env = environmentKey(getEnv());
    const accepted = await confirm({
      title: t(
        operation === "disable"
          ? "settings.auth.disableConfirm"
          : "settings.auth.regenerateConfirm",
      ),
      confirmLabel: t(
        operation === "disable"
          ? "settings.auth.disable"
          : "settings.auth.regenerateConfirmButton",
      ),
      variant: "danger",
    });
    if (!accepted || env !== environmentKey(getEnv())) return;
    await changeEnrollment(operation);
  };
  const download = () => {
    const url = URL.createObjectURL(
      new Blob([codes.join("\n") + "\n"], { type: "text/plain" }),
    );
    const link = document.createElement("a");
    link.href = url;
    link.download = "coddy-backup-codes.txt";
    link.click();
    URL.revokeObjectURL(url);
  };
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
        {login.totp_configured === true && (
          <button
            type="button"
            className="settings-btn"
            disabled={busy}
            onClick={() => void change("disable")}
          >
            {t("settings.auth.removeTotp")}
          </button>
        )}
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
                  disabled={busy}
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
                  disabled={busy}
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
        {(error || verifyError) && (
          <p className="settings-error" role="alert">
            {error ||
              t(
                verifyError === "disable"
                  ? "settings.auth.disableError"
                  : verifyError === "regenerate"
                    ? "settings.auth.regenerateError"
                    : "settings.auth.verifyError",
              )}
          </p>
        )}
        {login.totp_configured === true && (
          <div className="auth-backup-block">
            <div>
              {t("settings.auth.backupCodes")} ·{" "}
              {t("settings.auth.unused", {
                count: String(login.backup_codes_count ?? 0),
              })}
            </div>
            <div className="auth-backup-actions">
              <button
                type="button"
                className="settings-btn"
                disabled={codes.length === 0}
                onClick={() =>
                  void copyText(codes.join("\n")).then((ok) => {
                    if (!ok) setError(t("settings.auth.copyError"));
                  })
                }
              >
                {t("settings.auth.copy")}
              </button>
              <button
                type="button"
                className="settings-btn"
                disabled={codes.length === 0}
                onClick={download}
              >
                {t("settings.auth.download")}
              </button>
              <button
                type="button"
                className="settings-btn"
                disabled={busy}
                onClick={() => void change("regenerate")}
              >
                {t("settings.auth.regenerate")}
              </button>
            </div>
          </div>
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
