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
