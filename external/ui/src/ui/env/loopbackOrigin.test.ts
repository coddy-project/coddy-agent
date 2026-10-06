import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { describe, expect, it } from "vitest";
import { isLoopbackOrigin } from "./loopbackOrigin";

// The cases live in internal/config/testdata/loopback_origin_cases.json, which
// the server's test (internal/config/config_test.go) reads too: the server and
// this hint are held to the same literals, so the hint names the toggle exactly
// where the server would honour it. Change both or neither.
type OriginCase = { origin: string; loopback: boolean; note?: string };

const cases = JSON.parse(
  readFileSync(
    join(
      dirname(fileURLToPath(import.meta.url)),
      "../../../../../internal/config/testdata/loopback_origin_cases.json",
    ),
    "utf8",
  ),
) as OriginCase[];

describe("isLoopbackOrigin", () => {
  it("reads the shared cases", () => {
    expect(cases.length).toBeGreaterThanOrEqual(40);
  });

  it.each(cases.filter((c) => c.loopback))("admits $origin", ({ origin }) => {
    expect(isLoopbackOrigin(origin)).toBe(true);
  });

  it.each(cases.filter((c) => !c.loopback))("refuses $origin", ({ origin }) => {
    expect(isLoopbackOrigin(origin)).toBe(false);
  });
});
