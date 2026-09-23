import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { FieldHint } from "./FieldHint";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

test("the hint button carries its text in the tooltip element", () => {
  render(<FieldHint text={"First line.\n\nSecond paragraph."} />);
  const tip = document.querySelector(".field-hint-tip");
  expect(tip?.textContent).toBe("First line.Second paragraph.");
  // Newlines in the description become separate paragraphs.
  expect(tip?.querySelectorAll(".field-hint-para").length).toBe(2);
  expect(
    screen.getByTestId("field-hint").getAttribute("aria-label"),
  ).toBe("Field help");
});

test("a click does not open anything on a hover-capable device", () => {
  // jsdom's matchMedia stub answers false for the touch-only query, which is
  // the desktop case: the CSS tooltip carries the text and no sheet opens.
  render(<FieldHint text="Helpful words" />);
  fireEvent.click(screen.getByTestId("field-hint"));
  expect(document.querySelector(".field-hint-sheet")).toBeNull();
  expect(document.querySelector(".field-hint-backdrop")).toBeNull();
});
