import { fireEvent, render, screen } from "@testing-library/react";
import { expect, test, vi } from "vitest";
import { Composer } from "./Composer";

test("service tier uses the composer menu and distinguishes Standard from model default", () => {
  const onChange = vi.fn();
  render(
    <Composer
      value=""
      isEmpty
      mode="agent"
      modes={["agent"]}
      onModeChange={() => {}}
      onChange={() => {}}
      onSend={() => {}}
      serviceTierControl={{
        value: "priority",
        choices: ["default", "priority", "ultrafast"],
        onChange,
      }}
    />,
  );
  const picker = screen.getByRole("button", { name: "Service tier" });
  fireEvent.click(picker);
  expect(screen.getByRole("menu")).toHaveClass("mode-menu");
  fireEvent.click(screen.getByRole("menuitem", { name: "Standard" }));
  expect(onChange).toHaveBeenLastCalledWith("default");
  fireEvent.click(picker);
  fireEvent.click(screen.getByRole("menuitem", { name: "Model default" }));
  expect(onChange).toHaveBeenLastCalledWith("");
});
