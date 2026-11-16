/**
 * applyModelsChange replaces the `models` array in the settings doc and keeps the
 * default-model references in sync. When a logical model id is renamed in place
 * (the model list keeps the same length, one entry's `model` string changes),
 * every reference that used the old id is rewritten to the new id. This prevents
 * the config from becoming invalid after renaming the only model of a provider.
 *
 * Only in-place renames are reconciled. On add/remove (length change) references
 * are left untouched. Removing a referenced model should not silently repoint it.
 */
export function applyModelsChange(
  doc: Record<string, unknown>,
  nextModels: unknown[],
  state: ModelCascadeState = createModelCascadeState(),
): Record<string, unknown> {
  const base: Record<string, unknown> = { ...doc, models: nextModels };

  const previousModels = Array.isArray(doc.models) ? doc.models : [];
  const prevIds = modelIds(previousModels);
  const nextIds = modelIds(nextModels);
  if (prevIds.length !== nextIds.length) {
    syncRowIdentities(nextModels, nextIds, state.modelRows);
    return base;
  }

  const prevIdentities = prevIds.map((id, i) => {
    const row = rowObject(previousModels[i]);
    return (row && state.modelRows.get(row)) ?? id;
  });
  const plan = planRowIdentities(prevIdentities, nextIds, prevIds);
  let reconciledModels = nextModels;
  for (let i = 0; i < prevIds.length; i++) {
    let nextRow = rowObject(nextModels[i]);
    const renamed = plan.renames.get(prevIdentities[i]!);
    const metadata = nextRow ? rowObject(nextRow.kimi_metadata) : undefined;
    if (
      nextRow &&
      renamed !== undefined &&
      (metadata?.model === prevIdentities[i] || metadata?.model === prevIds[i])
    ) {
      const replacement = {
        ...nextRow,
        kimi_metadata: { ...metadata, model: renamed },
      };
      if (reconciledModels === nextModels) {
        reconciledModels = [...nextModels];
      }
      reconciledModels[i] = replacement;
      nextRow = replacement;
    }
    if (nextRow) {
      state.modelRows.set(nextRow, plan.identities[i]!);
    }
  }
  base.models = reconciledModels;
  const renames = plan.renames;
  if (renames.size === 0) {
    return base;
  }

  for (const key of MODEL_REFERENCE_SECTIONS) {
    base[key] = renameModelRef(base[key], renames, hasFallbackModels(key));
  }
  return base;
}

/**
 * applyProvidersChange keeps logical models and their references attached to a
 * provider renamed in place. Removing a provider also removes its models, clears
 * selectors that named them and drops them from fallback chains.
 */
