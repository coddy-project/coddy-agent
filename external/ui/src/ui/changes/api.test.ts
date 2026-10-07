import { afterEach, expect, test, vi } from "vitest";
import { discardSessionChanges, fetchSessionChanges } from "./api";

afterEach(() => vi.unstubAllGlobals());

test("an unreadable successful response becomes a recoverable error", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => ({
      ok: true,
      status: 200,
      json: async () => {
        throw new SyntaxError("truncated JSON");
      },
    })),
  );
  await expect(fetchSessionChanges("s1")).resolves.toMatchObject({
    ok: false,
    status: 200,
  });
  await expect(discardSessionChanges("s1", { all: true })).resolves.toMatchObject({
    ok: false,
    status: 200,
  });
});
