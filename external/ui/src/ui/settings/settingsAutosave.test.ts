import { describe, expect, test } from "vitest";
import type { JsonSchema } from "./SchemaForm";
import {
  collectSaveRules,
  freshRows,
  pendingChanges,
  sameDocument,
  sameValue,
  setIn,
  stepRowIds,
  withoutPending,
  type Doc,
  type DraftRows,
} from "./settingsAutosave";

const schema: JsonSchema = {
  type: "object",
  properties: {
    providers: {
      type: "array",
      "x-coddy-save": "confirm-removal",
      items: {
        type: "object",
        properties: { name: { type: "string" }, api_base: { type: "string" } },
      },
    },
    agent: {
      type: "object",
      properties: { max_turns: { type: "integer" } },
    },
    gateways: {
      type: "object",
      properties: {
        telegram: {
          type: "object",
          properties: {
            enable: { type: "boolean", "x-coddy-save": "confirm" },
            token: { type: "string" },
          },
        },
      },
    },
  },
};

const rules = collectSaveRules(schema);

function base(): Doc {
  return {
    revision: "r1",
    providers: [
      { name: "a", api_base: "http://a" },
      { name: "b" },
      { name: "c" },
    ],
    agent: { max_turns: 40 },
    gateways: { telegram: { token: "" } },
  };
}

/** A form that holds doc over the saved document b, its rows followed edit by edit. */
function form(b: Doc) {
  let doc = b;
  let rows: DraftRows = freshRows(rules, b);
  return {
    edit(next: (d: Doc) => Doc) {
      const n = next(doc);
      rows = { ...rows, doc: stepRowIds(rules, rows.doc, doc, n) };
      doc = n;
    },
    get doc() {
      return doc;
    },
    get rows() {
      return rows;
    },
  };
}

const providers = (d: Doc) => d.providers as Record<string, unknown>[];
const names = (d: Doc) => providers(d).map((p) => p.name);

describe("collectSaveRules", () => {
  test("reads a change mark on a field and a removal mark on a list", () => {
    expect(rules.map((r) => [r.path.join("."), r.kind])).toEqual([
      ["providers", "removal"],
      ["gateways.telegram.enable", "confirm"],
    ]);
  });

  test("a removal mark on anything but a list is not a rule", () => {
    const odd: JsonSchema = {
      type: "object",
      properties: { x: { type: "string", "x-coddy-save": "confirm-removal" } },
    };
    expect(collectSaveRules(odd)).toEqual([]);
    expect(collectSaveRules(null)).toEqual([]);
  });
});

describe("pending changes", () => {
  test("an ordinary edit holds nothing", () => {
    const f = form(base());
    f.edit((d) => setIn(d, ["agent", "max_turns"], 42));
    expect(pendingChanges(rules, base(), f.doc, f.rows)).toEqual([]);
  });

  test("a switch the document never carried and one turned off again are the same", () => {
    const b = base();
    const f = form(b);
    f.edit((d) => setIn(d, ["gateways", "telegram", "enable"], true));
    expect(pendingChanges(rules, b, f.doc, f.rows)).toHaveLength(1);
    f.edit((d) => setIn(d, ["gateways", "telegram", "enable"], false));
    expect(pendingChanges(rules, b, f.doc, f.rows)).toEqual([]);
  });

  test("a row taken out is held, a row renamed in place is not", () => {
    const b = base();
    const f = form(b);
    f.edit((d) => ({
      ...d,
      providers: providers(d).map((p, i) =>
        i === 0 ? { ...p, name: "a2" } : p,
      ),
    }));
    expect(pendingChanges(rules, b, f.doc, f.rows)).toEqual([]);
    f.edit((d) => ({
      ...d,
      providers: providers(d).filter((_, i) => i !== 1),
    }));
    const held = pendingChanges(rules, b, f.doc, f.rows);
    expect(held).toHaveLength(1);
    expect(held[0]).toMatchObject({ kind: "removal", row: { name: "b" } });
  });

  test("a row added and taken out again before a save holds nothing", () => {
    const b = base();
    const f = form(b);
    f.edit((d) => ({ ...d, providers: [...providers(d), { name: "" }] }));
    f.edit((d) => ({ ...d, providers: providers(d).slice(0, 3) }));
    expect(pendingChanges(rules, b, f.doc, f.rows)).toEqual([]);
  });

  test("a row taken out and a new one added at the end is a removal, not a rename", () => {
    const b = base();
    const f = form(b);
    f.edit((d) => ({ ...d, providers: providers(d).slice(0, 2) }));
    f.edit((d) => ({ ...d, providers: [...providers(d), { name: "d" }] }));
    const held = pendingChanges(rules, b, f.doc, f.rows);
    expect(held).toHaveLength(1);
    expect(held[0]).toMatchObject({ kind: "removal", row: { name: "c" } });
  });

  test("a row taken out and added again under its name is an edit, not a removal", () => {
    const b = base();
    const f = form(b);
    f.edit((d) => ({ ...d, providers: providers(d).slice(0, 2) }));
    f.edit((d) => ({
      ...d,
      providers: [...providers(d), { name: "c", api_base: "http://new" }],
    }));
    expect(pendingChanges(rules, b, f.doc, f.rows)).toEqual([]);
    expect(names(withoutPending(rules, b, f.doc, f.rows).doc)).toEqual([
      "a",
      "b",
      "c",
    ]);
  });
});

