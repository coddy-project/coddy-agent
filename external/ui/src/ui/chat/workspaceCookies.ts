import { envStorageSuffix } from "../env/remoteEnv";

/**
 * What the start screen remembers in this browser, in cookies like the theme
 * and the model: the folder last picked for a new chat - one per environment,
 * since a remote's folders are its own - and the worktree checkbox, one choice
 * for every folder and environment. A chat left for the start screen hands
 * neither its folder nor its branch to the next one; the branch is always the
 * one the folder is on.
 */

export const CODDY_WORKSPACE_DIR_COOKIE = "coddy_workspace_dir";
export const CODDY_WORKTREE_COOKIE = "coddy_worktree";
export const DESKTOP_WORKSPACE_RESOLVE_TIMEOUT_MS = 6500;

const MAX_AGE_SECONDS = 365 * 24 * 60 * 60;

function readCookie(name: string): string | null {
  if (typeof document === "undefined") {
    return null;
  }
  for (const part of document.cookie.split(";")) {
    const s = part.trim();
    if (s.startsWith(`${name}=`)) {
      try {
        return decodeURIComponent(s.slice(name.length + 1));
      } catch {
        return null;
      }
    }
  }
  return null;
}

function writeCookie(name: string, value: string): void {
  if (typeof document === "undefined") {
    return;
  }
  const secure =
    typeof window !== "undefined" && window.location.protocol === "https:"
      ? "; Secure"
      : "";
  document.cookie = `${name}=${encodeURIComponent(value)}; Path=/; Max-Age=${MAX_AGE_SECONDS}; SameSite=Lax${secure}`;
}

/** The folders picked last, by environment. */
function readDirs(): Record<string, string> {
  const raw = readCookie(CODDY_WORKSPACE_DIR_COOKIE);
  if (!raw) {
    return {};
  }
  try {
    const parsed = JSON.parse(raw) as unknown;
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
      return {};
    }
    const out: Record<string, string> = {};
    for (const [key, value] of Object.entries(parsed)) {
      if (typeof value === "string" && value.trim()) {
        out[key] = value;
      }
    }
    return out;
  } catch {
    return {};
  }
}

/** The folder last picked for a new chat in this environment, or "". */
export function readLastWorkspaceDir(): string {
  return readDirs()[envStorageSuffix()] ?? "";
}

/**
 * A new tab of Coddy Desktop hands its start screen a folder in the address.
 * The handoff belongs to that tab alone, so it wins over the shared cookie
 * once and then leaves the address before a later folder pick can be undone
 * by a restart of the tab.
 */
export function takeDesktopWorkspaceHandoff(): {
  path: string;
  pending: boolean;
} {
  if (typeof window === "undefined") {
    return { path: "", pending: false };
  }
  const url = new URL(window.location.href);
  const path = url.searchParams.get("desktop_workspace")?.trim() ?? "";
  const pending = url.searchParams.get("desktop_workspace_pending") === "1";
  if (!path && !pending) {
    return { path: "", pending: false };
  }
  url.searchParams.delete("desktop_workspace");
  url.searchParams.delete("desktop_workspace_pending");
  window.history.replaceState(
    window.history.state,
    "",
    `${url.pathname}${url.search}${url.hash}`,
  );
  return { path, pending };
}

export function takeDesktopWorkspaceDir(): string {
  return takeDesktopWorkspaceHandoff().path;
}

export type DesktopWorkspaceWait = {
  done: Promise<void>;
  isSettled: () => boolean;
  /** A manual folder pick settles the wait without applying a late answer. */
  finish: () => void;
};

/**
 * Waits for the desktop shell's authoritative folder without reloading the
 * SPA. The ready request covers a shell that resolved before React mounted;
 * the timeout leaves the address's fallback in force when no answer arrives.
 */
export function waitForDesktopWorkspace(
  onPath: (path: string) => void,
  timeoutMs = DESKTOP_WORKSPACE_RESOLVE_TIMEOUT_MS,
): DesktopWorkspaceWait {
  let settled = false;
  let timer = 0;
  let resolveDone = () => {};
  const done = new Promise<void>((resolve) => {
    resolveDone = resolve;
  });
  const finish = () => {
    if (settled) return;
    settled = true;
    window.clearTimeout(timer);
    window.removeEventListener("message", onMessage);
    resolveDone();
  };
  const onMessage = (event: MessageEvent) => {
    if (
      event.origin !== window.location.origin ||
      event.source !== window.parent
    ) {
      return;
    }
    const data = event.data as {
      channel?: unknown;
      type?: unknown;
      path?: unknown;
    } | null;
    if (
      !data ||
      data.channel !== "coddy.desktop" ||
      data.type !== "workspace-resolved" ||
      typeof data.path !== "string"
    ) {
      return;
    }
    try {
      onPath(data.path);
    } finally {
      finish();
    }
  };
  window.addEventListener("message", onMessage);
  timer = window.setTimeout(finish, timeoutMs);
  try {
    window.parent.postMessage(
      { channel: "coddy.desktop", type: "workspace-ready" },
      window.location.origin,
    );
  } catch {
    finish();
  }
  return { done, isSettled: () => settled, finish };
}

/** Keeps the draft's folder on its own desktop tab, not only in a shared cookie. */
export function publishWorkspaceToDesktop(path: string, manual = false): void {
  if (typeof window === "undefined") {
    return;
  }
  const frame = window.frameElement;
  if (frame?.closest(".frames")) {
    const url = new URL(window.location.href);
    if (path) {
      url.searchParams.set("desktop_workspace", path);
    } else {
      url.searchParams.delete("desktop_workspace");
    }
    window.history.replaceState(
      window.history.state,
      "",
      `${url.pathname}${url.search}${url.hash}`,
    );
  }
  window.parent.postMessage(
    {
      channel: "coddy.desktop",
      type: "workspace",
      path,
      ...(manual ? { manual: true } : {}),
    },
    window.location.origin,
  );
}

/** Remembers the folder picked for a new chat; an empty path forgets it. */
export function writeLastWorkspaceDir(path: string): void {
  const dirs = readDirs();
  const key = envStorageSuffix();
  if (path.trim()) {
    dirs[key] = path;
  } else {
    delete dirs[key];
  }
  writeCookie(CODDY_WORKSPACE_DIR_COOKIE, JSON.stringify(dirs));
}

/** Whether new chats open branch switches in a worktree. */
export function readWorktreePref(): boolean {
  return readCookie(CODDY_WORKTREE_COOKIE) === "1";
}

export function writeWorktreePref(on: boolean): void {
  writeCookie(CODDY_WORKTREE_COOKIE, on ? "1" : "0");
}
