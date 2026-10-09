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
