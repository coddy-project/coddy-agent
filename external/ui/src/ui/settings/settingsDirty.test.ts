import { expect, test } from "vitest";
import type { JsonSchema } from "./SchemaForm";
import { settingsFormDirty } from "./settingsDirty";

const schema: JsonSchema = {
  type: "object",
  properties: {
    tools: {
      type: "object",
      properties: {
        http_request: {
          type: "object",
          properties: {
            default_headers: {
              type: "object",
              additionalProperties: { type: "string" },
            },
          },
        },
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
            enable: { type: "boolean" },
            rich_messages: { type: "boolean", default: true },
            token: { type: "string" },
          },
        },
      },
    },
  },
};

const base = {
  revision: "r1",
  agent: { max_turns: 40 },
  gateways: { telegram: { token: "" } },
};

test("a form that has not been edited holds nothing unsaved", () => {
  expect(settingsFormDirty(null, {}, schema)).toBe(false);
  expect(settingsFormDirty(base, base, schema)).toBe(false);
});

test("a changed value is an unsaved edit", () => {
  expect(
    settingsFormDirty(base, { ...base, agent: { max_turns: 41 } }, schema),
  ).toBe(true);
});

test("a value put back, a field cleared or a switch turned off again is no edit", () => {
  expect(
    settingsFormDirty(base, { ...base, agent: { max_turns: 40 } }, schema),
  ).toBe(false);
  expect(
    settingsFormDirty(
      base,
      {
        ...base,
        gateways: {
          telegram: { token: "", enable: false, rich_messages: true },
        },
      },
      schema,
    ),
  ).toBe(false);
});

test("a switch turned off against its default of on is an edit", () => {
  expect(
    settingsFormDirty(
      base,
      { ...base, gateways: { telegram: { rich_messages: false } } },
      schema,
    ),
  ).toBe(true);
});

test("the revision a document carries is not an edit", () => {
  expect(settingsFormDirty(base, { ...base, revision: "r2" }, schema)).toBe(
    false,
  );
});

test("a header named with an empty value is an edit: an empty value leaves the header out", () => {
  const withHeader = {
    ...base,
    tools: { http_request: { default_headers: { "User-Agent": "" } } },
  };
  expect(settingsFormDirty(base, withHeader, schema)).toBe(true);
  expect(settingsFormDirty(withHeader, base, schema)).toBe(true);
});

// The models of a provider of type coddy: allow_reasoning_off has a third
// position there, "As the remote" (the key left out), which a save writes
// differently from "No".
const coddySchema: JsonSchema = {
  type: "object",
  properties: {
    providers: {
      type: "array",
      items: {
        type: "object",
        properties: { name: { type: "string" }, type: { type: "string" } },
      },
    },
    models: {
      type: "array",
      items: {
        type: "object",
        properties: {
          model: { type: "string" },
          allow_reasoning_off: { type: "boolean", default: false },
        },
      },
    },
  },
};

function rows(type: string, off?: boolean) {
  const model: Record<string, unknown> = { model: "studio/coder" };
  if (off !== undefined) {
    model["allow_reasoning_off"] = off;
  }
  return { providers: [{ name: "studio", type }], models: [model] };
}

test("on a model a coddy provider serves, As the remote and No are two settings", () => {
  expect(
    settingsFormDirty(rows("coddy"), rows("coddy", false), coddySchema),
  ).toBe(true);
  expect(
    settingsFormDirty(rows("coddy", false), rows("coddy"), coddySchema),
  ).toBe(true);
  expect(
    settingsFormDirty(rows("coddy"), rows("coddy", true), coddySchema),
  ).toBe(true);
  expect(
    settingsFormDirty(rows("coddy", false), rows("coddy", false), coddySchema),
  ).toBe(false);
  expect(
    settingsFormDirty(rows("coddy", true), rows("coddy", true), coddySchema),
  ).toBe(false);
});

test("on any other model a switch turned off is no edit", () => {
  expect(
    settingsFormDirty(rows("openai"), rows("openai", false), coddySchema),
  ).toBe(false);
  expect(
    settingsFormDirty(rows("openai"), rows("openai", true), coddySchema),
  ).toBe(true);
});
