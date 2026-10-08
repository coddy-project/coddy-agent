import React from "react";
import { afterEach, expect, test } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { TriStateField } from "./TriStateField";
import { setLocale } from "../i18n/i18n";

afterEach(() => {
  cleanup();
  setLocale("en");
});

// The key reaches the server through JSON.stringify, so the harness reports it
// that way: "Remote" is a key that is absent, which `undefined` alone would
// not show.
function Harness(props: { initial?: boolean | undefined; seen?: unknown[] }) {
  const [row, setRow] = React.useState<Record<string, unknown>>(
    props.initial === undefined ? {} : { multimodal: props.initial },
  );
  return (
    <>
      <output data-testid="row-json">{JSON.stringify(row)}</output>
      <TriStateField
        name="multimodal"
        value={row["multimodal"]}
        onChange={(v) => {
          props.seen?.push(v);
          setRow((r) => ({ ...r, multimodal: v }));
        }}
        label="Multimodal"
        description="Whether the model takes images."
      />
    </>
  );
}

function saved(): Record<string, unknown> {
  return JSON.parse(screen.getByTestId("row-json").textContent || "{}");
}

function option(id: "remote" | "yes" | "no"): HTMLInputElement {
  return screen.getByTestId(`tristate-multimodal-${id}`) as HTMLInputElement;
}

test("an absent key reads Remote and says the remote's listing decides", () => {
  render(<Harness />);
  expect(option("remote").checked).toBe(true);
  expect(option("yes").checked).toBe(false);
  expect(option("no").checked).toBe(false);
  expect(screen.getByTestId("tristate-multimodal-hint").textContent).toBe(
    "Follows the remote's listing.",
  );
});

test("the three positions are one radio group named after the field", () => {
  render(<Harness />);
  const group = screen.getByRole("radiogroup", { name: "Multimodal" });
  const radios = group.querySelectorAll('input[type="radio"]');
  expect(radios.length).toBe(3);
  expect(
    new Set([...radios].map((r) => (r as HTMLInputElement).name)).size,
  ).toBe(1);
  expect(
    [...group.querySelectorAll("label")].map((l) => l.textContent),
  ).toEqual(["Remote", "Yes", "No"]);
});

test("Yes writes true, No writes false, Remote removes the key", () => {
  const seen: unknown[] = [];
  render(<Harness seen={seen} />);
  fireEvent.click(option("yes"));
  expect(saved()).toEqual({ multimodal: true });
  expect(option("yes").checked).toBe(true);
  fireEvent.click(option("no"));
  // An explicit false is a value: it is written, not dropped.
  expect(saved()).toEqual({ multimodal: false });
  expect(option("no").checked).toBe(true);
  fireEvent.click(option("remote"));
  expect(saved()).toEqual({});
  expect(option("remote").checked).toBe(true);
  expect(seen).toEqual([true, false, undefined]);
});

test("a written value shows its position and a hint that the listing is ignored for it", () => {
  const { unmount } = render(<Harness initial={true} />);
  expect(option("yes").checked).toBe(true);
  expect(screen.getByTestId("tristate-multimodal-hint").textContent).toContain(
    "listing is ignored",
  );
  unmount();
  render(<Harness initial={false} />);
  // A stored false (phase 1 saved one on every row) reads No, visibly pinned.
  expect(option("no").checked).toBe(true);
  expect(option("remote").checked).toBe(false);
  expect(screen.getByTestId("tristate-multimodal-hint").textContent).toContain(
    "Choose Remote",
  );
});

test("null reads as an absent key", () => {
  render(
    <TriStateField
      name="multimodal"
      value={null}
      onChange={() => {}}
      label="Multimodal"
    />,
  );
  expect(option("remote").checked).toBe(true);
});

test("the options read in Russian", () => {
  setLocale("ru");
  render(<Harness initial={false} />);
  const labels = [
    ...screen
      .getByRole("radiogroup", { name: "Multimodal" })
      .querySelectorAll("label"),
  ].map((l) => l.textContent);
  expect(labels).toEqual(["Как у удалённого", "Да", "Нет"]);
  expect(screen.getByTestId("tristate-multimodal-hint").textContent).toContain(
    "игнорируется",
  );
});

test("the description opens from the (i) beside the name", () => {
  render(<Harness />);
  expect(screen.getByRole("button", { name: /multimodal/i })).toBeTruthy();
});
