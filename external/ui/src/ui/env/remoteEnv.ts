// Environment selector: lets the bundled UI talk to a remote coddy serve server instead of
// its own origin. A single global fetch shim rewrites same-origin API requests (/v1/*, /coddy/*,
// /openapi*) to the selected remote base URL and attaches its bearer token, so every existing
// call site becomes environment-aware without changes. The choice is persisted in localStorage.

export type CoddyEnv =
  | { mode: "local" }
  | {
      mode: "remote";
      baseUrl: string;
      token: string;
      name?: string;
      /**
       * The relay this environment was reached through, when it was.
       *
       * Entering a node means pointing the base URL at that node's mount, and
       * from there the relay's own routes are no longer under it - so the swarm
       * map, opened over the node, asks the relay at this address directly.
       */
      swarmRelay?: string;
      /** The node path inside that relay, for showing where we are. */
      swarmNode?: string;
    };

import { rememberRelayHome } from "./pageMemory";

const STORAGE_KEY = "coddy_env";

// The fetch the page had before the shim replaced it, so the local origin stays
// reachable whatever environment is active: the local config's remote list, the
// documentation, a probe of a remote by its full address. Taken when the shim
// installs; until then (and in tests, which install none) window.fetch is it.
let nativeFetch: typeof fetch | null = null;

/** localFetch always hits the page's own origin, bypassing the remote shim. */
export function localFetch(
  input: RequestInfo | URL,
  init?: RequestInit,
): Promise<Response> {
  if (nativeFetch) {
    return nativeFetch(input, init);
  }
  return typeof window !== "undefined"
    ? window.fetch(input, init)
    : fetch(input, init);
}

let cached: CoddyEnv | null = null;
const listeners = new Set<() => void>();

function normalizeBase(url: string): string {
  return url.trim().replace(/\/+$/, "");
}

/** envKey names an environment in browser storage: "local" or "remote:<base URL>". */
function envKey(env: CoddyEnv): string {
  return env.mode === "local" ? "local" : "remote:" + env.baseUrl;
}

/**
 * environmentKey names the server an environment is, whatever token it holds:
 * what the app is started over on when it changes (EnvScope).
 */
export function environmentKey(env: CoddyEnv): string {
  return envKey(env);
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
        swarmRelay?: string;
        swarmNode?: string;
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
        if (typeof parsed.swarmRelay === "string" && parsed.swarmRelay) {
          remote.swarmRelay = normalizeBase(parsed.swarmRelay);
        }
        if (typeof parsed.swarmNode === "string" && parsed.swarmNode) {
          remote.swarmNode = parsed.swarmNode;
        }
        resolved = remote;
      }
    }
  } catch {
    /* fall through to local */
  }
  cached = resolved;
  return resolved;
}

/** storeEnv makes env the active environment without telling anyone yet. */
function storeEnv(env: CoddyEnv): void {
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
}

