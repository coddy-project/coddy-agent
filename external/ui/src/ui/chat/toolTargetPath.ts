// How a transcript row spells the path a call touched.
//
// The row names its target next to the label, and on a worktree checkout the
// absolute path spends most of the row on the part every row shares, pushing the
// file name past the ellipsis. Against the session's own directory the same file
// is a handful of segments. The absolute spelling stays in the expanded card and
// in the row's title attribute, where there is room for it.

/** `C:\` or `C:/` - the only absolute form that is not rooted at a separator. */
const WINDOWS_DRIVE = /^[a-zA-Z]:[\\/]/;

/** `\\server\share` - a root of its own, with no relation to a local directory. */
function isUNC(value: string): boolean {
  return value.startsWith("\\\\") || value.startsWith("//");
}

function isAbsolutePath(value: string): boolean {
  return value.startsWith("/") || isUNC(value) || WINDOWS_DRIVE.test(value);
}

/** Windows compares paths without regard to case; POSIX does not. */
function isWindowsPath(value: string): boolean {
  return WINDOWS_DRIVE.test(value) || value.startsWith("\\");
}

/** The separator a path already uses, so a rewritten path keeps reading like itself. */
function separatorOf(value: string): string {
  return value.includes("\\") && !value.includes("/") ? "\\" : "/";
}

function segmentsOf(value: string): string[] {
  return value.split(/[\\/]+/).filter((segment) => segment !== "");
}

/**
 * The shorter spelling of `target` for a session working in `cwd`: relative when
 * that says the same thing in less space, the original otherwise.
 *
 * Only absolute filesystem paths are rewritten. A command, a search pattern, a
 * url or a name is returned untouched - the caller decides what kind of target it
 * holds (see `toolCallTargetIsPath`).
 */
export function relativeToolTarget(target: string, cwd: string): string {
  const path = target.trim();
  const base = cwd.trim();
  if (!path || !base || !isAbsolutePath(path) || !isAbsolutePath(base)) {
    return target;
  }
  // A UNC share is not reachable from a drive letter by walking up, and the two
  // roots say nothing about each other.
  if (isUNC(path) !== isUNC(base)) {
    return target;
  }

  const fold = (value: string) =>
    isWindowsPath(path) || isWindowsPath(base) ? value.toLowerCase() : value;
  const pathSegments = segmentsOf(path);
  const baseSegments = segmentsOf(base);
  // Different drives, or a UNC pair on different shares: no walk connects them.
  if (
    baseSegments.length > 0 &&
    pathSegments.length > 0 &&
    isAbsolutePath(path) &&
    (WINDOWS_DRIVE.test(path) || WINDOWS_DRIVE.test(base) || isUNC(path)) &&
    fold(String(pathSegments[0])) !== fold(String(baseSegments[0]))
  ) {
    return target;
  }

  let shared = 0;
  while (
    shared < pathSegments.length &&
    shared < baseSegments.length &&
    fold(String(pathSegments[shared])) === fold(String(baseSegments[shared]))
  ) {
    shared++;
  }

  const up = baseSegments.length - shared;
  const down = pathSegments.slice(shared);
  if (up === 0 && down.length === 0) {
    return ".";
  }

  const separator = separatorOf(path);
  const relative = [...Array<string>(up).fill(".."), ...down].join(separator);
  return relative.length < path.length ? relative : target;
}