export function applyProvidersChange(
  doc: Record<string, unknown>,
  nextProviders: unknown[],
  state: ModelCascadeState = createModelCascadeState(),
): Record<string, unknown> {
  const base: Record<string, unknown> = { ...doc, providers: nextProviders };
  const previousProviders = Array.isArray(doc.providers) ? doc.providers : [];
  const prevNames = rowNames(previousProviders);
  const nextNames = rowNames(nextProviders);

  if (prevNames.length === nextNames.length) {
    const prevNameTotals = nameCounts(prevNames);
    const prevIdentities = prevNames.map((name, i) => {
      const row = rowObject(previousProviders[i]);
      const recorded = row && state.providerRows.get(row);
      const reservedByAnother = previousProviders.some((other, j) => {
        const otherRow = rowObject(other);
        return (
          i !== j && !!otherRow && state.providerRows.get(otherRow) === name
        );
      });
      return recorded === "" &&
        name !== "" &&
        nextNames[i] !== name &&
        prevNameTotals.get(name) === 1 &&
        !reservedByAnother
        ? name
        : (recorded ?? name);
    });
    const plan = planRowIdentities(prevIdentities, nextNames, prevNames);
    const providerRenames = plan.renames;
    for (let i = 0; i < prevNames.length; i++) {
      const nextRow = rowObject(nextProviders[i]);
      if (nextRow) {
        state.providerRows.set(nextRow, plan.identities[i]!);
      }
    }
    if (providerRenames.size > 0) {
      if (Array.isArray(doc.models)) {
        base.models = doc.models.map((row) => {
          const obj = rowObject(row);
          if (!obj) {
            return row;
          }
          const visibleId = typeof obj.model === "string" ? obj.model : "";
          const identity = state.modelRows.get(obj) ?? visibleId;
          const directlyRenamed = renameProviderId(visibleId, providerRenames);
          const rebased = renameProviderId(identity, providerRenames);
          const coherentId =
            visibleId !== "" &&
            identity !== "" &&
            modelProvider(visibleId) !== modelProvider(identity)
              ? (rebased ?? directlyRenamed ?? visibleId)
              : (directlyRenamed ?? visibleId);
          const renamed = coherentId === visibleId ? undefined : coherentId;
          const metadata = rowObject(obj.kimi_metadata);
          let renamedMetadata: string | undefined;
          if (metadata && typeof metadata.model === "string") {
            if (metadata.model === visibleId && renamed !== undefined) {
              renamedMetadata = renamed;
            } else if (metadata.model === identity && rebased !== undefined) {
              renamedMetadata = rebased;
            }
          }
          const nextRow =
            renamed === undefined && renamedMetadata === undefined
              ? row
              : {
                  ...obj,
                  ...(renamed === undefined ? {} : { model: renamed }),
                  ...(metadata && renamedMetadata !== undefined
                    ? {
                        kimi_metadata: {
                          ...metadata,
                          model: renamedMetadata,
                        },
                      }
                    : {}),
                };
          const nextObject = rowObject(nextRow);
          if (
            nextObject &&
            (renamed !== undefined ||
              rebased !== undefined ||
              renamedMetadata !== undefined)
          ) {
            state.modelRows.set(nextObject, rebased ?? identity);
          }
          return nextRow;
        });
      }
      for (const key of MODEL_REFERENCE_SECTIONS) {
        base[key] = mapModelRef(
          base[key],
          (id) => renameProviderId(id, providerRenames),
          hasFallbackModels(key),
        );
      }
    }
    return base;
  }

  const remainingRows = new Set(
    nextProviders.filter((row) => rowObject(row) !== undefined),
  );
  const remainingIdentities = new Set<string>();
  for (let i = 0; i < nextProviders.length; i++) {
    const row = rowObject(nextProviders[i]);
    const identity = (row && state.providerRows.get(row)) ?? nextNames[i]!;
    if (identity !== "") {
      remainingIdentities.add(identity);
    }
  }
  const removedNames = new Set<string>();
  for (let i = 0; i < prevNames.length; i++) {
    const row = rowObject(previousProviders[i]);
    if (row && remainingRows.has(row)) {
      continue;
    }
    const identity = (row && state.providerRows.get(row)) ?? prevNames[i]!;
    if (identity !== "" && !remainingIdentities.has(identity)) {
      removedNames.add(identity);
    }
  }
  syncRowIdentities(nextProviders, nextNames, state.providerRows, false);
  if (removedNames.size === 0) {
    return base;
  }

  if (Array.isArray(doc.models)) {
    base.models = doc.models.flatMap((row) => {
      const obj = rowObject(row);
      if (!obj) {
        return [row];
      }
      const visibleId = typeof obj.model === "string" ? obj.model : "";
      const identity = state.modelRows.get(obj) ?? visibleId;
      if (hasRemovedProvider(identity, removedNames)) {
        return [];
      }
      if (
        visibleId === "" ||
        identity === "" ||
        !hasRemovedProvider(visibleId, removedNames)
      ) {
        return [row];
      }
      const metadata = rowObject(obj.kimi_metadata);
      const restored = {
        ...obj,
        model: identity,
        ...(metadata?.model === visibleId
          ? { kimi_metadata: { ...metadata, model: identity } }
          : {}),
      };
      state.modelRows.set(restored, identity);
      return [restored];
    });
  }
  for (const key of MODEL_REFERENCE_SECTIONS) {
    base[key] = removeProviderRef(
      base[key],
      removedNames,
      hasFallbackModels(key),
    );
  }
  return base;
}

/** Identities follow row objects through blank and duplicate names in the form. */
export interface ModelCascadeState {
  readonly providerRows: WeakMap<object, string>;
  readonly modelRows: WeakMap<object, string>;
}

export function createModelCascadeState(): ModelCascadeState {
  return { providerRows: new WeakMap(), modelRows: new WeakMap() };
}

const MODEL_REFERENCE_SECTIONS = [
  "agent",
  "memory",
  "compaction",
  "supervisor",
] as const;

function renameModelRef(
  section: unknown,
  renames: Map<string, string>,
  includeFallbacks: boolean,
): unknown {
  return mapModelRef(section, (id) => renames.get(id), includeFallbacks);
}

function mapModelRef(
  section: unknown,
  rename: (id: string) => string | undefined,
  includeFallbacks: boolean,
): unknown {
  if (!section || typeof section !== "object" || Array.isArray(section)) {
    return section;
  }
  const obj = section as Record<string, unknown>;
  let next: Record<string, unknown> | undefined;
  const current = obj.model;
  if (typeof current === "string") {
    const renamed = rename(current);
    if (renamed !== undefined) {
      next = { ...obj, model: renamed };
    }
  }
  if (includeFallbacks && Array.isArray(obj.fallback_models)) {
    let changed = false;
    const fallbacks = obj.fallback_models.map((id) => {
      const renamed = typeof id === "string" ? rename(id) : undefined;
      if (renamed === undefined) {
        return id;
      }
      changed = true;
      return renamed;
    });
    if (changed) {
      next = { ...(next ?? obj), fallback_models: fallbacks };
    }
  }
  return next ?? section;
}

