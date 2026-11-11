import { fireEvent, render, screen } from "@testing-library/react";
import { expect, test, vi } from "vitest";
import { SecretInput } from "./SecretInput";

test("masks a secret and toggles its visibility accessibly", () => {
  render(
    <SecretInput aria-label="Password" value="secret" onChange={vi.fn()} />,
  );
  const input = screen.getByLabelText("Password") as HTMLInputElement;
  expect(input.type).toBe("password");
  fireEvent.click(screen.getByRole("button", { name: "Show password" }));
  expect(input.type).toBe("text");
  fireEvent.click(screen.getByRole("button", { name: "Hide password" }));
  expect(input.type).toBe("password");
});
