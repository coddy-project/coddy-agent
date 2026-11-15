import { describe, expect, test } from "vitest";
import {
  selectMessageRenderer,
  validateMessageRenderer,
  type MessageRenderer,
} from "./messageRenderers";
import type { Owned } from "./contributions";
import type { ExtensionAPI } from "./runtime";

const row = (
  owner: string,
  id: string,
  match: string | string[],
): Owned<MessageRenderer> =>
  ({
    id,
    match,
    mount: () => {},
    key: `${owner}/${id}`,
    owner,
    api: { api: 1 } as ExtensionAPI,
  }) as Owned<MessageRenderer>;

describe("message renderer selection", () => {
  const rows = [
    row("a", "all", "*"),
    row("a", "frames", "ext_frame"),
    row("b", "named", "ext_frame:usage"),
    row("b", "host-type", "compaction"),
    row("c", "multi", ["ext_frame:usage", "compaction"]),
  ];

  test("an ext_frame row prefers the named event, then ext_frame, then *", () => {
    expect(selectMessageRenderer(rows, "ext_frame", "usage")?.key).toBe(
      "b/named",
    );
    expect(selectMessageRenderer(rows, "ext_frame", "other")?.key).toBe(
      "a/frames",
    );
  });

  test("* never takes a host row; an explicit type may", () => {
    expect(selectMessageRenderer(rows, "tool_call", "")).toBeUndefined();
    expect(selectMessageRenderer(rows, "compaction", "")?.key).toBe(
      "b/host-type",
    );
  });

  test("ties fall to owner id then registration order", () => {
    const tied = [row("b", "two", "*"), row("a", "one", "*")];
    expect(selectMessageRenderer(tied, "ext_frame", "x")?.key).toBe("a/one");
  });

  test("validation rejects a renderer without matches or mount", () => {
    expect(() =>
      validateMessageRenderer({ id: "x", match: [], mount: () => {} }),
    ).toThrow(/Invalid message renderer/);
    expect(() =>
      validateMessageRenderer({
        id: "x",
        match: "a",
        mount: undefined as never,
      }),
    ).toThrow(/Invalid message renderer/);
  });
});
