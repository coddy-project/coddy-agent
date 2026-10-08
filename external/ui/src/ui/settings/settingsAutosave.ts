// What the Settings form saves on its own and what waits for the Save button
// (issue #485).
//
// The form saves a moment after the last edit. A few changes are a deliberate
// act and wait for Save instead; the schema marks them (x-coddy-save, set by
// internal/config/ui_schema.go): "confirm" on a field holds any change of it -
// the address a server binds, a subsystem of coddy serve turned on or off, a
// relay's credentials - and "confirm-removal" on a list holds a row taken out
// of it (a provider, a model), while rows added or edited save on their own.
//
// A save on its own sends the form's document with every such change undone,
// so the rest goes through while those wait; Save sends the document as it is.
//
// The rows of a watched list are told apart by an id the form gives each one,
// not by their name: a row renamed in place is the same row (a model rename
// moves the references to it along, applyModelsChange), and a name typed in two
// sittings is not a row taken out. The form edits a list only in three ways -
// one row changed in place, one row taken out, one row added at the end - and
// each edit is followed as it happens (stepRowIds).

import type { JsonSchema } from "./SchemaForm";

/** A field or list whose change waits for Save. */
export type SaveRule = {
  /** Path of the field from the root of the document. */
  path: string[];
  /** "confirm": any change waits; "removal": a row taken out waits. */
  kind: "confirm" | "removal";
  schema: JsonSchema;
};

/** A change the form holds until Save. */
export type PendingChange =
  | { kind: "change"; path: string[]; schema: JsonSchema; value: unknown }
  | { kind: "removal"; path: string[]; schema: JsonSchema; row: unknown };

/** The id of every row of each watched list (key: the list's path joined by "."). */
export type RowIds = Record<string, number[]>;

/** Row ids of the saved document (base) and of the form's (doc). */
export type DraftRows = { base: RowIds; doc: RowIds };

export type Doc = Record<string, unknown>;

/**
 * collectSaveRules reads the marks of a settings schema. Array items are not
 * walked: a mark there has no single path in the document.
 */
export function collectSaveRules(schema: JsonSchema | null): SaveRule[] {
  const out: SaveRule[] = [];
  const walk = (node: JsonSchema, path: string[]) => {
    const how = node["x-coddy-save"];
    if (path.length > 0 && how === "confirm") {
      out.push({ path, kind: "confirm", schema: node });
    } else if (
      path.length > 0 &&
      how === "confirm-removal" &&
      node.type === "array"
    ) {
      out.push({ path, kind: "removal", schema: node });
    }
    for (const [k, sub] of Object.entries(node.properties ?? {})) {
      walk(sub, [...path, k]);
    }
  };
  if (schema) {
    walk(schema, []);
  }
  return out;
}

export function ruleKey(path: string[]): string {
  return path.join(".");
}

function isObject(v: unknown): v is Record<string, unknown> {
  return v !== null && typeof v === "object" && !Array.isArray(v);
}

/** getIn reads the value at path, undefined where the document has none. */
export function getIn(doc: unknown, path: string[]): unknown {
  let cur: unknown = doc;
  for (const k of path) {
    if (!isObject(cur)) {
      return undefined;
    }
    cur = cur[k];
  }
  return cur;
}

/**
 * setIn returns doc with value at path, copying only the objects on the way;
 * undefined takes the key out. A missing object on the way is created.
 */
export function setIn(doc: Doc, path: string[], value: unknown): Doc {
  const [head, ...rest] = path;
  if (head === undefined) {
    return doc;
  }
  const out: Doc = { ...doc };
  if (rest.length === 0) {
    if (value === undefined) {
      delete out[head];
    } else {
      out[head] = value;
    }
    return out;
  }
  const child = isObject(doc[head]) ? (doc[head] as Doc) : {};
  out[head] = setIn(child, rest, value);
  return out;
}

function listAt(doc: unknown, path: string[]): unknown[] {
  const v = getIn(doc, path);
  return Array.isArray(v) ? v : [];
}

/**
 * normalized is a value as the form means it: an empty string, null, an empty
 * list and an object of nothing but those are all "not set", and a switch the
 * document does not carry is its default. Keys are sorted, so two objects
 * that differ only in the order of their keys compare equal.
 */
function normalized(v: unknown, schema?: JsonSchema): unknown {
  if (v === undefined || v === null || v === "") {
    if (schema?.type === "boolean") {
      return schema.default === true;
    }
    return undefined;
  }
  if (Array.isArray(v)) {
    const items = v.map((x) => normalized(x, schema?.items) ?? null);
    return items.length === 0 ? undefined : items;
  }
  if (isObject(v)) {
    const out: Record<string, unknown> = {};
    for (const k of Object.keys(v).sort()) {
      const n = normalized(v[k], schema?.properties?.[k]);
      if (n !== undefined) {
        out[k] = n;
      }
    }
    return Object.keys(out).length === 0 ? undefined : out;
  }
  return v;
}

/** sameValue compares two values the way the form means them (normalized). */
export function sameValue(
  a: unknown,
  b: unknown,
  schema?: JsonSchema,
): boolean {
  return (
    JSON.stringify(normalized(a, schema)) ===
    JSON.stringify(normalized(b, schema))
  );
}

