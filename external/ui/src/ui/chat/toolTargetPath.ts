// How a transcript row spells the path a call touched.
//
// The row names its target next to the label, and an absolute path spends most
// of the row on the part every row shares, pushing the file name past the
// ellipsis. Against the directory the work is actually happening in, the same
// file is a handful of segments.
//
// Which directory that is depends on where the file lives. A session opened on a
// checkout that also holds worktrees reads a file inside one of them against
// *that worktree*, not against the checkout it hangs under: the worktree is the
// root of the work, and the path to it is exactly the part that says nothing.
// Anything outside every worktree reads against the session's own directory.
//
// The absolute spelling stays in the expanded card and in the row's title
// attribute, where there is room for it.

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

/** Two paths that cannot be walked between: different drives, or a share and a disk. */
function rootsDiffer(path: string, base: string): boolean {
  if (isUNC(path) !== isUNC(base)) {
    return true;
  }
  if (!WINDOWS_DRIVE.test(path) && !WINDOWS_DRIVE.test(base) && !isUNC(path)) {
    return false;
  }
  const first = (value: string) => segmentsOf(value)[0] ?? "";
  return foldFor(path, base)(first(path)) !== foldFor(path, base)(first(base));
}

function foldFor(path: string, base: string): (value: string) => string {
  return isWindowsPath(path) || isWindowsPath(base)
    ? (value: string) => value.toLowerCase()
    : (value: string) => value;
}

/** Whether `path` lies inside `root` (or is `root` itself). */
function holds(root: string, path: string): boolean {
  if (rootsDiffer(path, root)) {
    return false;
  }
  const fold = foldFor(path, root);
  const rootSegments = segmentsOf(root);
  const pathSegments = segmentsOf(path);
  if (pathSegments.length < rootSegments.length) {
    return false;
  }
  return rootSegments.every(
    (segment, i) => fold(segment) === fold(String(pathSegments[i])),
  );
}

/**
 * The shorter spelling of `target` for a session whose work roots are `roots`:
 * relative when that says the same thing in less space, the original otherwise.
 *
 * `roots` is the session's own directory followed by the worktrees of its
 * workspace. The deepest root holding the target wins, so a file inside a
 * worktree reads against that worktree; a target outside all of them falls back
 * to the first root and may walk up, but only while walking up stays shorter.
 *
 * Only absolute filesystem paths are rewritten. A command, a search pattern, a
 * url or a name is returned untouched - the caller decides what kind of target
 * it holds (see `toolCallTargetIsPath`).
 */
export function relativeToolTarget(
  target: string,
  roots: readonly string[],
): string {
  const path = target.trim();
  if (!path || !isAbsolutePath(path)) {
    return target;
  }
  const candidates = roots
    .map((root) => root.trim())
    .filter((root) => root !== "" && isAbsolutePath(root));
  if (candidates.length === 0) {
    return target;
  }

  let deepest = "";
  for (const root of candidates) {
    if (
      holds(root, path) &&
      segmentsOf(root).length >= segmentsOf(deepest).length
    ) {
      deepest = root;
    }
  }
  const base = deepest || String(candidates[0]);
  if (rootsDiffer(path, base)) {
    return target;
  }

  const fold = foldFor(path, base);
  const pathSegments = segmentsOf(path);
  const baseSegments = segmentsOf(base);
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
