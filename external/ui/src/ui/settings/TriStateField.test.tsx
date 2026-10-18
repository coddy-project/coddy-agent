import React from "react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { I18nProvider } from "../i18n/I18nProvider";
import { initLocale } from "../i18n/i18n";
import { TriStateField } from "./TriStateField";

beforeEach(() => initLocale("en"));

afterEach(() => {
  cleanup();
  initLocale("en");
  vi.restoreAllMocks();
});

function draw(value: unknown, onChange = vi.fn()) {
  render(
    <I18nProvider>
      <TriStateField
        name="allow_reasoning_off"
        value={value}
        onChange={onChange}
        label="Allow disabling reasoning"
      />
    </I18nProvider>,
  );
  return onChange;
}

function radio(position: "remote" | "yes" | "no"): HTMLInputElement {
  return screen.getByTestId(
    `tristate-allow_reasoning_off-${position}`,
  ) as HTMLInputElement;
}

function checked(): string[] {
  return (["remote", "yes", "no"] as const).filter((p) => radio(p).checked);
}

function hint(): string {
  return screen.getByTestId("tristate-allow_reasoning_off-hint").textContent!;
}

// The harness keeps the value the field writes, as the settings document does,
// and reports whether the key is still in the row.
function Harness() {
  const [row, setRow] = React.useState<Record<string, unknown>>({
    model: "terra/qwen",
  });
  return (
    <I18nProvider>
      <TriStateField
        name="allow_reasoning_off"
        value={row["allow_reasoning_off"]}
        onChange={(v) => setRow((r) => ({ ...r, allow_reasoning_off: v }))}
        label="Allow disabling reasoning"
      />
      <output data-testid="row-json">{JSON.stringify(row)}</output>
    </I18nProvider>
  );
}

test("the field is a group of three radios named by its label", () => {
  draw(undefined);

  const group = screen.getByRole("radiogroup", {
    name: "Allow disabling reasoning",
  });
  const radios = [...group.querySelectorAll('input[type="radio"]')];
  expect(radios).toEqual([radio("remote"), radio("yes"), radio("no")]);
  expect(radios.map((r) => r.closest("label")?.textContent)).toEqual([
    "Remote",
    "Yes",
    "No",
  ]);
  // One group: the three share a name, so the browser lets one be checked.
  const names = new Set(radios.map((r) => (r as HTMLInputElement).name));
  expect(names.size).toBe(1);
});

test("an absent or null key is shown at Remote, true at Yes and false at No", () => {
  for (const [value, position] of [
    [undefined, "remote"],
    [null, "remote"],
    [true, "yes"],
    [false, "no"],
  ] as const) {
    draw(value);
    expect(checked(), String(value)).toEqual([position]);
    cleanup();
  }
});

test("picking a position writes undefined, true or false", () => {
  const onChange = draw(undefined);

  fireEvent.click(radio("yes"));
  expect(onChange).toHaveBeenLastCalledWith(true);
  fireEvent.click(radio("no"));
  expect(onChange).toHaveBeenLastCalledWith(false);
  cleanup();

  const fromYes = draw(true);
  fireEvent.click(radio("remote"));
  expect(fromYes).toHaveBeenCalledTimes(1);
  expect(fromYes).toHaveBeenLastCalledWith(undefined);
});

test("going back to Remote takes the key out of the row", () => {
  render(<Harness />);

  fireEvent.click(radio("no"));
  expect(JSON.parse(screen.getByTestId("row-json").textContent!)).toEqual({
    model: "terra/qwen",
    allow_reasoning_off: false,
  });
  expect(checked()).toEqual(["no"]);

  fireEvent.click(radio("remote"));
  expect(JSON.parse(screen.getByTestId("row-json").textContent!)).toEqual({
    model: "terra/qwen",
  });
  expect(checked()).toEqual(["remote"]);
});

test("the hint says the remote decides at Remote and that it is ignored otherwise", () => {
  draw(undefined);
  expect(hint()).toBe("Follows the remote's listing.");
  cleanup();

  for (const value of [true, false]) {
    draw(value);
    expect(hint(), String(value)).toBe(
      "The remote's listing is ignored for this key. Choose Remote to follow it again.",
    );
    cleanup();
  }
});
