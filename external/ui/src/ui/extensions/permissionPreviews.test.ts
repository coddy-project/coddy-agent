import { describe, expect, test } from "vitest";
import {
  selectPermissionPreview,
  validatePermissionPreview,
  type PermissionPreview,
} from "./permissionPreviews";
import type { Owned } from "./contributions";
import type { ExtensionAPI } from "./runtime";

const row = (
  owner: string,
  id: string,
  tool?: string,
): Owned<PermissionPreview> =>
  ({
    id,
    ...(tool !== undefined ? { tool } : {}),
    mount: () => {},
    key: `${owner}/${id}`,
    owner,
    api: { api: 1 } as ExtensionAPI,
  }) as Owned<PermissionPreview>;

describe("permission previews", () => {
  test("a named tool beats *, which beats an unqualified preview", () => {
    const rows = [
      row("a", "any"),
      row("a", "star", "*"),
      row("b", "named", "demo__status"),
    ];
    expect(selectPermissionPreview(rows, ["demo__status"])?.key).toBe(
      "b/named",
    );
    expect(selectPermissionPreview(rows, ["other"])?.key).toBe("a/star");
  });

  test("validation refuses a preview without mount", () => {
    expect(() =>
      validatePermissionPreview({ id: "x", mount: undefined as never }),
    ).toThrow(/Invalid permission preview/);
    expect(() =>
      validatePermissionPreview({ id: "x", tool: "", mount: () => {} }),
    ).toThrow(/Invalid permission preview/);
    expect(validatePermissionPreview({ id: "x", mount: () => {} })).toBe(
      undefined,
    );
  });
});
