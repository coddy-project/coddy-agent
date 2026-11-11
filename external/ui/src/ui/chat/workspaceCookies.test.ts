import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { setEnv } from "../env/remoteEnv";
import {
  CODDY_WORKSPACE_DIR_COOKIE,
  CODDY_WORKTREE_COOKIE,
  DESKTOP_WORKSPACE_RESOLVE_TIMEOUT_MS,
  readLastWorkspaceDir,
  readWorktreePref,
  publishWorkspaceToDesktop,
  takeDesktopWorkspaceHandoff,
  takeDesktopWorkspaceDir,
  waitForDesktopWorkspace,
  writeLastWorkspaceDir,
  writeWorktreePref,
} from "./workspaceCookies";

/**
 * What the start screen remembers in this browser: the folder last picked for
 * a new chat, one per environment (a remote's folders are its own), and the
 * worktree checkbox, one for every folder and environment.
 */

function clear() {
  for (const name of [CODDY_WORKSPACE_DIR_COOKIE, CODDY_WORKTREE_COOKIE]) {
    document.cookie = `${name}=; Path=/; Max-Age=0`;
  }
}

beforeEach(() => {
  clear();
  setEnv({ mode: "local" });
});

afterEach(() => {
  clear();
  setEnv({ mode: "local" });
  vi.useRealTimers();
  vi.restoreAllMocks();
});

test("nothing picked yet: no folder, the worktree checkbox off", () => {
  expect(readLastWorkspaceDir()).toBe("");
  expect(readWorktreePref()).toBe(false);
});

test("the folder picked last is remembered, per environment", () => {
  writeLastWorkspaceDir("/home/me/src/app with spaces");
  expect(readLastWorkspaceDir()).toBe("/home/me/src/app with spaces");
  setEnv({ mode: "remote", baseUrl: "http://box.lan:12345", token: "" });
  expect(readLastWorkspaceDir()).toBe("");
  writeLastWorkspaceDir("/srv/work");
  expect(readLastWorkspaceDir()).toBe("/srv/work");
  setEnv({ mode: "local" });
  expect(readLastWorkspaceDir()).toBe("/home/me/src/app with spaces");
  // An empty path forgets this environment's folder only.
  writeLastWorkspaceDir("");
  expect(readLastWorkspaceDir()).toBe("");
  setEnv({ mode: "remote", baseUrl: "http://box.lan:12345", token: "" });
  expect(readLastWorkspaceDir()).toBe("/srv/work");
});

test("the worktree checkbox is one choice for every folder and environment", () => {
  writeWorktreePref(true);
  expect(readWorktreePref()).toBe(true);
  setEnv({ mode: "remote", baseUrl: "http://box.lan:12345", token: "" });
  expect(readWorktreePref()).toBe(true);
  writeWorktreePref(false);
  expect(readWorktreePref()).toBe(false);
});

test("a cookie that is not ours to read is ignored", () => {
  document.cookie = `${CODDY_WORKSPACE_DIR_COOKIE}=${encodeURIComponent("not json")}; Path=/`;
  expect(readLastWorkspaceDir()).toBe("");
  writeLastWorkspaceDir("/x");
  expect(readLastWorkspaceDir()).toBe("/x");
});

test("a desktop workspace handoff is read once and removed from the address", () => {
  history.replaceState(
    null,
    "",
    "/?desktop_workspace=%2Fsrv%2Ffrom%20desktop&lang=en#/",
  );
  expect(takeDesktopWorkspaceDir()).toBe("/srv/from desktop");
  expect(window.location.search).toBe("?lang=en");
  expect(window.location.hash).toBe("#/");
  expect(takeDesktopWorkspaceDir()).toBe("");
});

test("a pending desktop handoff keeps its fallback and is consumed once", () => {
  history.replaceState(
    null,
    "",
    "/?desktop_workspace=%2Fsrv%2Ffallback&desktop_workspace_pending=1&lang=en#/",
  );
  expect(takeDesktopWorkspaceHandoff()).toEqual({
    path: "/srv/fallback",
    pending: true,
  });
  expect(window.location.search).toBe("?lang=en");
  expect(takeDesktopWorkspaceHandoff()).toEqual({ path: "", pending: false });
});

test("desktop resolution accepts only the parent at this origin and answers ready", async () => {
  const posted = vi.spyOn(window, "postMessage").mockImplementation(() => {});
  const onPath = vi.fn();
  const wait = waitForDesktopWorkspace(onPath);
  expect(posted).toHaveBeenCalledWith(
    { channel: "coddy.desktop", type: "workspace-ready" },
    window.location.origin,
  );
  const data = {
    channel: "coddy.desktop",
    type: "workspace-resolved",
    path: "/srv/resolved",
  };
  window.dispatchEvent(
    new MessageEvent("message", {
      data,
      origin: "https://untrusted.example",
      source: window.parent,
    }),
  );
  window.dispatchEvent(
    new MessageEvent("message", {
      data,
      origin: window.location.origin,
      source: null,
    }),
  );
  expect(onPath).not.toHaveBeenCalled();

  window.dispatchEvent(
    new MessageEvent("message", {
      data,
      origin: window.location.origin,
      source: window.parent,
    }),
  );
  await wait.done;
  expect(onPath).toHaveBeenCalledWith("/srv/resolved");
});

test("desktop resolution times out with the fallback still in force", async () => {
  vi.useFakeTimers();
  vi.spyOn(window, "postMessage").mockImplementation(() => {});
  const onPath = vi.fn();
  const wait = waitForDesktopWorkspace(onPath);
  await vi.advanceTimersByTimeAsync(DESKTOP_WORKSPACE_RESOLVE_TIMEOUT_MS);
  await wait.done;
  expect(onPath).not.toHaveBeenCalled();
});

test("a failed ready request settles desktop resolution instead of hanging", async () => {
  vi.spyOn(window, "postMessage").mockImplementation(() => {
    throw new Error("parent unavailable");
  });
  const onPath = vi.fn();
  const wait = waitForDesktopWorkspace(onPath);
  await wait.done;
  expect(onPath).not.toHaveBeenCalled();
});

test("a desktop draft replaces and clears its workspace handoff", () => {
  const frames = document.createElement("div");
  frames.className = "frames";
  const frame = document.createElement("iframe");
  frames.append(frame);
  document.body.append(frames);
  Object.defineProperty(window, "frameElement", {
    configurable: true,
    value: frame,
  });
  const posted = vi.spyOn(window, "postMessage").mockImplementation(() => {});
  try {
    history.replaceState(null, "", "/?lang=en#/");
    publishWorkspaceToDesktop("/srv/one");
    expect(
      new URLSearchParams(window.location.search).get("desktop_workspace"),
    ).toBe("/srv/one");
    publishWorkspaceToDesktop("");
    expect(window.location.search).toBe("?lang=en");
    expect(posted).toHaveBeenLastCalledWith(
      { channel: "coddy.desktop", type: "workspace", path: "" },
      window.location.origin,
    );
  } finally {
    Reflect.deleteProperty(window, "frameElement");
    frames.remove();
  }
});