describe("withoutPending", () => {
  test("puts a held row back where it stood, edits and new rows kept", () => {
    const b = base();
    const f = form(b);
    f.edit((d) => ({
      ...d,
      providers: providers(d).filter((_, i) => i !== 0),
    }));
    f.edit((d) => ({
      ...d,
      providers: providers(d).map((p) =>
        p.name === "c" ? { ...p, api_base: "http://c" } : p,
      ),
    }));
    f.edit((d) => ({ ...d, providers: [...providers(d), { name: "d" }] }));
    f.edit((d) => setIn(d, ["agent", "max_turns"], 7));
    const out = withoutPending(rules, b, f.doc, f.rows);
    expect(names(out.doc)).toEqual(["a", "b", "c", "d"]);
    expect(providers(out.doc)[0]).toBe(providers(b)[0]);
    expect(providers(out.doc)[2]).toEqual({ name: "c", api_base: "http://c" });
    expect((out.doc.agent as Doc).max_turns).toBe(7);
    // The returned ids follow the returned rows: with them as the saved
    // document's, nothing is held any more except what the form still lacks.
    const after: DraftRows = { base: out.ids, doc: f.rows.doc };
    const held = pendingChanges(rules, out.doc, f.doc, after);
    expect(held.map((c) => c.kind === "removal" && c.row)).toEqual([
      { name: "a", api_base: "http://a" },
    ]);
  });

  test("held rows go back before the rows added since", () => {
    const b = base();
    const f = form(b);
    f.edit((d) => ({
      ...d,
      providers: providers(d).filter((_, i) => i === 1),
    }));
    f.edit((d) => ({ ...d, providers: [...providers(d), { name: "d" }] }));
    expect(names(withoutPending(rules, b, f.doc, f.rows).doc)).toEqual([
      "a",
      "b",
      "c",
      "d",
    ]);
  });

  test("a held switch goes back to the saved value, taken out when the document had none", () => {
    const b = base();
    const f = form(b);
    f.edit((d) => setIn(d, ["gateways", "telegram", "enable"], true));
    f.edit((d) => setIn(d, ["gateways", "telegram", "token"], "123:abc"));
    const out = withoutPending(rules, b, f.doc, f.rows);
    expect(out.doc.gateways).toEqual({ telegram: { token: "123:abc" } });
  });

  test("returns the form's own document when nothing is held", () => {
    const b = base();
    const f = form(b);
    f.edit((d) => setIn(d, ["agent", "max_turns"], 1));
    expect(withoutPending(rules, b, f.doc, f.rows).doc).toBe(f.doc);
  });
});

describe("sameValue and sameDocument", () => {
  test("empty values and key order do not count as a change", () => {
    expect(sameValue({ a: "", b: [] }, undefined)).toBe(true);
    expect(sameValue({ x: 1, y: 2 }, { y: 2, x: 1 })).toBe(true);
    expect(sameValue(undefined, false, { type: "boolean" })).toBe(true);
    expect(sameValue(undefined, true, { type: "boolean", default: true })).toBe(
      true,
    );
    expect(sameValue(8080, 8081)).toBe(false);
  });

  test("a document compares without its revision", () => {
    expect(sameDocument({ ...base(), revision: "r2" }, base())).toBe(true);
    expect(sameDocument(setIn(base(), ["agent", "max_turns"], 1), base())).toBe(
      false,
    );
  });
});
