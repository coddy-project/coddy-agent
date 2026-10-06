import { afterEach, beforeEach, expect, test } from "vitest";
import { setEnv } from "../env/remoteEnv";
import {
  CODDY_WORKSPACE_DIR_COOKIE,
  CODDY_WORKTREE_COOKIE,
  readLastWorkspaceDir,
  readWorktreePref,
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
