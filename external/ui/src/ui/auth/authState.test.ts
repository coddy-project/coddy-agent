import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

// The module reaches the server through localFetch, which captures the native
// fetch when remoteEnv loads, so the module is mocked rather than the global.
let respond: (url: string, init?: RequestInit) => Response = () =>
  ({ ok: true, status: 200, json: async () => ({}) }) as Response;
const seen: { url: string; method: string }[] = [];
let envMode: "local" | "remote" = "local";
let unauthorizedCb: (() => void) | null = null;
// hang, when set, is the answer the next call waits on, so a test can look at
// the moment between asking and being told.
let hang: Promise<Response> | null = null;

vi.mock("../env/remoteEnv", async (importOriginal) => {
  const actual = await importOriginal<typeof import("../env/remoteEnv")>();
  return {
    ...actual,
    getEnv: () =>
      envMode === "local"
        ? { mode: "local" }
        : { mode: "remote", baseUrl: "http://box:12345", token: "t" },
    onLocalApiUnauthorized: (cb: () => void) => {
      unauthorizedCb = cb;
      return () => {
        unauthorizedCb = null;
      };
    },
    localFetch: (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      seen.push({ url, method: init?.method ?? "GET" });
      if (hang) {
        return hang;
      }
      return Promise.resolve(respond(url, init));
    },
  };
});

const {
  refreshAuthState,
  resetAuthStateForTests,
  signIn,
  signOut,
  snapshotAuth,
} = await import("./authState");

function jsonResponse(status: number, body: unknown): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    json: async () => body,
  } as Response;
}

beforeEach(() => {
  resetAuthStateForTests();
  seen.length = 0;
  envMode = "local";
  hang = null;
});

afterEach(() => {
  vi.restoreAllMocks();
});

describe("refreshAuthState", () => {
  it("reads what the server says", async () => {
    respond = () =>
      jsonResponse(200, {
        login_required: true,
        auth_required: true,
        authenticated: true,
        user: "operator",
      });
    const state = await refreshAuthState();
    expect(state).toEqual({
      loginRequired: true,
      authRequired: true,
      authenticated: true,
      user: "operator",
      loaded: true,
    });
    expect(snapshotAuth()).toEqual(state);
    expect(seen[0]?.url).toBe("/coddy/auth/me");
  });

  it("treats a server without the route as one that wants no sign-in", async () => {
    // An older coddy serve answers 404 here. The app must render as it always
    // did rather than trap the user behind a form nothing can satisfy.
    respond = () => jsonResponse(404, {});
    const state = await refreshAuthState();
    expect(state.loaded).toBe(true);
    expect(state.loginRequired).toBe(false);
  });

  it("treats an unreachable server the same way", async () => {
    respond = () => {
      throw new Error("connection refused");
    };
    const state = await refreshAuthState();
    expect(state.loaded).toBe(true);
    expect(state.loginRequired).toBe(false);
  });

  it("notifies subscribers", async () => {
    const { subscribeAuth } = await import("./authState");
    const calls: number[] = [];
    const stop = subscribeAuth(() => calls.push(1));
    respond = () => jsonResponse(200, { login_required: true });
    await refreshAuthState();
    stop();
    expect(calls.length).toBeGreaterThan(0);
  });
});

describe("signIn", () => {
  it("posts the credentials and re-reads the state on success", async () => {
    respond = (url) => {
      if (url === "/coddy/auth/login") {
        return jsonResponse(200, { ok: true, user: "operator" });
      }
      return jsonResponse(200, {
        login_required: true,
        authenticated: true,
        user: "operator",
      });
    };
    const res = await signIn("operator", "correct-horse");
    expect(res.ok).toBe(true);
    expect(seen[0]).toEqual({ url: "/coddy/auth/login", method: "POST" });
    expect(seen[1]?.url).toBe("/coddy/auth/me");
    expect(snapshotAuth().authenticated).toBe(true);
    expect(snapshotAuth().user).toBe("operator");
  });

  it("returns the status and the message of a refusal", async () => {
    respond = () => jsonResponse(401, { error: "invalid credentials" });
    const res = await signIn("operator", "wrong");
    expect(res.ok).toBe(false);
    expect(res.status).toBe(401);
    expect(res.error).toBe("invalid credentials");
    // A refused sign-in must not claim a session.
    expect(snapshotAuth().authenticated).toBe(false);
  });

  it("survives a server that answers nothing at all", async () => {
    respond = () => {
      throw new Error("network down");
    };
    const res = await signIn("operator", "correct-horse");
    expect(res.ok).toBe(false);
    expect(res.status).toBe(0);
  });
});

