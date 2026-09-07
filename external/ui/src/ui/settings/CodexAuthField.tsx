import { useEffect, useState } from "react";
import { useT } from "../i18n/I18nProvider";
import { translate } from "../i18n/i18n";

type AuthStatus = {
  connected: boolean;
  source?: string;
  account_id?: string;
};

type DeviceLogin = {
  login_id?: string;
  verification_url?: string;
  user_code?: string;
  status?: string;
  connected?: boolean;
  error?: string;
};

async function responseError(response: Response): Promise<string> {
  try {
    const body = (await response.json()) as { error?: unknown };
    if (typeof body.error === "string" && body.error.trim() !== "") {
      return body.error;
    }
  } catch {
    // Fall through to the HTTP status when the server did not return JSON.
  }
  return `HTTP ${response.status}`;
}

export function CodexAuthField(props: { providerName: string }) {
  const providerName = props.providerName.trim();
  const endpoint = `/coddy/providers/${encodeURIComponent(providerName)}/codex-auth`;
  const [status, setStatus] = useState<AuthStatus>({ connected: false });
  const [login, setLogin] = useState<DeviceLogin | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    setLogin(null);
    setError("");
    if (!providerName) {
      setStatus({ connected: false });
      return;
    }
    const controller = new AbortController();
    void (async () => {
      try {
        const response = await fetch(endpoint, {
          method: "GET",
          signal: controller.signal,
        });
        if (!response.ok) {
          throw new Error(await responseError(response));
        }
        setStatus((await response.json()) as AuthStatus);
      } catch (err) {
        if (!controller.signal.aborted) {
          setError(err instanceof Error ? err.message : String(err));
        }
      }
    })();
    return () => controller.abort();
  }, [endpoint, providerName]);

  useEffect(() => {
    const loginID = login?.login_id;
    if (!loginID || login.status === "completed" || login.status === "failed") {
      return;
    }
    let cancelled = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const poll = async () => {
      try {
        const response = await fetch(
          `${endpoint}/device/${encodeURIComponent(loginID)}`,
          { method: "GET" },
        );
        if (!response.ok) {
          throw new Error(await responseError(response));
        }
        const next = (await response.json()) as DeviceLogin;
        if (cancelled) return;
        setLogin((current) => ({ ...current, ...next }));
        if (next.status === "completed") {
          setStatus({ connected: true, source: "coddy" });
          setLoading(false);
          return;
        }
        if (next.status === "failed") {
          setError(next.error || translate("codexAuth.error.signInFailed"));
          setLoading(false);
          return;
        }
        timer = setTimeout(() => void poll(), 1000);
      } catch (err) {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : String(err));
          setLoading(false);
        }
      }
    };
    timer = setTimeout(() => void poll(), 500);
    return () => {
      cancelled = true;
      if (timer) clearTimeout(timer);
    };
  }, [endpoint, login?.login_id, login?.status]);

  const signIn = async () => {
    if (!providerName) return;
    setLoading(true);
    setError("");
    setLogin(null);
    try {
      const response = await fetch(`${endpoint}/device`, { method: "POST" });
      if (!response.ok) {
        throw new Error(await responseError(response));
      }
      const next = (await response.json()) as DeviceLogin;
      if (!next.login_id || !next.verification_url || !next.user_code) {
        throw new Error(translate("codexAuth.error.incompleteResponse"));
      }
      setLogin(next);
      window.open(next.verification_url, "_blank", "noopener,noreferrer");
    } catch (err) {
      setLoading(false);
      setError(err instanceof Error ? err.message : String(err));
    }
  };

  const signOut = async () => {
    setLoading(true);
    setError("");
    try {
      const response = await fetch(endpoint, { method: "DELETE" });
      if (!response.ok) {
        throw new Error(await responseError(response));
      }
      setStatus((await response.json()) as AuthStatus);
      setLogin(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  };

  const { t } = useT();
  const connectedLabel =
    status.source === "codex_cli"
      ? t("codexAuth.connected.viaCli")
      : t("codexAuth.connected.withChatGpt");

  return (
    <div className="settings-row" data-testid="codex-auth-field">
      <span className="settings-label">{t("codexAuth.fieldLabel")}</span>
      <p className="settings-field-desc">{t("codexAuth.description")}</p>
      {status.connected ? (
        <p className="settings-muted codex-auth-status">{connectedLabel}</p>
      ) : null}
      {login?.user_code && login.verification_url ? (
        <div className="codex-auth-device">
          <p className="settings-field-desc">{t("codexAuth.enterCode")}</p>
          <code className="codex-auth-code">{login.user_code}</code>
          <a
            className="settings-btn"
            href={login.verification_url}
            target="_blank"
            rel="noreferrer"
          >
            {t("codexAuth.openSignInPage")}
          </a>
          {login.status !== "failed" && login.status !== "completed" ? (
            <span className="settings-muted">{t("codexAuth.waiting")}</span>
          ) : null}
        </div>
      ) : null}
      <div className="codex-auth-actions">
        {status.connected && status.source === "coddy" ? (
          <button
            type="button"
            className="settings-btn settings-btn-danger"
            disabled={loading}
            onClick={() => void signOut()}
          >
            {loading ? t("codexAuth.signingOut") : t("codexAuth.signOut")}
          </button>
        ) : (
          <button
            type="button"
            className="settings-btn settings-btn-primary"
            data-testid="codex-auth-sign-in"
            disabled={!providerName || loading}
            onClick={() => void signIn()}
          >
            {loading
              ? t("codexAuth.waitingForChatGpt")
              : t("codexAuth.signInWithChatGpt")}
          </button>
        )}
      </div>
      {!providerName ? (
        <p className="settings-field-desc">
          {t("codexAuth.enterProviderName")}
        </p>
      ) : null}
      {error ? <p className="settings-error">{error}</p> : null}
    </div>
  );
}
