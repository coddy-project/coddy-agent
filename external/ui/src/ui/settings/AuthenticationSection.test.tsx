import { useState } from "react";
import { fireEvent, render, screen } from "@testing-library/react";
import { expect, test } from "vitest";
import { AuthenticationSection } from "./AuthenticationSection";

function Form() {
  const [value, setValue] = useState<Record<string, unknown>>({
    login: { enable: true, user: "operator" },
  });
  return (
    <>
      <AuthenticationSection value={value} onChange={setValue} />
      <output data-testid="document">{JSON.stringify(value)}</output>
    </>
  );
}

test("edits account settings without replacing the stored password with an empty value", () => {
  render(<Form />);
  expect(screen.getByText("Account")).toBeTruthy();
  fireEvent.click(
    screen.getByRole("switch", { name: "Enable authentication" }),
  );
  expect(screen.getByTestId("document").textContent).toContain(
    '"enable":false',
  );
  fireEvent.click(
    screen.getByRole("switch", { name: "Enable authentication" }),
  );
  fireEvent.change(screen.getByLabelText("Login"), {
    target: { value: "alice" },
  });
  expect(screen.getByTestId("document").textContent).toContain(
    '"user":"alice"',
  );
  const password = screen.getByLabelText("Password") as HTMLInputElement;
  fireEvent.click(screen.getByRole("button", { name: "Show password" }));
  expect(password.type).toBe("text");
  fireEvent.change(password, { target: { value: "new password" } });
  fireEvent.change(password, { target: { value: "" } });
  expect(
    JSON.parse(screen.getByTestId("document").textContent!).login.password ??
      "",
  ).toBe("");
});