describe("signOut", () => {
  it("asks the server and forgets the session here", async () => {
    respond = () =>
      jsonResponse(200, {
        login_required: true,
        authenticated: true,
        user: "operator",
      });
    await refreshAuthState();
    expect(snapshotAuth().authenticated).toBe(true);

    seen.length = 0;
    respond = () => jsonResponse(200, { ok: true });
    expect(await signOut()).toBe(true);
    expect(seen[0]).toEqual({ url: "/coddy/auth/logout", method: "POST" });
    expect(snapshotAuth().authenticated).toBe(false);
    expect(snapshotAuth().user).toBe("");
  });

  it("reports a refusal instead of pretending, because the cookie is the server's", async () => {
    // A page that cleared its own state and reloaded would be signed straight
    // back in by the cookie it cannot see, and the button would look broken.
    respond = (url) => {
      if (url === "/coddy/auth/logout") {
        return jsonResponse(403, { error: "cross-site request refused" });
      }
      return jsonResponse(200, {
        login_required: true,
        authenticated: true,
        user: "operator",
      });
    };
    await refreshAuthState();
    expect(await signOut()).toBe(false);
    expect(snapshotAuth().authenticated).toBe(true);
  });

  it("says so when the server cannot be reached at all", async () => {
    respond = () =>
      jsonResponse(200, { login_required: true, authenticated: true });
    await refreshAuthState();
    respond = () => {
      throw new Error("gone");
    };
    expect(await signOut()).toBe(false);
  });
});

describe("installAuthUnauthorizedWatch", () => {
  it("re-reads the state when a local API call is refused", async () => {
    const { installAuthUnauthorizedWatch } = await import("./authState");
    respond = () =>
      jsonResponse(200, {
        login_required: true,
        authenticated: true,
        user: "operator",
      });
    await refreshAuthState();
    expect(snapshotAuth().authenticated).toBe(true);

    const stop = installAuthUnauthorizedWatch();
    respond = () =>
      jsonResponse(200, { login_required: true, authenticated: false });
    // This is the signal the fetch shim raises when a same-origin API call comes
    // back 401 - the session ended while the page was open.
    unauthorizedCb?.();
    await vi.waitFor(() => expect(snapshotAuth().authenticated).toBe(false));
    stop();
  });

  it("recovers a page that came up while the server was unreachable", async () => {
    // The boot read failed, so the gate rendered the app over an API that
    // refuses it. The first 401 has to be what puts the form back.
    const { installAuthUnauthorizedWatch } = await import("./authState");
    respond = () => {
      throw new Error("connection refused");
    };
    await refreshAuthState();
    expect(snapshotAuth()).toMatchObject({
      loaded: true,
      loginRequired: false,
    });

    const stop = installAuthUnauthorizedWatch();
    respond = () =>
      jsonResponse(200, { login_required: true, authenticated: false });
    unauthorizedCb?.();
    await vi.waitFor(() => expect(snapshotAuth().loginRequired).toBe(true));
    stop();
  });

  it("asks once for a burst of refusals", async () => {
    // Every request the page had in the air when the session ended answers 401
    // at about the same moment; that must be one question, not a dozen.
    const { installAuthUnauthorizedWatch } = await import("./authState");
    respond = () =>
      jsonResponse(200, { login_required: true, authenticated: true });
    await refreshAuthState();

    const stop = installAuthUnauthorizedWatch();
    seen.length = 0;
    let release: ((r: Response) => void) | null = null;
    hang = new Promise<Response>((resolve) => {
      release = resolve;
    });
    unauthorizedCb?.();
    unauthorizedCb?.();
    unauthorizedCb?.();
    expect(seen.filter((s) => s.url === "/coddy/auth/me")).toHaveLength(1);
    release?.(
      jsonResponse(200, { login_required: true, authenticated: false }),
    );
    hang = null;
    await vi.waitFor(() => expect(snapshotAuth().authenticated).toBe(false));
    stop();
  });

  it("says nothing more once the sign-in screen is up", async () => {
    const { installAuthUnauthorizedWatch } = await import("./authState");
    respond = () =>
      jsonResponse(200, { login_required: true, authenticated: false });
    await refreshAuthState();
    const stop = installAuthUnauthorizedWatch();
    seen.length = 0;
    unauthorizedCb?.();
    expect(seen).toHaveLength(0);
    stop();
  });

  it("stays quiet for a remote environment", async () => {
    const { installAuthUnauthorizedWatch } = await import("./authState");
    respond = () =>
      jsonResponse(200, { login_required: false, authenticated: false });
    await refreshAuthState();
    const stop = installAuthUnauthorizedWatch();

    envMode = "remote";
    seen.length = 0;
    unauthorizedCb?.();
    expect(seen).toHaveLength(0);
    stop();
  });
});
