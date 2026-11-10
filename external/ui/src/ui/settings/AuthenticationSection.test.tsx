import { useState } from "react";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
} from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { ConfirmProvider } from "../components/useConfirm";
import { AuthenticationSection } from "./AuthenticationSection";
import { resetEnrollment } from "./authEnrollmentStore";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  resetEnrollment();
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

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((r) => {
    resolve = r;
  });
  return { promise, resolve };
}

function NavigableForm() {
  const [value, setValue] = useState<Record<string, unknown>>({
    login: { enable: true, user: "operator" },
  });
  const [visible, setVisible] = useState(true);
  return (
    <>
      <button onClick={() => setVisible(!visible)}>Navigate</button>
      {visible && <AuthenticationSection value={value} onChange={setValue} />}
      <output data-testid="document">{JSON.stringify(value)}</output>
    </>
  );
}

async function startDeferredVerification() {
  const pending = deferred<{
    ok: boolean;
    json: () => Promise<{ codes: string[] }>;
  }>();
  vi.stubGlobal(
    "fetch",
    vi.fn((url: string) =>
      url.endsWith("setup")
        ? Promise.resolve({
            ok: true,
            json: async () => ({
              secret: "JBSWY3DPEHPK3PXP",
              qr_png: "data:image/png;base64,YQ==",
            }),
          })
        : pending.promise,
    ),
  );
  fireEvent.click(
    screen.getByRole("switch", { name: "Require one-time code" }),
  );
  await screen.findByRole("img", { name: "Authenticator QR code" });
  fireEvent.change(screen.getByLabelText("Code from the app"), {
    target: { value: "123456" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Verify and enable" }));
  return pending;
}

test("preserves codes when verification completes while the section is unmounted", async () => {
  render(<NavigableForm />);
  const pending = await startDeferredVerification();
  fireEvent.click(screen.getByText("Navigate"));
  pending.resolve({
    ok: true,
    json: async () => ({ codes: ["SURVIVING-CODE"] }),
  });
  await act(async () => {
    await pending.promise;
  });
  fireEvent.click(screen.getByText("Navigate"));
  expect(await screen.findByText("SURVIVING-CODE")).toBeTruthy();
});

test("verification merges metadata with account edits made in flight", async () => {
  render(<Form />);
  const pending = await startDeferredVerification();
  fireEvent.change(screen.getByLabelText("Login"), {
    target: { value: "new-user" },
  });
  fireEvent.change(screen.getByLabelText("Password"), {
    target: { value: "new-password" },
  });
  fireEvent.change(screen.getByLabelText("Session duration (hours)"), {
    target: { value: "24" },
  });
  fireEvent.click(
    screen.getByRole("switch", { name: "Enable authentication" }),
  );
  pending.resolve({ ok: true, json: async () => ({ codes: ["CODE"] }) });
  await screen.findByText("CODE");
  expect(
    JSON.parse(screen.getByTestId("document").textContent!).login,
  ).toMatchObject({
    user: "new-user",
    password: "new-password",
    session_ttl_hours: 24,
    enable: false,
    totp_configured: true,
  });
});

test("locks secret and code during verification", async () => {
  render(<Form />);
  const pending = await startDeferredVerification();
  expect(screen.getByLabelText("Secret")).toBeDisabled();
  expect(screen.getByLabelText("Code from the app")).toBeDisabled();
  pending.resolve({ ok: false, json: async () => ({ codes: [] }) });
  await screen.findByRole("alert");
});

test("an older setup response cannot replace the secret being verified", async () => {
  const old = deferred<{
    ok: boolean;
    json: () => Promise<{ secret: string; qr_png: string }>;
  }>();
  const verify = deferred<{
    ok: boolean;
    json: () => Promise<{ codes: string[] }>;
  }>();
  const fetchMock = vi.fn((url: string, init?: RequestInit) => {
    if (url.endsWith("enable")) return verify.promise;
    const input = JSON.parse(String(init?.body)) as { secret?: string };
    if (input.secret === "AAAA") return old.promise;
    return Promise.resolve({
      ok: true,
      json: async () => ({
        secret: input.secret ?? "JBSWY3DPEHPK3PXP",
        qr_png: "data:image/png;base64,YQ==",
      }),
    });
  });
  vi.stubGlobal("fetch", fetchMock);
  render(<Form />);
  fireEvent.click(
    screen.getByRole("switch", { name: "Require one-time code" }),
  );
  await screen.findByRole("img", { name: "Authenticator QR code" });
  fireEvent.change(screen.getByLabelText("Secret"), {
    target: { value: "AAAA" },
  });
  fireEvent.change(screen.getByLabelText("Secret"), {
    target: { value: "BBBB" },
  });
  await screen.findByRole("img", { name: "Authenticator QR code" });
  fireEvent.change(screen.getByLabelText("Code from the app"), {
    target: { value: "123456" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Verify and enable" }));
  expect(
    JSON.parse(
      String(
        fetchMock.mock.calls.find(([url]) => url.endsWith("enable"))![1]?.body,
      ),
    ),
  ).toEqual({ secret: "BBBB", code: "123456" });
  old.resolve({
    ok: true,
    json: async () => ({
      secret: "AAAA",
      qr_png: "data:image/png;base64,Yg==",
    }),
  });
  await act(async () => {
    await old.promise;
  });
  expect(screen.getByLabelText("Secret")).toHaveValue("BBBB");
  verify.resolve({ ok: true, json: async () => ({ codes: ["RACE-CODE"] }) });
  expect(await screen.findByText("RACE-CODE")).toBeTruthy();
});
