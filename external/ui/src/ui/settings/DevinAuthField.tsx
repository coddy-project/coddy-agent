import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { useT } from "../i18n/I18nProvider";
import { responseError } from "../settings/apiError";
import { FieldLabel } from "../settings/FieldHint";
import type { OverrideTexts } from "../settings/settingsSections";

type AuthStatus = {
  connected: boolean;
  source?: string;
  masked?: string;
  email?: string;
};
type DeviceLogin = {
  login_id?: string;
  verification_url?: string;
  status?: string;
  connected?: boolean;
  error?: string;
};

export function DevinAuthField(props: {
  providerName: string;
  proxy?: string;
  hasExplicitKey: boolean;
  texts: OverrideTexts;
  onConnectedChange?: (connected: boolean) => void;
}) {
  const { t } = useT();
  const name = props.providerName.trim();
  const endpoint = `/coddy/providers/${encodeURIComponent(name)}/devin-auth`;
  const [status, setStatus] = useState<AuthStatus>({ connected: false });
  const [login, setLogin] = useState<DeviceLogin | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [paste, setPaste] = useState("");
  const generation = useRef(0);
  const onConnectedChange = props.onConnectedChange;

  useLayoutEffect(() => {
    const managedLogin =
      status.connected &&
      (status.source === "coddy" || status.source === "devin_cli");
    onConnectedChange?.(managedLogin);
  }, [status.connected, status.source, onConnectedChange]);

  useEffect(() => {
    const mine = ++generation.current;
    setStatus({ connected: false });
    setLogin(null);
    setLoading(false);
    setError("");
    const controller = new AbortController();
    if (name) {
      void fetch(endpoint, { signal: controller.signal })
        .then(async (response) => {
          if (!response.ok) throw new Error(await responseError(response));
          return (await response.json()) as AuthStatus;
        })
        .then((next) => {
          if (!controller.signal.aborted && mine === generation.current)
            setStatus(next);
        })
        .catch((err: unknown) => {
          if (!controller.signal.aborted && mine === generation.current)
            setError(err instanceof Error ? err.message : String(err));
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
        const response = await fetch(
          `${endpoint}/device/${encodeURIComponent(login.login_id!)}`,
          { signal: controller.signal },
        );
        if (!response.ok) throw new Error(await responseError(response));
        const next = (await response.json()) as DeviceLogin;
        if (controller.signal.aborted || mine !== generation.current) return;
        if (next.status === "completed") {
          const account = await fetch(endpoint, { signal: controller.signal })
            .then(async (current) =>
              current.ok
                ? ((await current.json()) as AuthStatus)
                : ({ connected: true, source: "coddy" } as AuthStatus),
            )
            .catch((): AuthStatus => ({ connected: true, source: "coddy" }));
          if (controller.signal.aborted || mine !== generation.current) return;
          setStatus(account);
          setLogin(null);
          setLoading(false);
          setPaste("");
        } else if (next.status === "failed") {
          setLogin((current) =>
            current ? { ...current, status: "failed" } : current,
          );
          setLoading(false);
          setError(next.error || t("devinAuth.error.signInFailed"));
        } else {
          if (next.error) setError(next.error);
          timer = setTimeout(() => void poll(), 1000);
        }
      } catch (err) {
        if (controller.signal.aborted || mine !== generation.current) return;
        timer = setTimeout(() => void poll(), 2000);
      }
    };
    timer = setTimeout(() => void poll(), 500);
    return () => {
      controller.abort();
      clearTimeout(timer);
    };
  }, [endpoint, login?.login_id, login?.status, t]);

  const signIn = async () => {
    if (!name) return;
    const mine = ++generation.current;
    setLoading(true);
    setError("");
    setLogin(null);
    try {
      const response = await fetch(`${endpoint}/device`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(
          props.proxy === undefined ? {} : { proxy: props.proxy },
        ),
      });
      if (!response.ok) throw new Error(await responseError(response));
      const next = (await response.json()) as DeviceLogin;
      if (mine !== generation.current) return;
      if (!next.login_id || !next.verification_url)
        throw new Error(t("devinAuth.error.incompleteResponse"));
      setLogin(next);
      setLoading(false);
      window.open(next.verification_url, "_blank", "noopener,noreferrer");
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
      const response = await fetch(endpoint, { method: "DELETE" });
      if (!response.ok) throw new Error(await responseError(response));
      const next = (await response.json()) as AuthStatus;
      if (mine === generation.current) setStatus(next);
    } catch (err) {
      if (mine === generation.current)
        setError(err instanceof Error ? err.message : String(err));
    } finally {
      if (mine === generation.current) setLoading(false);
    }
  };

  const submitPaste = async () => {
    if (!login?.login_id || !paste.trim()) return;
    setError("");
    setLoading(true);
    try {
      const response = await fetch(
        `${endpoint}/device/${encodeURIComponent(login.login_id)}/paste`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ url: paste.trim() }),
        },
      );
      if (!response.ok) setError(await responseError(response));
    } catch (err) {
      // A dropped connection must show why the paste did not arrive, not
      // leave the field silent with an unhandled rejection.
      setError(err instanceof Error ? err.message : String(err));
    } finally {
      setLoading(false);
    }
  };

  return (
    <div className="settings-row" data-testid="devin-auth-field">
      <FieldLabel
        label={props.texts.second ?? props.texts.primary}
        description={props.texts.secondDesc ?? props.texts.primaryDesc}
      />
      {status.connected ? (
        <p className="settings-muted codex-auth-status">
          {t("devinAuth.connected", {
            account: status.email || status.masked || "Devin",
          })}
        </p>
      ) : null}
      {status.connected && props.hasExplicitKey ? (
        <p className="settings-field-desc">{t("devinAuth.shadowedByKey")}</p>
      ) : null}
      {login?.verification_url ? (
        <div className="codex-auth-device">
          <a
            className="settings-btn"
            href={login.verification_url}
            target="_blank"
            rel="noreferrer"
          >
            {t("devinAuth.openSignInPage")}
          </a>
          {login.status === "pending" ? (
            <span className="settings-muted">{t("devinAuth.waiting")}</span>
          ) : null}
          <label
            className="provider-picker-field"
            htmlFor="devin-callback-paste"
          >
            <span>{t("devinAuth.pasteLabel")}</span>
            <input
              id="devin-callback-paste"
              className="provider-picker-input"
              value={paste}
              onChange={(event) => setPaste(event.target.value)}
              placeholder="http://127.0.0.1:…/callback?code=…&state=…"
              data-testid="devin-callback-paste"
            />
          </label>
          <button
            type="button"
            className="settings-btn"
            disabled={!paste.trim() || loading}
            onClick={() => void submitPaste()}
            data-testid="devin-paste-submit"
          >
            {t("devinAuth.pasteSubmit")}
          </button>
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
            {t("devinAuth.signOut")}
          </button>
        ) : (
          <button
            type="button"
            className="settings-btn settings-btn-primary"
            data-testid="devin-auth-sign-in"
            disabled={!name || loading}
            onClick={() => void signIn()}
          >
            {loading ? t("devinAuth.waiting") : t("devinAuth.signIn")}
          </button>
        )}
      </div>
      {error ? <p className="settings-error">{error}</p> : null}
    </div>
  );
}
