export type InstallableLike = {
  name: string;
  description: string;
  installed: boolean;
};

/**
 * Filter the not-yet-installed marketplace plugins whose name or description
 * matches `query` (case-insensitive), capped at `limit`. Returns the visible
 * matches plus the number dropped by the cap, so the dropdown can show a
 * "+N more" hint instead of silently truncating. An empty query yields no
 * matches (the dropdown stays closed).
 */
export function filterInstallableMatches<T extends InstallableLike>(
  available: readonly T[],
  query: string,
  limit: number,
): { matches: T[]; more: number } {
  const q = query.trim().toLowerCase();
  if (!q) return { matches: [], more: 0 };
  const all = available.filter(
    (p) =>
      !p.installed &&
      (p.name.toLowerCase().includes(q) ||
        (p.description || "").toLowerCase().includes(q)),
  );
  const matches = limit > 0 ? all.slice(0, limit) : all;
  return { matches, more: Math.max(0, all.length - matches.length) };
}