/** sameDocument compares two whole documents, the revision left out. */
export function sameDocument(a: Doc | null, b: Doc | null): boolean {
  if (a === b) {
    return true;
  }
  if (a === null || b === null) {
    return false;
  }
  const strip = (d: Doc) => {
    const { revision: _revision, ...rest } = d;
    return rest;
  };
  return sameValue(strip(a), strip(b));
}

let lastRowId = 0;

function freshIds(n: number): number[] {
  return Array.from({ length: n }, () => ++lastRowId);
}

/**
 * freshRows gives every row of the watched lists of doc an id, the same in the
 * saved document and the form's: a form that has just taken a document holds
 * nothing of its own.
 */
export function freshRows(rules: SaveRule[], doc: Doc | null): DraftRows {
  const ids: RowIds = {};
  for (const r of rules) {
    if (r.kind === "removal") {
      ids[ruleKey(r.path)] = freshIds(listAt(doc, r.path).length);
    }
  }
  return { base: ids, doc: ids };
}

function labelOf(row: unknown, field: string): string {
  return isObject(row) && row[field] !== undefined && row[field] !== null
    ? String(row[field]).trim()
    : "";
}

/**
 * rowLabelField is the field that names a row of the list: the first of name,
 * model and url its item schema has.
 */
export function rowLabelField(schema: JsonSchema): string {
  const props = schema.items?.properties ?? {};
  return ["name", "model", "url"].find((f) => f in props) ?? "name";
}

/**
 * matchRowIds names the rows of next after the rows of prev with the same
 * label, unmatched rows getting new ids. It is the fallback for an edit that
 * is none of the three the form makes, where the rows cannot be followed one
 * by one.
 */
function matchRowIds(
  rule: SaveRule,
  prev: unknown[],
  prevIds: number[],
  next: unknown[],
): number[] {
  const field = rowLabelField(rule.schema);
  const used = new Set<number>();
  return next.map((row) => {
    const label = labelOf(row, field);
    if (label !== "") {
      for (let i = 0; i < prev.length; i++) {
        const id = prevIds[i];
        if (
          id !== undefined &&
          !used.has(id) &&
          labelOf(prev[i], field) === label
        ) {
          used.add(id);
          return id;
        }
      }
    }
    return ++lastRowId;
  });
}

/**
 * matchRows gives the rows of base fresh ids and names the form's rows after
 * them by their label: for a form whose rows were not followed edit by edit
 * (the schema, and with it the watched lists, arrived after the document).
 */
export function matchRows(
  rules: SaveRule[],
  base: Doc | null,
  doc: Doc,
): DraftRows {
  const fresh = freshRows(rules, base);
  if (base === null || doc === base) {
    return fresh;
  }
  const docIds: RowIds = {};
  for (const r of rules) {
    if (r.kind === "removal") {
      const key = ruleKey(r.path);
      docIds[key] = matchRowIds(
        r,
        listAt(base, r.path),
        fresh.base[key] ?? [],
        listAt(doc, r.path),
      );
    }
  }
  return { base: fresh.base, doc: docIds };
}

/**
 * stepRowIds follows one edit of the form through the watched lists: a row
 * changed in place keeps its id, a row taken out takes its id along, a row
 * added at the end gets a new one. Rows are compared by identity: an edit
 * replaces only the row it changes.
 */
export function stepRowIds(
  rules: SaveRule[],
  ids: RowIds,
  prevDoc: Doc,
  nextDoc: Doc,
): RowIds {
  let out = ids;
  for (const r of rules) {
    if (r.kind !== "removal") {
      continue;
    }
    const key = ruleKey(r.path);
    const prev = listAt(prevDoc, r.path);
    const next = listAt(nextDoc, r.path);
    if (prev === next) {
      continue;
    }
    const cur = ids[key] ?? freshIds(prev.length);
    let stepped: number[];
    if (next.length === prev.length) {
      stepped = cur;
    } else if (
      next.length === prev.length + 1 &&
      prev.every((row, i) => next[i] === row)
    ) {
      stepped = [...cur, ++lastRowId];
    } else if (next.length === prev.length - 1) {
      let i = 0;
      while (i < next.length && next[i] === prev[i]) {
        i++;
      }
      const restSame = next.slice(i).every((row, j) => row === prev[i + j + 1]);
      stepped = restSame
        ? [...cur.slice(0, i), ...cur.slice(i + 1)]
        : matchRowIds(r, prev, cur, next);
    } else {
      stepped = matchRowIds(r, prev, cur, next);
    }
    if (stepped !== cur || ids[key] === undefined) {
      out = { ...out, [key]: stepped };
    }
  }
  return out;
}

/**
 * heldRows tells which rows of the saved list the form took out and holds
 * until Save: a row whose id the form no longer has, unless the form has a row
 * of the same name - one taken out and added again is the same entry of the
 * configuration, edited, and putting the old one back would name it twice.
 */
