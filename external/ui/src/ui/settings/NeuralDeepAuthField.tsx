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
import { providerAPIKeyEnvVarName } from "./providerApiKeyPlaceholder";

type AuthStatus = {
  connected: boolean;
  masked?: string;
  key_name?: string;
  /** Credential requests actually use: oauth | api_key | api_key_command | env | none. */
  source?: string;
  /** Hub that issued the stored login (recorded in the credential file). */
  hub?: string;
  /** Hub a sign-in for the endpoint queried with ?api_base= would use. */
  endpoint_hub?: string;
};

type DeviceLogin = {
  login_id?: string;
  verification_url?: string;
  user_code?: string;
  status?: string;
  connected?: boolean;
  error?: string;
};

/**
 * NeuralDeepAuthField signs the provider in through the hub's device flow:
 * the browser and the Coddy server may run on different machines, so the
 * server polls the hub while the user confirms the code on the portal. The
 * manual api_key field stays available above; when an explicit key shadows a
 * stored login the widget says so instead of pretending the login is active.
 */
export function NeuralDeepAuthField(props: {
  providerName: string;
  hasExplicitKey: boolean;
  /**
   * Endpoint currently picked in the form (canonical api_base). It may not be
   * saved yet, so it travels with every call: the device start carries it in
   * the body (the endpoint decides which hub mints the key) and the status
   * read asks for it, so a login issued by the other deployment is flagged.
   */
  apiBase: string;
  /** Current unsaved provider proxy; undefined keeps the saved row route. */
  proxy?: string;
  /** The account's name and description (second); primary is the endpoint's. */
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
  const apiBase = props.apiBase.trim();
  const endpoint = `/coddy/providers/${encodeURIComponent(providerName)}/neuraldeep-auth`;
  const statusEndpoint = apiBase
    ? `${endpoint}?api_base=${encodeURIComponent(apiBase)}`
    : endpoint;
  const [status, setStatus] = useState<AuthStatus>({ connected: false });
  const [login, setLogin] = useState<DeviceLogin | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  // A pending login is tied to the provider it was started for, not to the
  // endpoint currently picked: switching the endpoint mid-flow keeps polling
  // (the key is minted by the hub the flow started with, and the status read
  // afterwards flags a mismatch), while a different provider name discards it.
  const lastProviderRef = useRef<string | null>(null);

  useEffect(() => {
    if (lastProviderRef.current !== providerName) {
      lastProviderRef.current = providerName;
      setLogin(null);
      setError("");
    }
    if (!providerName) {
      setStatus({ connected: false });
      return;
    }
    const controller = new AbortController();
    void (async () => {
      try {
        const response = await fetch(statusEndpoint, {
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
  }, [statusEndpoint, providerName]);

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
          // Re-read the stored status: the poll response carries no masked
          // key, and "Signed in ()" with an empty mask reads as a bug.
          try {
            const statusResponse = await fetch(statusEndpoint, {
              method: "GET",
            });
            if (statusResponse.ok) {
              setStatus((await statusResponse.json()) as AuthStatus);
            } else {
              setStatus({ connected: true, source: "oauth" });
            }
          } catch {
            setStatus({ connected: true, source: "oauth" });
          }
          // The code is spent, and "enter this code" beside "signed in"
          // reads as a contradiction: the success line is all that is left.
          setLogin(null);
          setLoading(false);
          return;
        }
        if (next.status === "failed") {
          setError(
            next.error || translate("neuralDeepAuth.error.signInFailed"),
          );
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
  }, [endpoint, statusEndpoint, login?.login_id, login?.status]);

  const signIn = async () => {
    if (!providerName) return;
    setLoading(true);
    setError("");
    setLogin(null);
    try {
      // Always JSON: the server refuses any other content type, the ones a
      // cross-site page could send without a preflight.
      const response = await fetch(`${endpoint}/device`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          ...(apiBase ? { api_base: apiBase } : {}),
          ...(props.proxy === undefined ? {} : { proxy: props.proxy }),
        }),
      });
      if (!response.ok) {
        throw new Error(await responseError(response));
      }
      const next = (await response.json()) as DeviceLogin;
      if (!next.login_id || !next.verification_url || !next.user_code) {
        throw new Error(translate("neuralDeepAuth.error.incompleteResponse"));
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
      const response = await fetch(
        props.proxy === undefined
          ? endpoint
          : `${endpoint}?proxy=${encodeURIComponent(props.proxy)}`,
        { method: "DELETE" },
      );
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
    ? t("neuralDeepAuth.waitingForHub")
    : t("neuralDeepAuth.signIn");
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
  const shadowed =
    status.connected &&
    (props.hasExplicitKey ||
      (status.source && status.source !== "oauth" && status.source !== "none"));
  // The NAME_API_KEY variable of the server wins over the login too, and it
  // follows the row's name: a row called "openai" reads OPENAI_API_KEY. The
  // api_key field is empty then, so the note names the variable instead.
  const shadowEnv =
    !props.hasExplicitKey && status.source === "env"
      ? providerAPIKeyEnvVarName(providerName)
      : "";
  // A key minted by one deployment is not honored by the other; say so while
  // the login is the credential in use, instead of letting requests fail.
  const hubMismatch =
    status.connected &&
    !shadowed &&
    !!status.hub &&
    !!status.endpoint_hub &&
    status.hub.replace(/\/+$/, "").toLowerCase() !==
      status.endpoint_hub.replace(/\/+$/, "").toLowerCase();
  const onConnectedChange = props.onConnectedChange;
  useLayoutEffect(() => {
    onConnectedChange?.(status.connected && !hubMismatch);
  }, [status.connected, hubMismatch, onConnectedChange]);

  // With the action drawn elsewhere and nothing else to say - no label, no
  // account, no device code, no error - this field renders nothing at all.
  // An empty box still took a row of the form's column and pushed what
  // follows a gap further down.
  if (
    props.hideAction &&
    !status.connected &&
    !login &&
    !error &&
    !props.texts.second
  ) {
    return null;
  }

  return (
    <div className="settings-row" data-testid="neuraldeep-auth-field">
      {props.texts.second ? (
        <FieldLabel
          label={props.texts.second}
          description={props.texts.secondDesc}
        />
      ) : null}
      {status.connected ? (
        <p className="settings-muted codex-auth-status">
          {t("neuralDeepAuth.connected", { masked: status.masked || "" })}
        </p>
      ) : null}
      {!props.accountOnly && shadowed ? (
        <p
          className="settings-field-desc"
          data-testid="neuraldeep-auth-shadowed"
        >
          {shadowEnv
            ? t("neuralDeepAuth.shadowedByEnv", { env: shadowEnv })
            : t("neuralDeepAuth.shadowedByKey")}
        </p>
      ) : null}
      {!props.accountOnly && hubMismatch ? (
        <p
          className="settings-field-desc"
          data-testid="neuraldeep-auth-hub-mismatch"
        >
          {t("neuralDeepAuth.hubMismatch", {
            hub: status.hub || "",
            endpoint: apiBase,
          })}
        </p>
      ) : null}
      {/* The code and its page belong to a sign-in still running: a connected
          row says so and nothing else (CodexAuthField). */}
      {!status.connected && login?.user_code && login.verification_url ? (
        <div className="codex-auth-device">
          <p className="settings-field-desc">{t("neuralDeepAuth.enterCode")}</p>
          <code className="codex-auth-code">{login.user_code}</code>
          <a
            className="settings-btn"
            href={login.verification_url}
            target="_blank"
            rel="noreferrer"
          >
            {t("neuralDeepAuth.openSignInPage")}
          </a>
          {login.status !== "failed" && login.status !== "completed" ? (
            <span className="settings-muted">
              {t("neuralDeepAuth.waiting")}
            </span>
          ) : null}
        </div>
      ) : null}
      {props.accountOnly ? null : (
        <div className="codex-auth-actions">
          {status.connected ? (
            <button
              type="button"
              className="settings-btn settings-btn-danger"
              disabled={loading}
              onClick={() => void signOut()}
            >
              {loading
                ? t("neuralDeepAuth.signingOut")
                : t("neuralDeepAuth.signOut")}
            </button>
          ) : props.hideAction ? null : (
            <button
              type="button"
              className="settings-btn settings-btn-primary"
              data-testid="neuraldeep-auth-sign-in"
              disabled={!providerName || loading}
              onClick={() => void signIn()}
            >
              {loading
                ? t("neuralDeepAuth.waitingForHub")
                : t("neuralDeepAuth.signIn")}
            </button>
          )}
        </div>
      )}
      {!providerName ? (
        <p className="settings-field-desc">
          {t("neuralDeepAuth.enterProviderName")}
        </p>
      ) : null}
      {error ? <p className="settings-error">{error}</p> : null}
    </div>
  );
}
