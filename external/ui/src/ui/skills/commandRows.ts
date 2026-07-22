// A built-in command row (name + one-line description), shaped like a slash-menu
// SlashRow. Backs the composer's "Commands" group, sourced from GET /coddy/commands.
export type CommandRow = { name: string; description: string };

// filterCommandRows returns the built-in command rows whose name starts with the
// given prefix (case-insensitive). A leading "/" in the prefix is ignored and an
// empty prefix returns every row.
export function filterCommandRows(
  rows: CommandRow[],
  prefix: string,
): CommandRow[] {
  const p = prefix.trim().replace(/^\//, "").toLowerCase();
  if (!p) {
    return rows;
  }
  return rows.filter((r) => r.name.toLowerCase().startsWith(p));
}
