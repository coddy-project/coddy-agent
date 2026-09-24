import { describe, expect, test } from "vitest";

import { INDENT_JSON_MAX_CHARS, indentJsonDocument } from "./indentJsonDocument";

describe("indentJsonDocument", () => {
  test("lays a document out the way JSON.stringify(value, null, 2) does", () => {
    const value = {
      total_count: 2,
      incomplete_results: false,
      items: [{ id: 1, topics: ["a", "b"], owner: null }, { id: 2, topics: [], meta: {} }],
      note: "a, b: {c} [d]",
    };
    expect(indentJsonDocument(JSON.stringify(value))).toBe(JSON.stringify(value, null, 2));
  });

  test("an array at the top is a document too", () => {
    const value = [1, "two", { three: 3 }];
    expect(indentJsonDocument(JSON.stringify(value))).toBe(JSON.stringify(value, null, 2));
  });

  test("keeps every literal as the server wrote it", () => {
    // Parsed and printed again, the id would round to 12345678901234567000, the
    // escape would turn into the letter and the repeated key would fold into one.
    const raw = '{"id":12345678901234567891,"name":"caf\\u00e9","k":1,"k":2,"q":"say \\"hi\\""}';
    expect(indentJsonDocument(raw)).toBe(
      [
        "{",
        '  "id": 12345678901234567891,',
        '  "name": "caf\\u00e9",',
        '  "k": 1,',
        '  "k": 2,',
        '  "q": "say \\"hi\\""',
        "}",
      ].join("\n"),
    );
  });

  test("whitespace inside strings is kept and whitespace between tokens is not", () => {
    expect(indentJsonDocument('  {\n "a" :  "x  y" }\n')).toBe('{\n  "a": "x  y"\n}');
  });

  test("a document past the cap comes back as it came", () => {
    const pad = "x".repeat(INDENT_JSON_MAX_CHARS);
    const big = JSON.stringify({ pad });
    expect(big.length).toBeGreaterThan(INDENT_JSON_MAX_CHARS);
    expect(indentJsonDocument(big)).toBe(big);
    const fits = JSON.stringify({ pad: pad.slice(0, INDENT_JSON_MAX_CHARS - 20) });
    expect(indentJsonDocument(fits)).toBe(JSON.stringify(JSON.parse(fits), null, 2));
  });

  test("anything that is not a whole object or array comes back unchanged", () => {
    for (const text of [
      '{"total_count": 2, "items": [{"id": 1',
      "[link](https://example.com) and more",
      "{not json}",
      '"just a string"',
      "42",
      "null",
      "",
      "plain text",
    ]) {
      expect(indentJsonDocument(text)).toBe(text);
    }
  });
});
