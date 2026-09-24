import { describe, expect, test } from "vitest";

import { INDENT_JSON_MAX_CHARS, indentJson, parseJsonSource } from "./jsonSource";

describe("parseJsonSource", () => {
  test("every node carries the text it was read from", () => {
    const node = parseJsonSource(
      ' {"id":12345678901234567891,"name":"caf\\u00e9","tags":["a",9007199254740993],"x":null} ',
    );
    expect(node?.kind).toBe("object");
    if (node?.kind !== "object") return;
    expect(node.source).toBe(
      '{"id":12345678901234567891,"name":"caf\\u00e9","tags":["a",9007199254740993],"x":null}',
    );
    expect(node.entries.map((e) => e.key)).toEqual(["id", "name", "tags", "x"]);
    expect(node.entries[0]!.value).toEqual({ kind: "literal", source: "12345678901234567891" });
    // A string node knows both the literal and the string it encodes.
    expect(node.entries[1]!.value).toEqual({
      kind: "string",
      source: '"caf\\u00e9"',
      value: "café",
    });
    const tags = node.entries[2]!.value;
    expect(tags.kind).toBe("array");
    if (tags.kind !== "array") return;
    expect(tags.items.map((n) => n.source)).toEqual(['"a"', "9007199254740993"]);
    expect(node.entries[3]!.value).toEqual({ kind: "literal", source: "null" });
  });

  test("a repeated key is an entry of its own", () => {
    const node = parseJsonSource('{"k":1,"k":2}');
    expect(node?.kind === "object" && node.entries.map((e) => [e.key, e.value.source])).toEqual([
      ["k", "1"],
      ["k", "2"],
    ]);
  });

  test("keys are the strings they encode", () => {
    const node = parseJsonSource('{"caf\\u00e9":"\\"q\\""}');
    expect(node?.kind === "object" && node.entries[0]).toEqual({
      key: "café",
      value: { kind: "string", source: '"\\"q\\""', value: '"q"' },
    });
  });

  test("scalars at the top are nodes too, and text that is not JSON is nothing", () => {
    expect(parseJsonSource(" 42 ")).toEqual({ kind: "literal", source: "42" });
    expect(parseJsonSource('"s"')).toEqual({ kind: "string", source: '"s"', value: "s" });
    for (const text of ["", "   ", '{"a":', "{not json}", "[link](https://example.com)", "1 2"]) {
      expect(parseJsonSource(text)).toBeUndefined();
    }
  });

  test("empty containers and whitespace between tokens", () => {
    const node = parseJsonSource('{ "a" : [ ] , "b" : { } }');
    expect(node?.kind === "object" && node.entries.map((e) => [e.value.kind, e.value.source])).toEqual([
      ["array", "[ ]"],
      ["object", "{ }"],
    ]);
  });
});

describe("indentJson", () => {
  test("lays a document out the way JSON.stringify(value, null, 2) does", () => {
    const value = {
      total_count: 2,
      incomplete_results: false,
      items: [{ id: 1, topics: ["a", "b"], owner: null }, { id: 2, topics: [], meta: {} }],
      note: "a, b: {c} [d]",
    };
    expect(indentJson(JSON.stringify(value))).toBe(JSON.stringify(value, null, 2));
    const list = [1, "two", { three: 3 }];
    expect(indentJson(JSON.stringify(list))).toBe(JSON.stringify(list, null, 2));
  });

  test("keeps every literal as the server wrote it", () => {
    // Parsed and printed again, the id would round to 12345678901234567000, the
    // escape would turn into the letter and the repeated key would fold into one.
    const raw = '{"id":12345678901234567891,"name":"caf\\u00e9","k":1,"k":2,"q":"say \\"hi\\""}';
    expect(indentJson(raw)).toBe(
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
    expect(indentJson('  {\n "a" :  "x  y" }\n')).toBe('{\n  "a": "x  y"\n}');
  });

  test("a document past the cap comes back as it came", () => {
    const pad = "x".repeat(INDENT_JSON_MAX_CHARS);
    const big = JSON.stringify({ pad });
    expect(indentJson(big)).toBe(big);
    const fits = JSON.stringify({ pad: pad.slice(0, INDENT_JSON_MAX_CHARS - 20) });
    expect(indentJson(fits)).toBe(JSON.stringify(JSON.parse(fits), null, 2));
  });

  test("anything that is not JSON comes back unchanged", () => {
    for (const text of ['{"total_count": 2, "items": [{"id": 1', "{not json}", "plain text", ""]) {
      expect(indentJson(text)).toBe(text);
    }
  });
});
