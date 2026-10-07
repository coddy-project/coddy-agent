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
