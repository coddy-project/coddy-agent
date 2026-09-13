// The browser half of the optional sign-in: what the server says about this
// page's credentials, and the three calls that change it.
//
// It applies to the local origin only. A remote environment is reached
// cross-origin with a bearer token the user pasted into the environment
// selector, and cookies do not travel there; AuthGate skips this module in that
// mode entirely.

import { getEnv, localFetch, onLocalApiUnauthorized } from "../env/remoteEnv";

export type AuthState = {
  /** A password form is configured and this browser has not passed it. */
  loginRequired: boolean;
  /** Some credential gates the API, including a token this page cannot supply. */
  authRequired: boolean;
  /** This browser currently holds a valid session (or the server wants none). */
  authenticated: boolean;
  /** The signed-in account, when there is one. */
  user: string;
  /**
   * Whether the server has been asked yet. The app waits for it rather than
   * rendering and then yanking itself away to a sign-in screen.
   */
  loaded: boolean;
};

const initial: AuthState = {
  loginRequired: false,
  authRequired: false,
  authenticated: false,
  user: "",
  loaded: false,
};

let current: AuthState = initial;
const listeners = new Set<() => void>();

export function subscribeAuth(cb: () => void): () => void {
  listeners.add(cb);
  return () => {
    listeners.delete(cb);
  };
}

export function snapshotAuth(): AuthState {
  return current;
}

/** setAuthState replaces the snapshot and wakes the subscribers. */
export function setAuthState(next: AuthState): void {
  current = next;
  listeners.forEach((cb) => cb());
}

/** resetAuthStateForTests puts the module back to its initial snapshot. */
export function resetAuthStateForTests(): void {
  inflight = null;
  setAuthState(initial);
}

type AuthMeResponse = {
  login_required?: boolean;
  auth_required?: boolean;
  authenticated?: boolean;
  user?: string;
};

// inflight is the read that is already on its way. A burst of 401s - every
// request the page had in the air when the session ended - must ask the server
// once, not once each.
let inflight: Promise<AuthState> | null = null;

/**
 * refreshAuthState asks the server what it wants.
 *
 * Anything unexpected - a network error, a 404 from a server built before this
 * existed - is read as "no sign-in here", so the app renders exactly as it did
 * before rather than trapping the user behind a screen the server cannot
 * answer. That answer is provisional: a later 401 from the API asks again, which
 * is how a page that came up during a blip still finds its way to the form.
 */
export function refreshAuthState(): Promise<AuthState> {
  if (!inflight) {
    inflight = readAuthState().finally(() => {
      inflight = null;
    });
  }
  return inflight;
}

async function readAuthState(): Promise<AuthState> {
  let next: AuthState = { ...initial, loaded: true };
  try {
    const res = await localFetch("/coddy/auth/me", {
      headers: { Accept: "application/json" },
    });
    if (res.ok) {
      const body = (await res.json()) as AuthMeResponse;
      next = {
        loginRequired: body.login_required === true,
        authRequired: body.auth_required === true,
        authenticated: body.authenticated === true,
        user: typeof body.user === "string" ? body.user : "",
        loaded: true,
      };
    }
  } catch {
    /* treated as "no sign-in", see above */
  }
  setAuthState(next);
  return next;
}

export type SignInResult = { ok: boolean; status: number; error?: string };

/** signIn posts the form. The cookie, when there is one, is set by the server. */
export async function signIn(
  user: string,
  password: string,
): Promise<SignInResult> {
  let res: Response;
  try {
    res = await localFetch("/coddy/auth/login", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Accept: "application/json",
      },
      body: JSON.stringify({ user, password }),
    });
  } catch (e) {
    return { ok: false, status: 0, error: e instanceof Error ? e.message : "" };
  }
  if (res.ok) {
    await refreshAuthState();
    return { ok: true, status: res.status };
  }
  let error = "";
  try {
    const body = (await res.json()) as { error?: string };
    error = typeof body.error === "string" ? body.error : "";
  } catch {
    /* the status alone is the message */
  }
  return { ok: false, status: res.status, error };
}

/**
 * signOut drops the session on the server and forgets it here.
 *
 * It reports whether the server actually let go. Pretending otherwise is worse
 * than useless: the cookie is HttpOnly, so a page that clears its own state and
 * reloads is signed straight back in, and the button looks broken rather than
 * refused.
 */
export async function signOut(): Promise<boolean> {
  let ok = false;
  try {
    const res = await localFetch("/coddy/auth/logout", {
      method: "POST",
      headers: { Accept: "application/json" },
    });
    ok = res.ok;
  } catch {
    /* unreachable server: the state below is what the page can still do */
  }
  if (ok) {
    setAuthState({ ...current, authenticated: false, user: "" });
    return true;
  }
  // The server still holds the session, so the honest thing is to show that.
  await refreshAuthState();
  return false;
}

/**
 * installAuthUnauthorizedWatch sends the app back to the sign-in screen when a
 * call it makes later is refused.
 *
 * A session can end while the page is open - it expires, the operator rotates
 * the password, somebody signs out in another tab - and without this the app
 * would sit there rendering empty lists over a row of 401s.
 */
export function installAuthUnauthorizedWatch(): () => void {
  return onLocalApiUnauthorized(() => {
    if (getEnv().mode !== "local") {
      return;
    }
    // Already on the sign-in screen: the 401s behind it are expected, and
    // asking again would only add noise.
    if (current.loginRequired && !current.authenticated) {
      return;
    }
    // Everything else is worth re-reading, including the case this exists for:
    // the boot read failed and the page is now rendering an app over an API
    // that refuses it. A burst of refusals collapses into one question, because
    // refreshAuthState shares the read that is already on its way.
    void refreshAuthState();
  });
}
