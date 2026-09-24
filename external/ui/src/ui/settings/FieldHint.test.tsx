import { readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
} from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
import { FieldHint, FieldLabel, LegendWithHint } from "./FieldHint";

afterEach(cleanup);

function hint(): HTMLButtonElement {
  return screen.getByTestId("field-hint") as HTMLButtonElement;
}

test("the description is not on the page until the (i) is hovered", () => {
  render(
    <FieldLabel label="Max tokens" description="Upper bound on tokens." />,
  );

  expect(screen.queryByRole("tooltip")).toBeNull();
  expect(document.body.textContent).not.toContain("Upper bound on tokens.");
  expect(hint().getAttribute("aria-label")).toBe("About Max tokens");

  fireEvent.mouseEnter(hint());
  const tip = screen.getByRole("tooltip");
  expect(tip.textContent).toBe("Upper bound on tokens.");
  expect(hint().getAttribute("aria-describedby")).toBe(tip.id);

  fireEvent.mouseLeave(hint());
  expect(screen.queryByRole("tooltip")).toBeNull();
  expect(hint().getAttribute("aria-describedby")).toBeNull();
});

// The tip is portalled to <body>: inside the field it would sit in the
// settings scroll container, be clipped by it or grow a scrollbar for it.
test("the tip is rendered in the body, outside the field", () => {
  const { container } = render(
    <div className="settings-scroll">
      <FieldLabel label="Temperature" description="Sampling temperature." />
    </div>,
  );

  fireEvent.mouseEnter(hint());
  const tip = screen.getByRole("tooltip");
  expect(container.contains(tip)).toBe(false);
  expect(tip.parentElement).toBe(document.body);
});

test("keyboard focus opens the tip and blur closes it", () => {
  render(<FieldLabel label="Proxy URL" description="Optional proxy." />);

  fireEvent.focus(hint());
  expect(screen.getByRole("tooltip").textContent).toBe("Optional proxy.");
  fireEvent.blur(hint());
  expect(screen.queryByRole("tooltip")).toBeNull();
});

test("Escape, a scroll and a tap elsewhere close the tip", () => {
  render(<FieldLabel label="Timeout" description="Per request." />);

  fireEvent.click(hint());
  expect(screen.getByRole("tooltip")).toBeTruthy();
  fireEvent.keyDown(document, { key: "Escape" });
  expect(screen.queryByRole("tooltip")).toBeNull();

  fireEvent.click(hint());
  act(() => {
    document.dispatchEvent(new Event("scroll"));
  });
  expect(screen.queryByRole("tooltip")).toBeNull();

  fireEvent.click(hint());
  fireEvent.mouseDown(document.body);
  expect(screen.queryByRole("tooltip")).toBeNull();
});

// Inside a <label for> or a <legend> the click must stay with the (i).
test("a click on the (i) does not reach the control around it", () => {
  let clicks = 0;
  render(
    <label
      onClick={() => {
        clicks++;
      }}
    >
      Name
      <FieldHint text="Help" label="Name" />
    </label>,
  );

  fireEvent.click(hint());
  expect(clicks).toBe(0);
  expect(screen.getByRole("tooltip").textContent).toBe("Help");
});

test("a field without a description has a bare label and no (i)", () => {
  const { container } = render(<FieldLabel label="Model id" />);

  expect(container.querySelector(".field-hint")).toBeNull();
  expect(container.querySelector(".settings-label")?.textContent).toBe(
    "Model id",
  );
});

test("a fieldset legend carries its description the same way", () => {
  render(
    <fieldset>
      <LegendWithHint label="Models" description="What the provider lists." />
    </fieldset>,
  );

  expect(screen.getByRole("group", { name: /Models/ })).toBeTruthy();
  fireEvent.mouseEnter(hint());
  expect(screen.getByRole("tooltip").textContent).toBe(
    "What the provider lists.",
  );
});

// Placement and layering are CSS jsdom cannot lay out: pin the rules that keep
// the tip over every panel and fixed to the viewport.
test("the tip is fixed, layered over every panel and never takes the pointer", () => {
  const css = readFileSync(
    join(dirname(fileURLToPath(import.meta.url)), "../../styles.css"),
    "utf8",
  );
  const rule = css.match(/^\.field-hint-tip\s*\{([^}]*)\}/m);
  expect(rule).not.toBeNull();
  expect(rule![1]).toMatch(/position:\s*fixed/);
  expect(rule![1]).toMatch(/z-index:\s*1000/);
  expect(rule![1]).toMatch(/pointer-events:\s*none/);
  // Higher than anything else the stylesheet stacks.
  const others = [...css.matchAll(/z-index:\s*(\d+)/g)]
    .map((m) => Number(m[1]))
    .filter((n) => n !== 1000);
  expect(Math.max(...others)).toBeLessThan(1000);
});
