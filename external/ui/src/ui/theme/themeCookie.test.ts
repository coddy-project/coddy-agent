import { expect, test } from "vitest";
import {
  CODDY_UI_THEME_COOKIE,
  readUiThemeCookie,
  writeUiThemeCookie,
} from "./themeCookie";

test("write then read ui theme cookie", () => {
  document.cookie = `${CODDY_UI_THEME_COOKIE}=; Max-Age=0; Path=/`;
  Object.defineProperty(window, "location", {
    value: new URL("http://127.0.0.1:5173/"),
    configurable: true,
  });
  writeUiThemeCookie("light");
  expect(readUiThemeCookie()).toBe("light");
  writeUiThemeCookie("dark");
  expect(readUiThemeCookie()).toBe("dark");
});

test("invalid cookie value is ignored", () => {
  document.cookie = `${CODDY_UI_THEME_COOKIE}=sepia; Path=/`;
  expect(readUiThemeCookie()).toBeNull();
});
