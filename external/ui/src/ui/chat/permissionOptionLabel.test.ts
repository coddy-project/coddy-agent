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

test("the session-wide switches of #292 are translated", () => {
  const bypass = {
    optionId: "allow_session_bypass",
    name: "Bypass permissions for this session",
    kind: "allow_always",
  };
  const edits = {
    optionId: "allow_session_accept_edits",
    name: "Allow edits for this session",
    kind: "allow_always",
  };
  expect(permissionOptionLabel(bypass)).toBe("Bypass permissions for this session");
  expect(permissionOptionLabel(edits)).toBe("Allow edits for this session");
  setLocale("ru");
  expect(permissionOptionLabel(bypass)).toBe("Без вопросов до конца сессии");
  expect(permissionOptionLabel(edits)).toBe("Правки без вопросов до конца сессии");
});
