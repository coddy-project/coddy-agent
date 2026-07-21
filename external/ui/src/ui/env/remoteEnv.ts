// Environment selector: lets the bundled UI talk to a remote coddy http server instead of
// its own origin. A single global fetch shim rewrites same-origin API requests (/v1/*, /coddy/*,
// /openapi*) to the selected remote base URL and attaches its bearer token, so every existing
// call site becomes environment-aware without changes. The choice is persisted in localStorage.

export type CoddyEnv =
  | { mode: "local" }
  | { mode: "remote"; baseUrl: string; token: string; name?: string };

const STORAGE_KEY = "coddy_env";

// Capture the native fetch before the shim replaces it, so we can still reach the local origin
// (e.g. to read the local config's remote list regardless of the active environment).
const nativeFetch: typeof fetch =
  typeof window !== "undefined" ? window.fetch.bind(window) : fetch;

/** localFetch always hits the page's own origin, bypassing the remote shim. */
export function localFetch(
  input: RequestInfo | URL,
  init?: RequestInit,
): Promise<Response> {
  return nativeFetch(input, init);
}

let cached: CoddyEnv | null = null;
const listeners = new Set<() => void>();

function normalizeBase(url: string): string {
  return url.trim().replace(/\/+$/, "");
}

export function getEnv(): CoddyEnv {
  if (cached) return cached;
  let resolved: CoddyEnv = { mode: "local" };
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (raw) {
      const parsed = JSON.parse(raw) as {
        mode?: string;
        baseUrl?: string;
        token?: string;
        name?: string;
      };
      if (
        parsed &&
        parsed.mode === "remote" &&
        typeof parsed.baseUrl === "string" &&
        parsed.baseUrl
      ) {
        const remote: Extract<CoddyEnv, { mode: "remote" }> = {
          mode: "remote",
          baseUrl: normalizeBase(parsed.baseUrl),
          token: typeof parsed.token === "string" ? parsed.token : "",
        };
        if (typeof parsed.name === "string") remote.name = parsed.name;
        resolved = remote;
      }
    }
  } catch {
    /* fall through to local */
  }
  cached = resolved;
  return resolved;
}

export function setEnv(env: CoddyEnv): void {
  cached =
    env.mode === "remote"
      ? { ...env, baseUrl: normalizeBase(env.baseUrl) }
      : env;
  try {
    if (env.mode === "local") localStorage.removeItem(STORAGE_KEY);
    else localStorage.setItem(STORAGE_KEY, JSON.stringify(cached));
  } catch {
    /* ignore persistence errors */
  }
  listeners.forEach((cb) => cb());
}

/** subscribe/snapshot for React useSyncExternalStore. */
export function subscribeEnv(cb: () => void): () => void {
  listeners.add(cb);
  return () => listeners.delete(cb);
}
export function snapshotEnv(): CoddyEnv {
  return getEnv();
}

/** envStorageSuffix is a stable per-environment key for namespacing browser storage (e.g. the
 * workspace folder recents), so each remote remembers its own last paths. */
export function envStorageSuffix(): string {
  const env = getEnv();
  return env.mode === "local" ? "local" : "remote:" + env.baseUrl;
}

// Per-remote bearer tokens, kept in this browser only, so re-selecting a known remote from the
// composer menu is one click instead of re-typing the token every time.
const TOKENS_KEY = "coddy_env_tokens";

export function getRemoteToken(url: string): string {
  try {
    const m = JSON.parse(localStorage.getItem(TOKENS_KEY) || "{}") as Record<
      string,
      unknown
    >;
    const t = m[normalizeBase(url)];
    return typeof t === "string" ? t : "";
  } catch {
    return "";
  }
}

export function setRemoteToken(url: string, token: string): void {
  try {
    const m = JSON.parse(localStorage.getItem(TOKENS_KEY) || "{}") as Record<
      string,
      string
    >;
    m[normalizeBase(url)] = token;
    localStorage.setItem(TOKENS_KEY, JSON.stringify(m));
  } catch {
    /* ignore persistence errors */
  }
}

/** hasRemoteToken reports whether a token was ever saved for this remote (even an empty one). */
export function hasRemoteToken(url: string): boolean {
  try {
    const m = JSON.parse(localStorage.getItem(TOKENS_KEY) || "{}") as Record<
      string,
      unknown
    >;
    return Object.prototype.hasOwnProperty.call(m, normalizeBase(url));
  } catch {
    return false;
  }
}

/** connectLocal switches to the local origin and reloads so all state re-fetches locally. */
export function connectLocal(): void {
  setEnv({ mode: "local" });
  window.location.reload();
}

/** connectRemote points the UI at a remote coddy http (persisting its token) and reloads. */
export function connectRemote(url: string, token: string, name?: string): void {
  const base = normalizeBase(url);
  if (!base) return;
  setRemoteToken(base, token);
  setEnv(
    name
      ? { mode: "remote", baseUrl: base, token, name }
      : { mode: "remote", baseUrl: base, token },
  );
  window.location.reload();
}

export function isApiPath(path: string): boolean {
  return (
    path.startsWith("/v1/") ||
    path.startsWith("/coddy/") ||
    path.startsWith("/openapi")
  );
}

/** installRemoteFetchShim rewrites same-origin API requests to the selected remote. Idempotent. */
export function installRemoteFetchShim(): void {
  if (typeof window === "undefined") return;
  const w = window as Window & { __coddyFetchShimmed?: boolean };
  if (w.__coddyFetchShimmed) return;
  w.__coddyFetchShimmed = true;

  window.fetch = (
    input: RequestInfo | URL,
    init?: RequestInit,
  ): Promise<Response> => {
    const env = getEnv();
    if (env.mode !== "remote") return nativeFetch(input, init);

    let path: string | null = null;
    if (typeof input === "string") {
      if (input.startsWith("/")) path = input;
    } else if (input instanceof URL) {
      if (input.origin === window.location.origin)
        path = input.pathname + input.search;
    }
    if (path == null || !isApiPath(path)) return nativeFetch(input, init);

    const headers = new Headers(init?.headers ?? undefined);
    if (env.token) headers.set("Authorization", "Bearer " + env.token);
    return nativeFetch(env.baseUrl + path, { ...init, headers });
  };
}
