import React from "react";
import { afterEach, expect, test } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { ModelPicker } from "./ModelPicker";

afterEach(cleanup);

function Harness() {
  const [v, setV] = React.useState("");
  return (
    <>
      <ModelPicker
        value={v}
        onChange={setV}
        models={["openai/gpt-4o", "anthropic/claude"]}
      />
      <span data-testid="val">{v}</span>
    </>
  );
}

test("lists configured models and picking one updates the value", async () => {
  render(<Harness />);
  fireEvent.focus(screen.getByTestId("model-picker-input"));
  const opt = await screen.findByText("anthropic/claude");
  fireEvent.mouseDown(opt);
  expect(screen.getByTestId("val").textContent).toBe("anthropic/claude");
});

test("manual entry updates the value", () => {
  render(<Harness />);
  fireEvent.change(screen.getByTestId("model-picker-input"), {
    target: { value: "custom/model-x" },
  });
  expect(screen.getByTestId("val").textContent).toBe("custom/model-x");
});