function heldRows(rule: SaveRule, docList: unknown[], rows: DraftRows) {
  const key = ruleKey(rule.path);
  const kept = new Set(rows.doc[key] ?? []);
  const field = rowLabelField(rule.schema);
  const named = new Set(
    docList.map((row) => labelOf(row, field)).filter((l) => l !== ""),
  );
  const baseIds = rows.base[key] ?? [];
  return (i: number, row: unknown): boolean => {
    const id = baseIds[i];
    return id !== undefined && !kept.has(id) && !named.has(labelOf(row, field));
  };
}

/** pendingChanges lists what the form holds until Save, in schema order. */
export function pendingChanges(
  rules: SaveRule[],
  base: Doc | null,
  doc: Doc,
  rows: DraftRows,
): PendingChange[] {
  if (base === null) {
    return [];
  }
  const out: PendingChange[] = [];
  for (const r of rules) {
    if (r.kind === "confirm") {
      const value = getIn(doc, r.path);
      if (!sameValue(getIn(base, r.path), value, r.schema)) {
        out.push({ kind: "change", path: r.path, schema: r.schema, value });
      }
      continue;
    }
    const held = heldRows(r, listAt(doc, r.path), rows);
    listAt(base, r.path).forEach((row, i) => {
      if (held(i, row)) {
        out.push({ kind: "removal", path: r.path, schema: r.schema, row });
      }
    });
  }
  return out;
}

/**
 * withoutPending returns doc with every change that waits for Save undone: a
 * held field back to the saved value, a held row back where it stood among
 * the rows the form still has. It is what a save on its own sends, and what
 * Discard leaves in the form. ids are the row ids of the returned document.
 */
export function withoutPending(
  rules: SaveRule[],
  base: Doc | null,
  doc: Doc,
  rows: DraftRows,
): { doc: Doc; ids: RowIds } {
  if (base === null) {
    return { doc, ids: rows.doc };
  }
  let out = doc;
  let ids = rows.doc;
  for (const r of rules) {
    if (r.kind === "confirm") {
      // The saved value itself, even where the form's means the same (a
      // switch turned on and off again is false where the file has nothing):
      // the document then equals the saved one and nothing goes out.
      const saved = getIn(base, r.path);
      if (getIn(out, r.path) !== saved) {
        out = setIn(out, r.path, saved);
      }
      continue;
    }
    const key = ruleKey(r.path);
    const docList = listAt(out, r.path);
    const docIds = rows.doc[key] ?? [];
    const baseList = listAt(base, r.path);
    const baseIds = rows.base[key] ?? [];
    const held = heldRows(r, docList, rows);
    if (!baseList.some((row, i) => held(i, row))) {
      continue;
    }
    const at = new Map(baseIds.map((id, i) => [id, i]));
    const list: unknown[] = [];
    const listIds: number[] = [];
    // Rows only ever leave a list or join its end, so the rows the form kept
    // are in the saved order and a held row goes back before the first kept
    // row that stood after it.
    let next = 0;
    const putBackUpTo = (end: number) => {
      for (; next < end; next++) {
        if (held(next, baseList[next])) {
          list.push(baseList[next]);
          listIds.push(baseIds[next]!);
        }
      }
    };
    docList.forEach((row, j) => {
      const id = docIds[j] ?? ++lastRowId;
      const i = at.get(id);
      if (i !== undefined && i >= next) {
        putBackUpTo(i);
        next = i + 1;
      } else if (i === undefined) {
        // Rows added since stand after every saved one.
        putBackUpTo(baseList.length);
      }
      list.push(row);
      listIds.push(id);
    });
    putBackUpTo(baseList.length);
    out = setIn(out, r.path, list);
    ids = { ...ids, [key]: listIds };
  }
  return { doc: out, ids };
}

/**
 * autosaveDocument is what a save on its own sends: the form's document with
 * the changes that wait for Save undone (withoutPending), and without a row
 * added to a watched list that does not have what its schema requires yet
 * (a provider with no name or type, a model with no id). Such a row is being
 * filled in: the server would refuse it, and with it the rest of the form.
 * It goes out with the first save after it is complete; Save sends it as it
 * is.
 */
export function autosaveDocument(
  rules: SaveRule[],
  base: Doc | null,
  doc: Doc,
  rows: DraftRows,
): { doc: Doc; ids: RowIds } {
  const held = withoutPending(rules, base, doc, rows);
  let out = held.doc;
  let ids = held.ids;
  for (const r of rules) {
    const required = r.schema.items?.required ?? [];
    if (r.kind !== "removal" || required.length === 0) {
      continue;
    }
    const key = ruleKey(r.path);
    const saved = new Set(rows.base[key] ?? []);
    const list = listAt(out, r.path);
    const listIds = ids[key] ?? [];
    const keep = list.map(
      (row, i) =>
        saved.has(listIds[i] ?? -1) ||
        required.every((f) => labelOf(row, f) !== ""),
    );
    if (keep.every(Boolean)) {
      continue;
    }
    out = setIn(
      out,
      r.path,
      list.filter((_, i) => keep[i]),
    );
    ids = { ...ids, [key]: listIds.filter((_, i) => keep[i]) };
  }
  return { doc: out, ids };
}
