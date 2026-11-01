import React from "react";
import { afterEach, expect, test, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { Combobox } from "./Combobox";
import { OpenRailScreen } from "../nav/railEscape.fakes";

afterEach(cleanup);

test("inline options dismiss after the outside control receives its click", () => {
  const click = vi.fn();
  render(
    <>
      <Combobox
        inlineOptions
        value=""
        onChange={vi.fn()}
        options={[{ value: "model" }]}
        ariaLabel="Model"
      />
      <button onClick={click}>Save</button>
    </>,
  );
  fireEvent.focus(screen.getByRole("combobox"));
  fireEvent.mouseDown(screen.getByRole("button", { name: "Save" }));
  expect(screen.getByRole("listbox")).toBeInTheDocument();
  fireEvent.click(screen.getByRole("button", { name: "Save" }));
  expect(click).toHaveBeenCalledOnce();
  expect(screen.queryByRole("listbox")).toBeNull();
});

function Harness({ initial = "" }: { initial?: string }) {
  const [v, setV] = React.useState(initial);
  return (
    <>
      <Combobox
        value={v}
        onChange={setV}
        options={[{ value: "openai" }, { value: "anthropic" }]}
        ariaLabel="Type"
        testid="cb"
      />
      <span data-testid="val">{v}</span>
    </>
  );
}

test("shows all options on focus and picks one", () => {
  render(<Harness />);
  fireEvent.focus(screen.getByTestId("cb"));
  expect(screen.getByText("anthropic")).toBeTruthy();
  fireEvent.mouseDown(screen.getByText("anthropic"));
  expect(screen.getByTestId("val").textContent).toBe("anthropic");
});

test("typing filters options and keeps the typed text", () => {
  render(<Harness />);
  fireEvent.change(screen.getByTestId("cb"), { target: { value: "anth" } });
  expect(screen.getByText("anthropic")).toBeTruthy();
  expect(screen.queryByText("openai")).toBeNull();
  expect(screen.getByTestId("val").textContent).toBe("anth");
});

test("accepts a free-text value not in the options", () => {
  render(<Harness />);
  fireEvent.change(screen.getByTestId("cb"), { target: { value: "custom-x" } });
  expect(screen.getByTestId("val").textContent).toBe("custom-x");
});

// Escape undoes one step: the list first, the Settings drawer under it next.
test("Escape folds the list and leaves the drawer under it for the next one", () => {
  const closeDrawer = vi.fn();
  render(
    <>
      <OpenRailScreen id="settings" onClose={closeDrawer} />
      <Harness />
    </>,
  );
  const input = screen.getByTestId("cb");
  fireEvent.focus(input);
  expect(screen.getByRole("listbox")).toBeTruthy();
  fireEvent.keyDown(input, { key: "Escape" });
  expect(screen.queryByRole("listbox")).toBeNull();
  expect(closeDrawer).not.toHaveBeenCalled();
  fireEvent.keyDown(input, { key: "Escape" });
  expect(closeDrawer).toHaveBeenCalledTimes(1);
});
