import { useEffect, useRef, useState } from "react";
import { FieldLabel } from "./FieldHint";
import type { OverrideTexts } from "./settingsSections";
import { useT } from "../i18n/I18nProvider";
import { translate } from "../i18n/i18n";
import { providerAPIKeyEnvVarName } from "./providerApiKeyPlaceholder";

type AuthStatus = {
  connected: boolean;
  organization?: string;
  masked?: string;
  source?: string;
};
type DeviceLogin = {
  login_id?: string;
  verification_url?: string;
  user_code?: string;
  status?: string;
  error?: string;
};

// An answer the server refused, with its status: a poll answered 404 means
// the attempt is gone, while a poll that never reached the server says
// nothing about the sign-in.
class HTTPError extends Error {
  constructor(
    message: string,
    readonly status: number,
  ) {
    super(message);
  }
}

async function readResponse<T>(response: Response): Promise<T> {
  if (!response.ok) {
    let message = "";
    try {
      const body = (await response.json()) as { error?: unknown };
      if (typeof body.error === "string") message = body.error.trim();
    } catch {
      // A proxy's or a gone server's page is not JSON: the status names it.
    }
    throw new HTTPError(message || `HTTP ${response.status}`, response.status);
  }
  return (await response.json()) as T;
}

export function OpenCodeAuthField(props: {
  providerName: string;
  proxy?: string;
  hasExplicitKey: boolean;
  texts: OverrideTexts;
}) {
  const { t } = useT();
  const name = props.providerName.trim();
  const endpoint = `/coddy/providers/${encodeURIComponent(name)}/opencode-auth`;
  const [status, setStatus] = useState<AuthStatus>({ connected: false });
  const [login, setLogin] = useState<DeviceLogin | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const generation = useRef(0);
  const deviceRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    const mine = ++generation.current;
    setStatus({ connected: false });
    setLogin(null);
    setLoading(false);
    setError("");
    const controller = new AbortController();
    if (name) {
      void fetch(endpoint, { signal: controller.signal })
        .then(readResponse<AuthStatus>)
        .then((next) => {
          if (!controller.signal.aborted && mine === generation.current)
            setStatus(next);
        })
        .catch((err: unknown) => {
          if (!controller.signal.aborted && mine === generation.current)
            setError(String(err instanceof Error ? err.message : err));
        });
    }
    return () => {
      controller.abort();
      generation.current++;
    };
  }, [name, endpoint]);

  useEffect(() => {
    if (!login?.login_id || login.status !== "pending") return;
    const controller = new AbortController();
    const mine = generation.current;
    let timer: ReturnType<typeof setTimeout>;
    const poll = async () => {
      try {
        const next = await fetch(
          `${endpoint}/device/${encodeURIComponent(login.login_id!)}`,
          { signal: controller.signal },
        ).then(readResponse<DeviceLogin>);
        if (controller.signal.aborted || mine !== generation.current) return;
        if (next.status === "completed") {
          // The login is stored; a status read that fails only costs the
          // organization's name.
          const account = await fetch(endpoint, {
            signal: controller.signal,
          })
            .then(readResponse<AuthStatus>)
            .catch((): AuthStatus => ({ connected: true, source: "oauth" }));
          if (controller.signal.aborted || mine !== generation.current) return;
          setStatus(account);
          setLogin(null);
          setLoading(false);
        } else if (next.status === "failed") {
          // The code stays on screen with the reason: a failed attempt is what
          // the operator compares against Console before trying again.
          setLogin((current) =>
            current ? { ...current, status: "failed" } : current,
          );
          setLoading(false);
          setError(next.error || translate("openCodeAuth.error.signInFailed"));
        } else timer = setTimeout(() => void poll(), 1000);
      } catch (err) {
        if (controller.signal.aborted || mine !== generation.current) return;
        if (!(err instanceof HTTPError) || err.status >= 500) {
          // The poll did not reach the server, or the server could not answer
          // it: the attempt may still complete there, so ask again.
          timer = setTimeout(() => void poll(), 2000);
          return;
        }
        setLoading(false);
        setLogin((current) =>
          current ? { ...current, status: "failed" } : current,
        );
        setError(err.message);
      }
    };
    timer = setTimeout(() => void poll(), 500);
    return () => {
      controller.abort();
      clearTimeout(timer);
    };
  }, [endpoint, login?.login_id, login?.status]);

  // The code is what the operator compares with Console, so the form comes to
  // rest on it: it stands where a long provider form would otherwise leave it
  // below the fold.
  useEffect(() => {
    if (login?.user_code)
      deviceRef.current?.scrollIntoView?.({ block: "nearest" });
  }, [login?.user_code]);

  const signIn = async () => {
    if (!name) return;
    const mine = ++generation.current;
    setLoading(true);
    setError("");
    setLogin(null);
    try {
      const next = await fetch(`${endpoint}/device`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        // No gateway: the login belongs to the account and serves the Go
        // and the Console gateway alike, as in the OpenCode client.
        body: JSON.stringify(
          props.proxy === undefined ? {} : { proxy: props.proxy },
        ),
      }).then(readResponse<DeviceLogin>);
      if (mine !== generation.current) return;
      if (!next.login_id || !next.verification_url || !next.user_code)
        throw new Error(translate("openCodeAuth.error.incompleteResponse"));
      // The code stays in this form and the confirmation page opens only on
      // request. Opening it here handed the screen to Console the moment the
      // code arrived, so the code was never on screen to compare with the one
      // Console shows for this device.
      setLogin(next);
      setLoading(false);
    } catch (err) {
      if (mine === generation.current) {
        setLoading(false);
        setError(err instanceof Error ? err.message : String(err));
      }
    }
  };
  const signOut = async () => {
    const mine = ++generation.current;
    setLoading(true);
    setError("");
    setLogin(null);
    try {
      const next = await fetch(endpoint, { method: "DELETE" }).then(
        readResponse<AuthStatus>,
      );
      if (mine === generation.current) setStatus(next);
    } catch (err) {
      if (mine === generation.current)
        setError(err instanceof Error ? err.message : String(err));
    } finally {
      if (mine === generation.current) setLoading(false);
    }
  };
  const shadowed =
    status.connected &&
    (props.hasExplicitKey ||
      ["api_key", "api_key_command", "env"].includes(status.source || ""));

  return (
    <div className="settings-row" data-testid="opencode-auth-field">
      <FieldLabel
        label={props.texts.second}
        description={props.texts.secondDesc}
      />
      {status.connected ? (
        <p className="settings-muted codex-auth-status">
          {t("openCodeAuth.connected", {
            account: status.organization || status.masked || "OpenCode",
          })}
        </p>
      ) : null}
      {shadowed ? (
        <p className="settings-field-desc" data-testid="opencode-auth-shadowed">
          {!props.hasExplicitKey && status.source === "env"
            ? t("openCodeAuth.shadowedByEnv", {
                env: providerAPIKeyEnvVarName(name),
              })
            : t("openCodeAuth.shadowedByKey")}
        </p>
      ) : null}
      {login?.user_code && login.verification_url ? (
        <div className="codex-auth-device" ref={deviceRef}>
          <p className="settings-field-desc">{t("openCodeAuth.enterCode")}</p>
          <code className="codex-auth-code">{login.user_code}</code>
          <a
            className="settings-btn"
            href={login.verification_url}
            target="_blank"
            rel="noreferrer"
          >
            {t("openCodeAuth.openSignInPage")}
          </a>
          {login.status === "pending" ? (
            <span className="settings-muted">{t("openCodeAuth.waiting")}</span>
          ) : null}
        </div>
      ) : null}
      <div className="codex-auth-actions">
        {status.connected || login?.status === "pending" ? (
          <button
            type="button"
            className="settings-btn settings-btn-danger"
            disabled={loading && !login}
            onClick={() => void signOut()}
          >
            {t("openCodeAuth.signOut")}
          </button>
        ) : (
          <button
            type="button"
            className="settings-btn settings-btn-primary"
            data-testid="opencode-auth-sign-in"
            disabled={!name || loading}
            onClick={() => void signIn()}
          >
            {loading ? t("openCodeAuth.waiting") : t("openCodeAuth.signIn")}
          </button>
        )}
      </div>
      {!name ? (
        <p className="settings-field-desc">
          {t("openCodeAuth.enterProviderName")}
        </p>
      ) : null}
      {error ? <p className="settings-error">{error}</p> : null}
    </div>
  );
}
