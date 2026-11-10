import { useState } from "react";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { ConfirmProvider } from "../components/useConfirm";
import { AuthenticationSection } from "./AuthenticationSection";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

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

test("enrolls an authenticator and shows backup codes only after verification", async () => {
  const fetchMock = vi.fn(async (url: string) => ({
    ok: true,
    json: async () =>
      url.endsWith("/setup")
        ? {
            secret: "JBSWY3DPEHPK3PXP",
            uri: "otpauth://totp/test",
            qr_png: "data:image/png;base64,YQ==",
          }
        : { codes: ["FIRST-CODE"] },
  }));
  vi.stubGlobal("fetch", fetchMock);
  render(
    <ConfirmProvider>
      <Form />
    </ConfirmProvider>,
  );
  fireEvent.click(
    screen.getByRole("switch", { name: "Require one-time code" }),
  );
  expect(
    await screen.findByRole("img", { name: "Authenticator QR code" }),
  ).toBeTruthy();
  expect(screen.queryByText("FIRST-CODE")).toBeNull();
  fireEvent.change(screen.getByLabelText("Code from the app"), {
    target: { value: "123456" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Verify and enable" }));
  expect(await screen.findByText("FIRST-CODE")).toBeTruthy();
  expect(fetchMock).toHaveBeenCalledWith(
    "/coddy/auth/totp/enable",
    expect.objectContaining({ method: "POST" }),
  );
});

test("a rejected verification leaves TOTP unconfigured", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string) =>
      url.endsWith("/setup")
        ? {
            ok: true,
            json: async () => ({
              secret: "JBSWY3DPEHPK3PXP",
              qr_png: "data:image/png;base64,YQ==",
            }),
          }
        : { ok: false },
    ),
  );
  render(<Form />);
  fireEvent.click(
    screen.getByRole("switch", { name: "Require one-time code" }),
  );
  await screen.findByRole("img", { name: "Authenticator QR code" });
  fireEvent.change(screen.getByLabelText("Code from the app"), {
    target: { value: "123456" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Verify and enable" }));
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Could not verify",
  );
  expect(
    JSON.parse(screen.getByTestId("document").textContent!).login
      .totp_configured,
  ).toBeUndefined();
});
