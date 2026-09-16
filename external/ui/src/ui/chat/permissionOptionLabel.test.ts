import { afterEach, expect, test } from "vitest";

import { setLocale } from "../i18n/i18n";
import { permissionOptionLabel } from "./permissionOptionLabel";

afterEach(() => {
  setLocale("en");
});

test("an http_request grant button names the address or the origin it covers", () => {
  const url = {
    optionId: "allow_always_url",
    name: "Always allow https://api.x.dev/v1/items",
    kind: "allow_always",
  };
  const origin = {
    optionId: "allow_always_origin",
    name: "Always allow https://api.x.dev",
    kind: "allow_always",
  };
  expect(permissionOptionLabel(url)).toBe(
    "Always allow https://api.x.dev/v1/items",
  );
  setLocale("ru");
  expect(permissionOptionLabel(url)).toBe(
    "Всегда разрешать https://api.x.dev/v1/items",
  );
  expect(permissionOptionLabel(origin)).toBe(
    "Всегда разрешать https://api.x.dev",
  );
});

test("an unrecognised grant name falls back to the backend's own text", () => {
  setLocale("ru");
  expect(
    permissionOptionLabel({
      optionId: "allow_always_origin",
      name: "Allow everything",
      kind: "allow_always",
    }),
  ).toBe("Allow everything");
});
