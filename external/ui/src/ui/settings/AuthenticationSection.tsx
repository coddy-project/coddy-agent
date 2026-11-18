import {
  useEffect,
  useId,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import {
  acknowledgeEnrollment,
  discardEnrollment,
  reconcileEnrollment,
  changeEnrollment,
  enrollmentSnapshot,
  subscribeEnrollment,
  verifyEnrollment,
} from "./authEnrollmentStore";
import { useConfirm } from "../components/useConfirm";
import { environmentKey, getEnv } from "../env/remoteEnv";
import { copyText } from "../clipboard";
import { useT } from "../i18n/I18nProvider";
import { motionIsReduced, subscribeUiSimple } from "../theme/uiSimple";
import { SwitchField } from "./SwitchField";
import { FieldLabel, LegendWithHint } from "./FieldHint";
import { SecretInput } from "./SecretInput";
import { totpPeriodLeft } from "./totpPeriod";
import { IconCopy, IconDownload, IconSync } from "./icons";
import { fieldAnchor } from "./SchemaForm";
import { landmarkAnchor, SETTINGS_TEXTS } from "./settingsSections";

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
  const { t, tp } = useT();
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
    backupGeneration,
    pending,
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
    startedHere.current = true;
    void verifyEnrollment(secret, code);
  };
  const login = (
    value.login && typeof value.login === "object" ? value.login : {}
  ) as Record<string, unknown>;
  const startedHere = useRef(false);
  const metadataMismatch =
    !pending &&
    !busy &&
    configured !== null &&
    typeof login.totp_configured === "boolean" &&
    typeof login.backup_codes_count === "number" &&
    (login.totp_configured !== configured ||
      (login.backup_codes_configured === false && configured) ||
      login.backup_codes_count !== codes.length ||
      String(login.backup_codes_generation ?? "") !== backupGeneration);
  const pendingMountMismatch =
    pending &&
    !startedHere.current &&
    typeof login.backup_codes_generation === "string" &&
    login.backup_codes_generation !== backupGeneration;
  const shownCodes = metadataMismatch || pendingMountMismatch ? [] : codes;
  const latest = useRef({ value, onChange });
  latest.current = { value, onChange };
  useEffect(() => {
    if (configured === null) return;
    const freshGeneration = (
      latest.current.value.login as Record<string, unknown> | undefined
    )?.backup_codes_generation;
    if (
      !startedHere.current &&
      pending &&
      typeof freshGeneration === "string" &&
      freshGeneration !== backupGeneration
    ) {
      discardEnrollment();
      return;
    }
    if (!pending) {
      const account = (latest.current.value.login ?? {}) as Record<
        string,
        unknown
      >;
      if (
        typeof account.totp_configured === "boolean" &&
        typeof account.backup_codes_count === "number"
      ) {
        reconcileEnrollment(
          account.totp_configured && account.backup_codes_configured !== false,
          account.backup_codes_count,
          String(account.backup_codes_generation ?? ""),
        );
      }
      return;
    }
    setEnrolling(false);
    if (!configured) {
      setupSequence.current++;
      setSecret("");
      setQr("");
      setCode("");
    }
    const current = latest.current;
    const account = (current.value.login ?? {}) as Record<string, unknown>;
    current.onChange({
      ...current.value,
      login: {
        ...account,
        totp_configured: configured,
        backup_codes_configured: configured,
        backup_codes_count: codes.length,
        backup_codes_generation: backupGeneration,
      },
    });
    acknowledgeEnrollment();
  }, [
    codes,
    configured,
    pending,
    backupGeneration,
    login.totp_configured,
    login.backup_codes_count,
    login.backup_codes_generation,
  ]);
  useEffect(() => {
    if (busy) setError("");
  }, [busy]);
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
    startedHere.current = true;
    await changeEnrollment(operation);
  };
  const download = () => {
    let url: string | undefined;
    try {
      url = URL.createObjectURL(
        new Blob([shownCodes.join("\n") + "\n"], { type: "text/plain" }),
      );
      const link = document.createElement("a");
      link.href = url;
      link.download = "coddy-backup-codes.txt";
      link.click();
    } catch {
      setError(t("settings.auth.downloadError"));
    } finally {
      if (url) URL.revokeObjectURL(url);
    }
  };
  const update = (key: string, v: unknown) =>
    onChange({ ...value, login: { ...login, [key]: v } });
  // The bar and the label are computed from Date.now() at every paint, so
  // they can never drift from the period the authenticator app shows; the
  // timer only asks for a new frame, once a second under reduced motion.
  const reducedMotion = useSyncExternalStore(
    subscribeUiSimple,
    motionIsReduced,
    () => false,
  );
  const [, repaintPeriod] = useState(0);
  useEffect(() => {
    if (!enrolling) return;
    const id = window.setInterval(
      () => repaintPeriod((v) => v + 1),
      reducedMotion ? 1000 : 250,
    );
    return () => window.clearInterval(id);
  }, [enrolling, reducedMotion]);
  const period = totpPeriodLeft(Date.now());
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
        <LegendWithHint
          label={t(SETTINGS_TEXTS.authAccount.label)}
          description={t(SETTINGS_TEXTS.authAccount.desc)}
        />
        <div className="auth-account-fields">
          <div
            className="settings-row"
            data-settings-anchor={fieldAnchor("httpserver.login", "user")}
          >
            <label className="settings-label" htmlFor={`${id}-user`}>
              {t(AUTHENTICATION_FIELD_TEXTS.user!)}
            </label>
            <input
              id={`${id}-user`}
              className="settings-input"
              placeholder={t("settings.auth.userEnv")}
              value={String(login.user ?? "")}
              onChange={(e) => update("user", e.target.value)}
            />
          </div>
          <div
            className="settings-row"
            data-settings-anchor={fieldAnchor("httpserver.login", "password")}
          >
            <FieldLabel
              label={t(AUTHENTICATION_FIELD_TEXTS.password!)}
              description={t("settings.auth.passwordHint")}
            />
            <SecretInput
              id={`${id}-password`}
              aria-label={t(AUTHENTICATION_FIELD_TEXTS.password!)}
              placeholder={
                value.login_configured === true && value.login_source !== "env"
                  ? t("settings.auth.passwordStored")
                  : t("settings.auth.passwordEnv")
              }
              value={String(login.password ?? "")}
              onChange={(e) => update("password", e.target.value)}
              autoComplete="new-password"
            />
          </div>
          <div
            className="settings-row"
            data-settings-anchor={fieldAnchor(
              "httpserver.login",
              "session_ttl_hours",
            )}
          >
            <FieldLabel
              label={
                <label htmlFor={`${id}-ttl`}>
                  {t(AUTHENTICATION_FIELD_TEXTS.session_ttl_hours!)}
                </label>
              }
              hintLabel={t(AUTHENTICATION_FIELD_TEXTS.session_ttl_hours!)}
              description={t("settings.auth.ttlHint")}
            />
            <input
              id={`${id}-ttl`}
              className="settings-input"
              type="number"
              min="0"
              value={String(login.session_ttl_hours ?? 24)}
              onChange={(e) => {
                const hours = Number(e.target.value);
                update("session_ttl_hours", Number.isFinite(hours) ? hours : 0);
              }}
            />
          </div>
        </div>
      </fieldset>
      <fieldset
        className="settings-fieldset auth-enrollment"
        data-settings-anchor={landmarkAnchor("httpserver", "authenticator")}
      >
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
            className="settings-btn auth-edit-secret"
            onClick={() => {
              setEnrolling(true);
              if (!qr) void setup();
            }}
          >
            {t("settings.auth.editSecret")}
          </button>
        )}
        {enrolling && (
          <div className="auth-enrollment-grid">
            <div className="auth-enrollment-qr-column">
              {qr ? (
                <img
                  className="auth-enrollment-qr"
                  src={qr}
                  alt={t("settings.auth.qr")}
                />
              ) : (
                <div
                  className="auth-enrollment-qr auth-enrollment-qr--placeholder"
                  aria-hidden="true"
                />
              )}
              <span className="auth-enrollment-hint">
                {t("settings.auth.scanHint")}
              </span>
            </div>
            <div className="auth-enrollment-fields">
              <div className="settings-row">
                <label className="settings-label" htmlFor={`${id}-secret`}>
                  {t("settings.auth.secret")}
                </label>
                <SecretInput
                  id={`${id}-secret`}
                  value={secret ?? ""}
                  readOnly={login.totp_configured === true}
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
                >
                  <button
                    type="button"
                    className="settings-btn-icon settings-secret-copy"
                    title={t("settings.auth.copySecret")}
                    aria-label={t("settings.auth.copySecret")}
                    disabled={!secret || busy}
                    onClick={() => void copyText(secret)}
                  >
                    <IconCopy />
                  </button>
                  <button
                    type="button"
                    className="settings-btn-icon settings-secret-refresh"
                    title={t("settings.auth.refreshSecret")}
                    aria-label={t("settings.auth.refreshSecret")}
                    disabled={busy}
                    onClick={() => {
                      setCode("");
                      void setup();
                    }}
                  >
                    <IconSync />
                  </button>
                </SecretInput>
              </div>
              <div className="settings-row">
                <div className="auth-code-heading">
                  <label className="settings-label" htmlFor={`${id}-code`}>
                    {t("settings.auth.code")}
                  </label>
                  <div
                    className="auth-period auth-period--inline"
                    data-testid="auth-period"
                    role="timer"
                    aria-live="off"
                    aria-label={tp("settings.auth.period", period.seconds)}
                  >
                    <span className="auth-period-track" aria-hidden="true">
                      <span
                        className="auth-period-fill"
                        style={{ width: `${period.fraction * 100}%` }}
                      />
                    </span>
                    <span className="auth-period-seconds" aria-hidden="true">
                      {t("settings.auth.seconds", { count: period.seconds })}
                    </span>
                  </div>
                </div>
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
              <div className="auth-enrollment-actions">
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
          </div>
        )}
        {enrolling && login.totp_configured === true && (
          <button
            type="button"
            className="settings-btn settings-btn-danger auth-edit-secret"
            disabled={busy}
            onClick={() => void change("disable")}
          >
            {t("settings.auth.removeTotp")}
          </button>
        )}
        {(error || verifyError) && (
          <p className="settings-error" role="alert">
            {(verifyError &&
              t(
                verifyError === "disable"
                  ? "settings.auth.disableError"
                  : verifyError === "regenerate"
                    ? "settings.auth.regenerateError"
                    : "settings.auth.verifyError",
              )) ||
              error}
          </p>
        )}
      </fieldset>
      {login.totp_configured === true && (
        <fieldset
          className="settings-fieldset auth-backup-block"
          data-settings-anchor={landmarkAnchor("httpserver", "backupCodes")}
        >
          <legend>{t("settings.auth.backupCodes")}</legend>
          <div className="auth-backup-count">
            {tp("settings.auth.unused", Number(login.backup_codes_count ?? 0))}
          </div>
          {shownCodes.length > 0 && (
            <>
              <p className="auth-backup-notice">
                {t("settings.auth.backupNotice")}
              </p>
              <div className="auth-enrollment-codes">
                {shownCodes.map((item) => (
                  <code key={item}>{item}</code>
                ))}
              </div>
            </>
          )}
          <div className="auth-backup-actions">
            {shownCodes.length > 0 && (
              <>
                <button
                  type="button"
                  className="settings-btn auth-backup-button"
                  title={t("settings.auth.copyCodes")}
                  aria-label={t("settings.auth.copyCodes")}
                  onClick={() =>
                    void copyText(shownCodes.join("\n")).then((ok) => {
                      if (!ok) setError(t("settings.auth.copyError"));
                    })
                  }
                >
                  <IconCopy /> {t("settings.auth.copyAction")}
                </button>
                <button
                  type="button"
                  className="settings-btn auth-backup-button"
                  title={t("settings.auth.downloadCodes")}
                  aria-label={t("settings.auth.downloadCodes")}
                  onClick={download}
                >
                  <IconDownload /> {t("settings.auth.downloadAction")}
                </button>
              </>
            )}
            <button
              type="button"
              className="settings-btn auth-backup-button"
              title={t("settings.auth.regenerateCodes")}
              aria-label={t("settings.auth.regenerateCodes")}
              disabled={busy}
              onClick={() => void change("regenerate")}
            >
              <IconSync /> {t("settings.auth.regenerateAction")}
            </button>
          </div>
        </fieldset>
      )}
    </div>
  );
}
