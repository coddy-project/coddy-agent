import React from "react";
import { afterEach, expect, test } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { CODDY_UI_THEME_COOKIE } from "./themeCookie";
import { ThemeToggle } from "./ThemeToggle";

afterEach(() => {
  cleanup();
  document.cookie = `${CODDY_UI_THEME_COOKIE}=; Max-Age=0; Path=/`;
  document.documentElement.dataset.theme = "dark";
  document.documentElement.style.colorScheme = "dark";
});

test("theme toggle switches mode and cookie", () => {
  Object.defineProperty(window, "location", {
    value: new URL("http://127.0.0.1:5173/"),
    configurable: true,
  });
  document.documentElement.dataset.theme = "dark";

  render(<ThemeToggle />);

  fireEvent.click(screen.getByTestId("theme-toggle-light"));
  expect(document.documentElement.dataset.theme).toBe("light");
  expect(document.cookie).toContain(`${CODDY_UI_THEME_COOKIE}=light`);

  fireEvent.click(screen.getByTestId("theme-toggle-dark"));
  expect(document.documentElement.dataset.theme).toBe("dark");
  expect(document.cookie).toContain(`${CODDY_UI_THEME_COOKIE}=dark`);
});
