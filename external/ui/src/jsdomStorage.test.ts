import { afterEach, expect, test, vi } from "vitest";
import { installJsdomStorage } from "./jsdomStorage";

// Node 25 keeps a Web Storage of its own on the global object, and the jsdom
// environment of vitest left it there: localStorage read undefined and every
// test that touched it failed before it started (issue #609).

afterEach(() => {
  vi.unstubAllGlobals();
});

function jsdomWindow(): Window {
  const dom = (globalThis as { jsdom?: { window: Window } }).jsdom;
  if (!dom) throw new Error("the test runs in the jsdom environment");
  return dom.window;
}

test("the storages a test reads are jsdom's, whatever Node keeps", () => {
  const win = jsdomWindow();
  expect(window.localStorage).toBe(win.localStorage);
  expect(globalThis.localStorage).toBe(win.localStorage);
  expect(window.sessionStorage).toBe(win.sessionStorage);
  expect(localStorage).toBeInstanceOf(Storage);
  expect(sessionStorage).toBeInstanceOf(Storage);
  localStorage.setItem("coddy_issue_609", "kept");
  expect(win.localStorage.getItem("coddy_issue_609")).toBe("kept");
  localStorage.removeItem("coddy_issue_609");
});

test("a test can still replace a storage", () => {
  const fake = { getItem: () => "stub" } as unknown as Storage;
  vi.stubGlobal("localStorage", fake);
  expect(localStorage.getItem("any")).toBe("stub");
  vi.unstubAllGlobals();
  expect(localStorage).toBe(jsdomWindow().localStorage);
});

test("installJsdomStorage puts a window's storages over the global's own", () => {
  const local = { area: "local" } as unknown as Storage;
  const session = { area: "session" } as unknown as Storage;
  const nodeSession = { area: "node" };
  // The shape Node 25 gives the global object: localStorage is a getter that
  // answers undefined without --localstorage-file, sessionStorage its own.
  const target: Record<string, unknown> = {};
  Object.defineProperty(target, "localStorage", {
    configurable: true,
    get: () => undefined,
  });
  Object.defineProperty(target, "sessionStorage", {
    configurable: true,
    get: () => nodeSession,
  });

  installJsdomStorage(target, { localStorage: local, sessionStorage: session });

  expect(target.localStorage).toBe(local);
  expect(target.sessionStorage).toBe(session);
  // An assignment replaces it, as it does for every other window property
  // the environment puts on the global.
  const replaced = { area: "replaced" };
  target.localStorage = replaced;
  expect(target.localStorage).toBe(replaced);
  expect(target.sessionStorage).toBe(session);
});
