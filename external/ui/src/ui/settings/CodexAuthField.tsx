import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
} from "react";
import { responseError } from "./apiError";
import { FieldLabel } from "./FieldHint";
import type { OverrideTexts } from "./settingsSections";
import { useT } from "../i18n/I18nProvider";
import { translate } from "../i18n/i18n";

type AuthStatus = {
  connected: boolean;
  source?: string;
  account_id?: string;
  /**
   * The row the Codex CLI login on the server serves, when this row may not
   * use it: that login is one account and stands in for one codex row.
   */
  cli_login_row?: string;
};

type DeviceLogin = {
  login_id?: string;
  verification_url?: string;
  user_code?: string;
  status?: string;
  connected?: boolean;
  error?: string;
};

export function CodexAuthField(props: {
  providerName: string;
  proxy?: string;
  /** The account's name and description (primary). */
  texts: OverrideTexts;
  /**
   * Lets one primary action outside this field drive the sign-in: `action`
   * receives a description of it on every change (its label, whether it can
   * run, and how to run it) and `hideAction` keeps this field from drawing a
   * button of its own. A modal shows one primary action at a time, so the
   * picker renders it once, in its actions row, instead of beside this field
   * and again at the foot.
   */
  action?: (
    state: {
      label: string;
      disabled: boolean;
      /** The sign-in is running: the label is a wait, not an invitation. */
      pending: boolean;
      run: () => void;
    } | null,
  ) => void;
  hideAction?: boolean | undefined;
  /**
   * Onboarding: the account line and what a sign-in in flight needs, nothing
   * else. The advisory notes and the sign-out belong to the settings screen,
   * where a row is tuned; in a first-run dialog they are chrome around the
   * one fact that matters - the account is connected.
   */
  accountOnly?: boolean | undefined;

  onConnectedChange?: (connected: boolean) => void;
}) {
  const providerName = props.providerName.trim();
  const endpoint = `/coddy/providers/${encodeURIComponent(providerName)}/codex-auth`;
  const [status, setStatus] = useState<AuthStatus>({ connected: false });
  const [login, setLogin] = useState<DeviceLogin | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const onConnectedChange = props.onConnectedChange;

  useLayoutEffect(() => {
    onConnectedChange?.(status.connected);
  }, [status.connected, onConnectedChange]);

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
          // The code is spent, and "enter this code" beside "signed in"
          // reads as a contradiction: the success line is all that is left.
          setLogin(null);
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
      // JSON even without a payload: the server refuses any other content
      // type, the ones a cross-site page could send without a preflight.
      const response = await fetch(`${endpoint}/device`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(
          props.proxy === undefined ? {} : { proxy: props.proxy },
        ),
      });
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

  // What the outside primary would do right now. A layout effect so the
  // picker's button and this field never disagree within a paint.
  const publish = props.action;
  // A ref keeps `run` the same function across renders, so the effect below
  // publishes only when what the action says or does actually changes. A
  // fresh object every render would be a new state in the picker every
  // render, which is a loop React stops with "maximum update depth".
  const signInRef = useRef(signIn);
  signInRef.current = signIn;
  const run = useCallback(() => void signInRef.current(), []);
  const actionLabel = loading
    ? t("codexAuth.waitingForChatGpt")
    : t("codexAuth.signInWithChatGpt");
  const actionDisabled = !providerName || loading;
  useLayoutEffect(() => {
    if (!publish) return;
    publish(
      status.connected
        ? null
        : {
            label: actionLabel,
            disabled: actionDisabled,
            pending: loading,
            run,
          },
    );
    return () => publish(null);
  }, [publish, status.connected, actionLabel, actionDisabled, loading, run]);
  const connectedLabel =
    status.source === "codex_cli"
      ? t("codexAuth.connected.viaCli")
      : t("codexAuth.connected.withChatGpt");

  // With the action drawn elsewhere and nothing else to say - no label, no
  // account, no device code, no error - this field renders nothing at all.
  // An empty box still took a row of the form's column and pushed what
  // follows a gap further down.
  if (
    props.hideAction &&
    !status.connected &&
    !login &&
    !error &&
    !props.texts.primary
  ) {
    return null;
  }

  return (
    <div className="settings-row" data-testid="codex-auth-field">
      {props.texts.primary ? (
        <FieldLabel
          label={props.texts.primary}
          description={props.texts.primaryDesc}
        />
      ) : null}
      {status.connected ? (
        <p className="settings-muted codex-auth-status">{connectedLabel}</p>
      ) : null}
      {!props.accountOnly && !status.connected && status.cli_login_row ? (
        <p
          className="settings-field-desc"
          data-testid="codex-auth-cli-other-row"
        >
          {t("codexAuth.cliLoginOtherRow", { row: status.cli_login_row })}
        </p>
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
      {props.accountOnly ? null : (
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
          ) : props.hideAction ? null : (
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
      )}
      {!providerName ? (
        <p className="settings-field-desc">
          {t("codexAuth.enterProviderName")}
        </p>
      ) : null}
      {error ? <p className="settings-error">{error}</p> : null}
    </div>
  );
}
