import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import { useT } from "../i18n/I18nProvider";
import { responseError } from "./apiError";
import {
  snapshotSettingsConfig,
  subscribeSettingsConfig,
} from "./settingsConfigStore";

type Status = { connected: boolean; source?: string };
type Login = {
  login_id?: string;
  verification_url?: string;
  user_code?: string;
  status?: string;
  connected?: boolean;
  save_required?: boolean;
  error?: string;
  kimi_identity?: string;
  mode?: string;
  region?: string;
};
type Action = {
  label: string;
  disabled: boolean;
  pending: boolean;
  run: () => void;
};

function safeURL(raw: string, region: string): boolean {
  try {
    const u = new URL(raw);
    const pairs = [...u.searchParams.entries()];
    return (
      u.protocol === "https:" &&
      u.host === (region === "china" ? "www.kimi.com" : "www.kimi.ai") &&
      !u.username &&
      !u.password &&
      !u.hash &&
      u.pathname === "/code/authorize_device" &&
      (pairs.length === 0 ||
        (pairs.length === 1 &&
          pairs[0]?.[0] === "user_code" &&
          !!pairs[0]?.[1]))
    );
  } catch {
    return false;
  }
}

export function KimiAuthField(props: {
  providerName: string;
  region: string;
  mode: string;
  proxy?: string;
  identity?: string;
  onIdentity?: (identity: string, expected: string) => boolean;
  action?: (action: Action | null) => void;
  hideAction?: boolean;
  accountOnly?: boolean;
  onConnectedChange?: (connected: boolean) => void;
  onReadyChange?: (ready: boolean) => void;
}) {
  const { t } = useT();
  const savedConfig = useSyncExternalStore(
    subscribeSettingsConfig,
    snapshotSettingsConfig,
    snapshotSettingsConfig,
  );
  const name = props.providerName.trim();
  const region = props.region || "global";
  const endpoint = `/coddy/providers/${encodeURIComponent(name)}/kimi-auth`;
  const identity = props.identity || "";
  const query = `?mode=${encodeURIComponent(props.mode || "subscription")}&region=${encodeURIComponent(region)}&kimi_identity=${encodeURIComponent(identity)}`;
  const savedRows = Array.isArray(savedConfig.config?.providers)
    ? savedConfig.config.providers
    : [];
  const identitySaved =
    !!identity &&
    savedRows.some(
      (row: unknown) =>
        row &&
        typeof row === "object" &&
        (row as Record<string, unknown>).type === "kimi" &&
        (row as Record<string, unknown>).kimi_identity === identity,
    );
  const [status, setStatus] = useState<Status>({ connected: false });
  const [login, setLogin] = useState<Login | null>(null);
  const [loading, setLoading] = useState(false);
  const [saveRequired, setSaveRequired] = useState(false);
  const starting = useRef(false);
  const [startInFlight, setStartInFlight] = useState(false);
  const [error, setError] = useState("");
  const generation = useRef(0);
  const statusVersion = useRef(0);
  const acceptedIdentity = useRef(identity);
  const active = useRef<{ endpoint: string; query: string; id: string } | null>(
    null,
  );
  const mounted = useRef(true);
  useLayoutEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);
  const abandon = useCallback(() => {
    const attempt = active.current;
    active.current = null;
    if (!attempt) return;
    void (async () => {
      try {
        const response = await fetch(
          `${attempt.endpoint}/device/${encodeURIComponent(attempt.id)}${attempt.query}`,
          { method: "DELETE" },
        );
        if (!response.ok) throw new Error(await responseError(response));
      } catch (error) {
        if (mounted.current)
          setError(error instanceof Error ? error.message : String(error));
        else console.warn("Kimi login cancellation failed", error);
      }
    })();
  }, []);
  const cancel = useCallback(async () => {
    const attempt = active.current;
    if (!attempt) return;
    const response = await fetch(
      `${attempt.endpoint}/device/${encodeURIComponent(attempt.id)}${attempt.query}`,
      { method: "DELETE" },
    );
    if (!response.ok) throw new Error(await responseError(response));
    if (active.current === attempt) active.current = null;
  }, []);
  useLayoutEffect(() => {
    if (acceptedIdentity.current === identity) return;
    acceptedIdentity.current = identity;
    generation.current++;
    setStatus({ connected: false });
    setSaveRequired(false);
    setLogin(null);
    setLoading(false);
    abandon();
  }, [identity, abandon]);
  useEffect(() => {
    const gen = ++generation.current;
    setStatus({ connected: false });
    setSaveRequired(false);
    setLogin(null);
    setLoading(false);
    setError("");
    return () => {
      generation.current++;
      abandon();
    };
  }, [endpoint, name, props.mode, region, abandon]);
  useEffect(() => {
    if (!name || props.mode === "platform") return;
    const version = ++statusVersion.current;
    const gen = generation.current;
    void fetch(endpoint + query)
      .then(async (r) => {
        if (!r.ok) throw new Error(await responseError(r));
        return r.json() as Promise<Status>;
      })
      .then((value) => {
        if (
          generation.current === gen &&
          statusVersion.current === version &&
          !active.current &&
          (!saveRequired || identitySaved)
        ) {
          setStatus(value);
          if (identitySaved && saveRequired) {
            setSaveRequired(false);
            if (!value.connected) setError(t("kimiAuth.statusAfterSaveFailed"));
          }
        }
      })
      .catch((error) => {
        if (generation.current === gen && statusVersion.current === version)
          setError(error instanceof Error ? error.message : String(error));
      });
    return () => {
      statusVersion.current++;
    };
  }, [endpoint, query, name, props.mode, identitySaved, saveRequired, t]);
  useEffect(() => {
    props.onConnectedChange?.(status.connected);
  }, [status.connected, props.onConnectedChange]);
  useLayoutEffect(() => {
    props.onReadyChange?.(status.connected || saveRequired);
  }, [status.connected, saveRequired, props.onReadyChange]);
  useEffect(() => {
    if (
      !login?.login_id ||
      !loading ||
      !identity ||
      !active.current?.query.includes(
        `kimi_identity=${encodeURIComponent(identity)}`,
      )
    )
      return;
    const gen = generation.current;
    let timer: ReturnType<typeof setTimeout>;
    const poll = async () => {
      try {
        const r = await fetch(
          `${endpoint}/device/${encodeURIComponent(login.login_id!)}${query}`,
        );
        if (!r.ok) throw new Error(await responseError(r));
        const next = (await r.json()) as Login;
        if (generation.current !== gen) return;
        if (
          next.status === "completed" &&
          next.connected === false &&
          next.save_required === true
        ) {
          statusVersion.current++;
          active.current = null;
          setLogin(null);
          setLoading(false);
          setStatus({ connected: false });
          setSaveRequired(true);
        } else if (
          next.status === "completed" &&
          next.connected === true &&
          next.save_required !== true
        ) {
          active.current = null;
          setLogin(null);
          setLoading(false);
          const version = ++statusVersion.current;
          const result = await fetch(endpoint + query);
          if (!result.ok) throw new Error(await responseError(result));
          const s = (await result.json()) as Status;
          if (generation.current === gen && statusVersion.current === version)
            setStatus(s);
        } else if (next.status === "failed") {
          statusVersion.current++;
          active.current = null;
          setLoading(false);
          setLogin(null);
          setError(next.error || t("kimiAuth.failed"));
        } else if (next.status === "pending") {
          timer = setTimeout(() => void poll(), 1000);
        } else {
          statusVersion.current++;
          active.current = null;
          setLogin(null);
          setLoading(false);
          setError(t("kimiAuth.invalidPoll"));
        }
      } catch (e) {
        if (generation.current === gen) {
          statusVersion.current++;
          setLoading(false);
          setError(e instanceof Error ? e.message : String(e));
        }
      }
    };
    timer = setTimeout(() => void poll(), 500);
    return () => clearTimeout(timer);
  }, [login?.login_id, loading, endpoint, query, t]);
  const signIn = async () => {
    if (starting.current || active.current) return;
    starting.current = true;
    setStartInFlight(true);
    const gen = generation.current;
    statusVersion.current++;
    setError("");
    setLoading(true);
    try {
      const r = await fetch(`${endpoint}/device`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          mode: props.mode || "subscription",
          region,
          kimi_identity: identity,
          ...(props.proxy === undefined ? {} : { proxy: props.proxy }),
        }),
      });
      if (!r.ok) throw new Error(await responseError(r));
      const next = (await r.json()) as Login;
      const valid =
        !!next.login_id &&
        !!next.user_code &&
        !!next.verification_url &&
        safeURL(next.verification_url, region);
      if (!valid || generation.current !== gen) {
        if (next.login_id) {
          try {
            const cancelled = await fetch(
              `${endpoint}/device/${encodeURIComponent(next.login_id)}${query}`,
              { method: "DELETE" },
            );
            if (!cancelled.ok) throw new Error(await responseError(cancelled));
          } catch (error) {
            if (generation.current === gen) throw error;
            if (mounted.current)
              setError(error instanceof Error ? error.message : String(error));
            else console.warn("Kimi login cancellation failed", error);
            return;
          }
        }
        if (!valid) throw new Error(t("kimiAuth.invalidResponse"));
        return;
      }
      const returnedIdentity = next.kimi_identity || "";
      const identityValid =
        /^[0-9a-f]{32}(?:[0-9a-f]{32})?$/.test(returnedIdentity) &&
        next.region === region &&
        next.mode === "subscription" &&
        (!identity || identity === returnedIdentity);
      if (identityValid) acceptedIdentity.current = returnedIdentity;
      if (!identityValid || !props.onIdentity?.(returnedIdentity, identity)) {
        acceptedIdentity.current = identity;
        const cancelled = await fetch(
          `${endpoint}/device/${encodeURIComponent(next.login_id!)}${query}`,
          { method: "DELETE" },
        );
        if (!cancelled.ok) throw new Error(await responseError(cancelled));
        throw new Error(
          t(identityValid ? "kimiAuth.rowChanged" : "kimiAuth.invalidResponse"),
        );
      }
      acceptedIdentity.current = returnedIdentity;
      generation.current++;
      active.current = {
        endpoint,
        query: `?mode=subscription&region=${encodeURIComponent(region)}&kimi_identity=${encodeURIComponent(returnedIdentity)}`,
        id: next.login_id!,
      };
      setLogin(next);
    } catch (e) {
      if (generation.current === gen) {
        setLoading(false);
        setError(e instanceof Error ? e.message : String(e));
      }
    } finally {
      starting.current = false;
      if (mounted.current) setStartInFlight(false);
    }
  };
  const signOut = async () => {
    const gen = generation.current;
    setLoading(true);
    setError("");
    try {
      statusVersion.current++;
      const r = await fetch(endpoint + query, { method: "DELETE" });
      if (!r.ok) throw new Error(await responseError(r));
      const s = (await r.json()) as Status;
      if (generation.current === gen) setStatus(s);
    } catch (e) {
      if (generation.current === gen)
        setError(e instanceof Error ? e.message : String(e));
    } finally {
      if (generation.current === gen) setLoading(false);
    }
  };
  const ref = useRef(signIn);
  ref.current = signIn;
  const run = useCallback(() => void ref.current(), []);
  const publish = props.action;
  const hasActive = active.current !== null;
  useLayoutEffect(() => {
    if (!publish) return;
    publish(
      status.connected || saveRequired
        ? null
        : {
            label: t("kimiAuth.signIn"),
            disabled: !name || loading || hasActive || startInFlight,
            pending: loading,
            run,
          },
    );
    return () => publish(null);
  }, [
    publish,
    status.connected,
    saveRequired,
    name,
    loading,
    hasActive,
    startInFlight,
    run,
    t,
  ]);
  return (
    <div className="settings-row" data-testid="kimi-auth-field">
      {saveRequired ? (
        <p className="settings-muted" data-testid="kimi-auth-save-required">
          {t("kimiAuth.saveRequired")}
        </p>
      ) : null}
      {status.connected && !saveRequired ? (
        <p className="settings-muted">
          {status.source === "cli"
            ? t("kimiAuth.cli")
            : t("kimiAuth.connected")}
        </p>
      ) : null}
      {status.connected &&
      status.source === "managed" &&
      identity &&
      !identitySaved ? (
        <p className="settings-muted">{t("kimiAuth.unsaved")}</p>
      ) : null}
      {login?.user_code && login.verification_url ? (
        <div className="codex-auth-device">
          <code className="codex-auth-code">{login.user_code}</code>
          <a href={login.verification_url} target="_blank" rel="noreferrer">
            {t("kimiAuth.open")}
          </a>
          <button
            type="button"
            onClick={() => {
              generation.current++;
              void cancel()
                .then(() => {
                  setLogin(null);
                  setLoading(false);
                })
                .catch((e) => {
                  setLoading(false);
                  setError(e instanceof Error ? e.message : String(e));
                });
            }}
          >
            {t("kimiAuth.cancel")}
          </button>
        </div>
      ) : null}
      {!props.accountOnly &&
      status.connected &&
      (status.source === "managed" || status.source === "cli") ? (
        <button
          type="button"
          disabled={loading || status.source === "cli"}
          title={
            status.source === "cli" ? t("kimiAuth.cliSignOutHint") : undefined
          }
          onClick={() => void signOut()}
        >
          {t("kimiAuth.signOut")}
        </button>
      ) : null}
      {!status.connected && !saveRequired && !props.hideAction ? (
        <button
          type="button"
          disabled={!name || loading || hasActive || startInFlight}
          onClick={run}
        >
          {t("kimiAuth.signIn")}
        </button>
      ) : null}
      {error ? <p className="settings-error">{error}</p> : null}
    </div>
  );
}
