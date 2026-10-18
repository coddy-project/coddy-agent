/**
 * applyModelsChange replaces the `models` array in the settings doc and keeps the
 * default-model references in sync. When a logical model id is renamed in place
 * (the model list keeps the same length, one entry's `model` string changes),
 * every reference that used the old id — `agent.model` and `memory.model` — is
 * rewritten to the new id. This prevents the config from becoming invalid
 * (backend rejects `agent.model`/`memory.model` not present in the models list)
 * after renaming the only model of a provider.
 *
 * Only in-place renames are reconciled. On add/remove (length change) references
 * are left untouched — removing a referenced model should not silently repoint it.
 */
export function applyModelsChange(
  doc: Record<string, unknown>,
  nextModels: unknown[],
): Record<string, unknown> {
  const base: Record<string, unknown> = { ...doc, models: nextModels };

  const prevIds = modelIds(doc.models);
  const nextIds = modelIds(nextModels);
  if (prevIds.length !== nextIds.length) {
    return base;
  }

  const renames = new Map<string, string>();
  for (let i = 0; i < prevIds.length; i++) {
    const from = prevIds[i]!;
    const to = nextIds[i]!;
    if (from !== "" && from !== to) {
      renames.set(from, to);
    }
  }
  if (renames.size === 0) {
    return base;
  }

  base.agent = renameModelRef(base.agent, renames);
  base.memory = renameModelRef(base.memory, renames);
  return base;
}

function renameModelRef(
  section: unknown,
  renames: Map<string, string>,
): unknown {
  if (!section || typeof section !== "object" || Array.isArray(section)) {
    return section;
  }
  const obj = section as Record<string, unknown>;
  const current = obj.model;
  if (typeof current !== "string") {
    return section;
  }
  const renamed = renames.get(current);
  if (renamed === undefined) {
    return section;
  }
  return { ...obj, model: renamed };
}

function modelIds(models: unknown): string[] {
  if (!Array.isArray(models)) {
    return [];
  }
  return models.map((row) => {
    if (row && typeof row === "object" && !Array.isArray(row)) {
      const cell = (row as Record<string, unknown>).model;
      return cell === undefined || cell === null ? "" : String(cell);
    }
    return "";
  });
}

/**
 * applyToolsetsChange replaces the `toolsets` array in the settings doc and,
 * the way applyModelsChange keeps the model references, rewrites the
 * `models[].toolset` that named a preset renamed in place, so the saved
 * configuration does not name a toolset that is gone. A preset added or
 * removed leaves the references alone.
 */
export function applyToolsetsChange(
  doc: Record<string, unknown>,
  nextToolsets: unknown[],
): Record<string, unknown> {
  const base: Record<string, unknown> = { ...doc, toolsets: nextToolsets };
  const prev = rowNames(doc.toolsets);
  const next = rowNames(nextToolsets);
  if (prev.length !== next.length || !Array.isArray(doc.models)) {
    return base;
  }
  const renames = new Map<string, string>();
  for (let i = 0; i < prev.length; i++) {
    if (prev[i] !== "" && prev[i] !== next[i]) {
      renames.set(prev[i]!, next[i]!);
    }
  }
  if (renames.size === 0) {
    return base;
  }
  base.models = doc.models.map((row) => {
    if (!row || typeof row !== "object" || Array.isArray(row)) {
      return row;
    }
    const obj = row as Record<string, unknown>;
    const renamed =
      typeof obj.toolset === "string" ? renames.get(obj.toolset) : undefined;
    return renamed === undefined ? row : { ...obj, toolset: renamed };
  });
  return base;
}

function rowNames(rows: unknown): string[] {
  if (!Array.isArray(rows)) {
    return [];
  }
  return rows.map((row) => {
    if (row && typeof row === "object" && !Array.isArray(row)) {
      const cell = (row as Record<string, unknown>).name;
      return cell === undefined || cell === null ? "" : String(cell);
    }
    return "";
  });
}