function removeProviderRef(
  section: unknown,
  removed: Set<string>,
  includeFallbacks: boolean,
): unknown {
  if (!section || typeof section !== "object" || Array.isArray(section)) {
    return section;
  }
  const obj = section as Record<string, unknown>;
  let next: Record<string, unknown> | undefined;
  if (typeof obj.model === "string" && hasRemovedProvider(obj.model, removed)) {
    next = { ...obj, model: "" };
  }
  if (includeFallbacks && Array.isArray(obj.fallback_models)) {
    const fallbacks = obj.fallback_models.filter(
      (id) => typeof id !== "string" || !hasRemovedProvider(id, removed),
    );
    if (fallbacks.length !== obj.fallback_models.length) {
      next = { ...(next ?? obj), fallback_models: fallbacks };
    }
  }
  return next ?? section;
}

function renameProviderId(
  id: string,
  renames: Map<string, string>,
): string | undefined {
  const slash = id.indexOf("/");
  const provider = slash > 0 ? renames.get(id.slice(0, slash)) : undefined;
  return provider === undefined ? undefined : `${provider}${id.slice(slash)}`;
}

function hasRemovedProvider(id: string, removed: Set<string>): boolean {
  return removed.has(modelProvider(id));
}

function hasFallbackModels(section: string): boolean {
  return section === "memory" || section === "compaction";
}

function modelProvider(id: string): string {
  const slash = id.indexOf("/");
  return slash > 0 ? id.slice(0, slash) : "";
}

function rowObject(row: unknown): Record<string, unknown> | undefined {
  return row && typeof row === "object" && !Array.isArray(row)
    ? (row as Record<string, unknown>)
    : undefined;
}

function syncRowIdentities(
  rows: unknown[],
  names: string[],
  identities: WeakMap<object, string>,
  promoteEmpty = true,
): void {
  for (let i = 0; i < rows.length; i++) {
    const row = rowObject(rows[i]);
    if (!row || identities.has(row)) {
      continue;
    }
    let reserved = false;
    for (let j = 0; j < rows.length; j++) {
      const other = rowObject(rows[j]);
      if (
        i !== j &&
        names[i] !== "" &&
        other &&
        (identities.get(other) === names[i] ||
          (identities.get(other) === "" && names[j] === names[i]))
      ) {
        reserved = true;
        break;
      }
    }
    identities.set(row, reserved ? "" : names[i]!);
  }

  if (!promoteEmpty) {
    return;
  }

  const nameTotals = nameCounts(names);
  for (let i = 0; i < rows.length; i++) {
    const row = rowObject(rows[i]);
    if (
      !row ||
      identities.get(row) !== "" ||
      names[i] === "" ||
      nameTotals.get(names[i]!) !== 1
    ) {
      continue;
    }
    let reserved = false;
    for (let j = 0; j < rows.length; j++) {
      const other = rowObject(rows[j]);
      if (
        i !== j &&
        other &&
        (identities.get(other) === names[i] ||
          (identities.get(other) === "" && names[j] === names[i]))
      ) {
        reserved = true;
        break;
      }
    }
    if (!reserved) {
      identities.set(row, names[i]!);
    }
  }
}

function planRowIdentities(
  previous: string[],
  next: string[],
  previousVisible: string[],
): { identities: string[]; renames: Map<string, string> } {
  const sourceCounts = nameCounts(previous);
  const targetCounts = nameCounts(next);
  const changing = previous.map(
    (from, i) =>
      from !== next[i] &&
      next[i] !== "" &&
      targetCounts.get(next[i]!) === 1 &&
      (from !== "" || previousVisible[i] !== next[i]),
  );

  let changed = true;
  while (changed) {
    changed = false;
    for (let i = 0; i < changing.length; i++) {
      if (!changing[i]) {
        continue;
      }
      for (let j = 0; j < previous.length; j++) {
        if (i !== j && previous[j] === next[i] && !changing[j]) {
          changing[i] = false;
          changed = true;
          break;
        }
      }
    }
  }

  const identities = [...previous];
  const renames = new Map<string, string>();
  for (let i = 0; i < changing.length; i++) {
    if (!changing[i]) {
      continue;
    }
    const from = previous[i]!;
    const to = next[i]!;
    identities[i] = to;
    if (from !== "" && sourceCounts.get(from) === 1) {
      renames.set(from, to);
    }
  }
  return { identities, renames };
}

function nameCounts(names: string[]): Map<string, number> {
  const counts = new Map<string, number>();
  for (const name of names) {
    counts.set(name, (counts.get(name) ?? 0) + 1);
  }
  return counts;
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