export function setEnv(env: CoddyEnv): void {
  storeEnv(env);
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
  return envKey(getEnv());
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

// The route each environment was left at, kept in this browser. Switching
// reloads the page on the same address, so without this the route of one
// environment was asked of the next: a local session id of a relay, a relay's
// #/swarm of a plain agent - which is how coming back to Local from a relay
// landed on "not a swarm relay" instead of the conversation it was left in
// (issue #401).
const ROUTES_KEY = "coddy_env_routes";
/** Routes remembered at most; the oldest environment is forgotten first. */
const ROUTES_MAX = 32;

function readRoutes(): Record<string, string> {
  try {
    const m = JSON.parse(localStorage.getItem(ROUTES_KEY) || "{}") as unknown;
    return m && typeof m === "object" ? (m as Record<string, string>) : {};
  } catch {
    return {};
  }
}

/** rememberRoute keeps the route the current environment is being left at. */
function rememberRoute(): void {
  try {
    const hash = window.location.hash || "#/";
    const routes = readRoutes();
    const key = envKey(getEnv());
    delete routes[key];
    routes[key] = hash;
    const keys = Object.keys(routes);
    for (const old of keys.slice(0, Math.max(0, keys.length - ROUTES_MAX))) {
      delete routes[old];
    }
    localStorage.setItem(ROUTES_KEY, JSON.stringify(routes));
  } catch {
    /* ignore persistence errors: the switch still lands on the home screen */
  }
}

/** routeFor is where an environment was left, or its home screen. */
function routeFor(env: CoddyEnv): string {
  const hash = readRoutes()[envKey(env)];
  return typeof hash === "string" && hash.startsWith("#/") ? hash : "#/";
}

// What the modules that keep something of the environment - a copy of its
// settings, its version, its health - run to forget it when the app moves to
// another one without reloading the page.
const switchListeners = new Set<() => void>();

// Counts the switches made in place. EnvScope keys the app by it as well as by
// the environment, so choosing the environment the page is already on starts
// the app over too - the way to read everything again after that remote came
// back - while a token rotated under the same one (setEnv) leaves it alone.
let generation = 0;

/** switchGeneration is how many switches in place this page has made. */
export function switchGeneration(): number {
  return generation;
}

/**
 * onEnvironmentSwitch registers what to forget when the app moves to another
 * environment in place (switchTo). A reload forgets everything by itself.
 */
export function onEnvironmentSwitch(cb: () => void): () => void {
  switchListeners.add(cb);
  return () => {
    switchListeners.delete(cb);
  };
}

/**
 * switchTo leaves the current environment for another: the route it is left
 * at is remembered, and the next one opens where it was left, or at its home.
 * `hash` names a route to open instead (entering a node from the map).
 *
 * Between two remotes it happens in place: the page itself holds nothing that
 * changes with them, so the app starts over on the new one (EnvScope keys it
 * by the environment) and nothing blanks. To or from Local the page reloads,
 * since the sign-in of the page's own origin comes and goes with it.
 */
function switchTo(next: CoddyEnv, hash?: string): void {
  const before = getEnv();
  rememberRoute();
  // Nobody is told before it is decided how: an app started over on the new
  // environment while a reload is on its way writes the address, and a written
  // address cancels the reload.
  storeEnv(next);
  window.location.hash = hash || routeFor(getEnv());
  if (before.mode === "remote" && next.mode === "remote") {
    // What held the old one forgets it first, so the app started over reads
    // the new one from the start.
    generation += 1;
    switchListeners.forEach((cb) => cb());
    listeners.forEach((cb) => cb());
    return;
  }
  window.location.reload();
}

/** connectLocal switches to the local origin and reloads so all state re-fetches locally. */
export function connectLocal(): void {
  switchTo({ mode: "local" });
}

/**
 * connectRemote points the UI at a remote coddy serve and reloads. The token is
 * kept in this browser's per-remote list unless `rememberToken` is false, which
 * is how a token that comes from the server's configuration stays there.
 */
export function connectRemote(
  url: string,
  token: string,
  name?: string,
  opts: { rememberToken?: boolean } = {},
): void {
  const base = normalizeBase(url);
  if (!base) return;
  if (opts.rememberToken !== false) {
    setRemoteToken(base, token);
  }
  switchTo(
    name
      ? { mode: "remote", baseUrl: base, token, name }
      : { mode: "remote", baseUrl: base, token },
  );
}

/**
 * connectSwarmNode drives one node of a swarm as if it were an ordinary remote.
 *
 * A node's mount is a base URL with a path, which is all the rest of the app
 * has ever needed - so from here every existing screen works against that node
 * without knowing a relay is in the middle. The relay is remembered so there is
 * a way back to the swarm afterwards.
 *
 * `hash` optionally lands on a particular session once the reload is done.
 */
export function connectSwarmNode(
  relayUrl: string,
  nodePath: string[],
  token: string,
  hash?: string,
): void {
  const relay = normalizeBase(relayUrl);
  if (!relay || nodePath.length === 0) return;
  const base = relay + nodePath.map((n) => `/swarm/nodes/${n}`).join("");
  // The token is the environment's, not copied into the browser's list: one
  // that came from the configuration would outlive its removal there.
  //
  // Where it lands: the route the caller names (the map, a session a search
  // row picked), else where this node was left, else its home - never the
  // swarm route of the environment being left, which a node answers with
  // "not a swarm".
  switchTo(
    {
      mode: "remote",
      baseUrl: base,
      token,
      name: nodePath[nodePath.length - 1] ?? relay,
      swarmRelay: relay,
      swarmNode: nodePath.join("/"),
    },
    hash,
  );
}

/**
 * connectSwarmRelay opens a relay on the swarm map (issue #401). An empty path
 * is the relay the map is drawn for: the app connects to the relay itself, at
 * its home, with no node marked as where it has been. A path is a relay chained
 * under it: its mount is entered as an environment of its own - not as a node,
 * whose screens a relay does not have - so the app asks it whether it is a
 * relay and opens on its map, and its nodes are entered through it in turn.
 */
export function connectSwarmRelay(
  relayUrl: string,
  relayPath: string[],
  token: string,
  name = "",
): void {
  const relay = normalizeBase(relayUrl);
  if (!relay) return;
  const base = relay + relayPath.map((n) => `/swarm/nodes/${n}`).join("");
  // The token stays the environment's, as for a node (connectSwarmNode).
  // The app starts over on it in place: it is a relay from the first frame.
  rememberRelayHome(base, true);
  switchTo(
    {
      mode: "remote",
      baseUrl: base,
      token,
      name: name || relayPath[relayPath.length - 1] || getEnvName(relay),
    },
    "#/swarm",
  );
}

/**
 * The relay a mount URL hangs off - the part before the first /swarm/nodes/,
 * or "" when the base is not a mount. The map is always drawn by the
 * outermost relay of the chain: entering a node or a chained relay keeps the
 * whole swarm in view and only moves the mark of where the app stands.
 */
export function swarmMountRoot(baseUrl: string): string {
  const i = baseUrl.indexOf("/swarm/nodes/");
  return i < 0 ? "" : baseUrl.slice(0, i);
}

/**
 * The names a mount URL chains, outermost first:
 * "<relay>/swarm/nodes/a/swarm/nodes/b" -> ["a", "b"].
 */
export function swarmMountPath(baseUrl: string): string[] {
  const out: string[] = [];
  for (const part of baseUrl.split("/swarm/nodes/").slice(1)) {
    const head = part.split("/")[0];
    if (head) out.push(head);
  }
  return out;
}

/**
 * The relay that draws the swarm map for an environment: the outermost relay
 * of the mount chain a node or a chained relay is reached through, else the
 * environment's own base.
 */
export function swarmRootRelay(env: {
  swarmRelay?: string;
  baseUrl: string;
}): string {
  const through = env.swarmRelay ?? env.baseUrl;
  return swarmMountRoot(through) || through;
}

/** The name the current environment gives a relay base, for its chip. */
function getEnvName(relay: string): string {
  const env = getEnv();
  if (
    env.mode === "remote" &&
    env.baseUrl === relay &&
    env.name &&
    env.name !== "swarm"
  ) {
    return env.name;
  }
  return relay.replace(/^https?:\/\//, "");
}

export function isApiPath(path: string): boolean {
  return (
    path.startsWith("/v1/") ||
    path.startsWith("/coddy/") ||
    // A relay answers under /swarm/ and mounts each node beneath it, so these
    // have to reach the selected environment like any other API call.
    path.startsWith("/swarm/") ||
    path.startsWith("/openapi")
  );
}

// Listeners for a 401 from this origin's own API. The sign-in state lives in
// ui/auth and subscribes here, rather than this module importing it: the shim
// has to install before anything else runs, and a cycle between the two would
// be a startup order nobody can reason about.
const unauthorizedListeners = new Set<() => void>();

/**
 * onLocalApiUnauthorized reports a local API call refused with 401.
 *
 * The sign-in routes are excluded: a wrong password is answered by the form
 * itself, and treating it as "the session ended" would loop.
 */
export function onLocalApiUnauthorized(cb: () => void): () => void {
  unauthorizedListeners.add(cb);
  return () => {
    unauthorizedListeners.delete(cb);
  };
}

/**
 * notifyLocalApiUnauthorized tells the listeners that a local API call was refused
 * with 401. The fetch shim calls it for the page's own requests; the shared events
 * stream calls it for a refusal another context received on this page's behalf.
 */
export function notifyLocalApiUnauthorized(): void {
  unauthorizedListeners.forEach((cb) => cb());
}

/** isAuthPath reports the sign-in routes, which never signal a lost session. */
export function isAuthPath(path: string): boolean {
  return path.startsWith("/coddy/auth/");
}

/** requestPath extracts the same-origin path of a fetch argument, or null. */
function requestPath(input: RequestInfo | URL): string | null {
  if (typeof input === "string") {
    return input.startsWith("/") ? input : null;
  }
  if (input instanceof URL) {
    return input.origin === window.location.origin
      ? input.pathname + input.search
      : null;
  }
  if (typeof Request !== "undefined" && input instanceof Request) {
    try {
      const u = new URL(input.url, window.location.origin);
      return u.origin === window.location.origin ? u.pathname + u.search : null;
    } catch {
      return null;
    }
  }
  return null;
}

/** installRemoteFetchShim rewrites same-origin API requests to the selected remote. Idempotent. */
export function installRemoteFetchShim(): void {
  if (typeof window === "undefined") return;
  const w = window as Window & { __coddyFetchShimmed?: boolean };
  if (w.__coddyFetchShimmed) return;
  w.__coddyFetchShimmed = true;
  const original = window.fetch.bind(window);
  nativeFetch = original;

  window.fetch = (
    input: RequestInfo | URL,
    init?: RequestInit,
  ): Promise<Response> => {
    const env = getEnv();
    const path = requestPath(input);
    if (env.mode !== "remote") {
      // Local origin: nothing is rewritten, but a refusal is worth noticing.
      // The cookie a signed-in browser carries can stop being valid while the
      // page is open, and this is where the app learns that.
      if (path == null || !isApiPath(path) || isAuthPath(path)) {
        return original(input, init);
      }
      return original(input, init).then((res) => {
        if (res.status === 401) {
          notifyLocalApiUnauthorized();
        }
        return res;
      });
    }

    const request = path == null ? null : remoteApiRequest(path, init);
    if (!request) return original(input, init);
    return original(request.url, request.init);
  };
}

/**
 * remoteApiRequest maps a same-origin API path onto the selected remote: its
 * base URL in front of the path and its token in an Authorization header, never
 * in the URL. Null when nothing is rewritten - the local origin, or a path that
 * is not the API's. The fetch shim sends every API call this way; what the
 * browser loads by itself (an <img> src) has to be fetched through it too.
 */
export function remoteApiRequest(
  path: string,
  init?: RequestInit,
): { url: string; init: RequestInit } | null {
  const env = getEnv();
  if (env.mode !== "remote" || !isApiPath(path)) return null;
  const headers = new Headers(init?.headers ?? undefined);
  if (env.token) headers.set("Authorization", "Bearer " + env.token);
  return { url: env.baseUrl + path, init: { ...init, headers } };
}
