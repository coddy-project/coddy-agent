import React from "react";
import { afterEach, expect, test } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { Combobox } from "./Combobox";

afterEach(cleanup);

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
