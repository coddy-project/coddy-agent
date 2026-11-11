import { useState } from "react";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, describe, expect, test, vi } from "vitest";
import { ConfirmProvider } from "../components/useConfirm";
import { AuthenticationSection } from "./AuthenticationSection";
import { resetEnrollment } from "./authEnrollmentStore";
import { setLocale } from "../i18n/i18n";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  resetEnrollment();
});

function Form({ initial }: { initial?: Record<string, unknown> }) {
  const [value, setValue] = useState<Record<string, unknown>>(
    initial ?? {
      login: { enable: true, user: "operator" },
    },
  );
  return (
    <>
      <ConfirmProvider>
        <AuthenticationSection value={value} onChange={setValue} />
      </ConfirmProvider>
      <output data-testid="document">{JSON.stringify(value)}</output>
    </>
  );
}

test("draws searchable authenticator and backup codes landmarks", () => {
  render(
    <Form
      initial={{
        login: {
          enable: true,
          user: "operator",
          totp_configured: true,
          backup_codes_count: 2,
        },
      }}
    />,
  );
  expect(
    screen.getByRole("group", { name: "Authenticator app" }),
  ).toHaveAttribute(
    "data-settings-anchor",
    "landmark:httpserver:authenticator",
  );
  expect(screen.getByRole("group", { name: "Backup codes" })).toHaveAttribute(
    "data-settings-anchor",
    "landmark:httpserver:backupCodes",
  );
});

test("keeps a malformed session duration numeric instead of storing NaN", () => {
  render(<Form />);
  const input = screen.getByLabelText("Session duration (hours)");
  Object.defineProperty(input, "value", {
    configurable: true,
    get: () => "not-a-number",
    set: () => {},
  });
  fireEvent.change(input);
  expect(
    JSON.parse(screen.getByTestId("document").textContent!).login
      .session_ttl_hours,
  ).toBe(0);
});

test("defaults the persistent session duration to 24 hours", () => {
  render(<Form />);
  expect(screen.getByLabelText("Session duration (hours)")).toHaveValue(24);
});

test("puts the environment precedence notice beside the account legend", () => {
  render(<Form initial={{ login_source: "env", login: { enable: true } }} />);
  const hint = screen.getByRole("button", { name: "About Account" });
  expect(
    screen.queryByText(
      "Environment credentials take precedence over these account settings.",
    ),
  ).toBeNull();
  fireEvent.click(hint);
  expect(screen.getByRole("tooltip")).toHaveTextContent(
    "Environment credentials take precedence over these account settings.",
  );
});

test("puts the password help behind its field hint", () => {
  render(<Form />);
  expect(screen.getByRole("button", { name: "About Password" })).toBeTruthy();
});

test.each([
  [
    { login_configured: true, login_source: "config" },
    "Password hash is stored on the server",
  ],
  [
    { login_configured: true, login_source: "env" },
    "Set with CODDY_HTTP_PASSWORD",
  ],
  [{}, "Set with CODDY_HTTP_PASSWORD"],
])("reports the password state without exposing it", (state, placeholder) => {
  render(<Form initial={{ ...state, login: { enable: true } }} />);
  expect(
    screen.getByLabelText("Password").getAttribute("placeholder") ?? "",
  ).toBe(placeholder);
  expect(screen.getByLabelText("Password")).toHaveValue("");
  expect(screen.getByRole("button", { name: "About Password" })).toBeTruthy();
});

test("styles the secret input without inline layout and exposes the eye state", () => {
  render(<Form />);
  const password = screen.getByLabelText("Password");
  expect(password.parentElement).toHaveClass("settings-secret-input");
  expect(password.parentElement).not.toHaveAttribute("style");

  const toggle = screen.getByRole("button", { name: "Show password" });
  expect(toggle).toHaveAttribute("aria-pressed", "false");
  expect(toggle.querySelector("svg")).toHaveAttribute(
    "stroke-linecap",
    "round",
  );
  expect(toggle.querySelector("svg")).toHaveAttribute(
    "stroke-linejoin",
    "round",
  );
  fireEvent.click(toggle);
  expect(screen.getByRole("button", { name: "Hide password" })).toHaveAttribute(
    "aria-pressed",
    "true",
  );
});

test("edits account settings without replacing the stored password with an empty value", () => {
  render(<Form />);
  expect(screen.getByText("Account")).toBeTruthy();
  fireEvent.click(screen.getByRole("switch", { name: "Enabled" }));
  expect(screen.getByTestId("document").textContent).toContain(
    '"enable":false',
  );
  fireEvent.click(screen.getByRole("switch", { name: "Enabled" }));
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
  expect(
    screen
      .getByLabelText("App code")
      .compareDocumentPosition(screen.getByRole("button", { name: "Submit" })) &
      Node.DOCUMENT_POSITION_FOLLOWING,
  ).toBeTruthy();
  const period = screen.getByRole("timer", { name: /New code in/ });
  expect(period).toHaveAttribute("aria-live", "off");
  expect(
    period.compareDocumentPosition(screen.getByLabelText("App code")) &
      Node.DOCUMENT_POSITION_FOLLOWING,
  ).toBeTruthy();
  expect(
    period.compareDocumentPosition(
      screen.getByRole("button", { name: "Submit" }),
    ) & Node.DOCUMENT_POSITION_FOLLOWING,
  ).toBeTruthy();
  fireEvent.change(screen.getByLabelText("App code"), {
    target: { value: "123456" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Submit" }));
  expect(await screen.findByText("FIRST-CODE")).toBeTruthy();
  expect(fetchMock).toHaveBeenCalledWith(
    "/coddy/auth/totp/enable",
    expect.objectContaining({ method: "POST" }),
  );
});

test("keeps a QR placeholder while a refreshed secret is loading", async () => {
  let resolveRefresh!: (value: unknown) => void;
  const refresh = new Promise((resolve) => {
    resolveRefresh = resolve;
  });
  const response = {
    ok: true,
    json: async () => ({
      secret: "JBSWY3DPEHPK3PXP",
      qr_png: "data:image/png;base64,YQ==",
    }),
  };
  vi.stubGlobal(
    "fetch",
    vi.fn().mockResolvedValueOnce(response).mockReturnValueOnce(refresh),
  );
  render(<Form />);
  fireEvent.click(
    screen.getByRole("switch", { name: "Require one-time code" }),
  );
  await screen.findByRole("img", { name: "Authenticator QR code" });
  fireEvent.click(screen.getByRole("button", { name: "Refresh secret key" }));
  expect(
    document.querySelector(".auth-enrollment-qr--placeholder"),
  ).toBeTruthy();
  expect(screen.getByLabelText("App code")).toBeTruthy();
  await act(async () => {
    resolveRefresh(response);
  });
  expect(
    await screen.findByRole("img", { name: "Authenticator QR code" }),
  ).toBeTruthy();
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
  fireEvent.click(screen.getAllByRole("switch")[1]!);
  await screen.findByRole("img", { name: "Authenticator QR code" });
  fireEvent.change(screen.getByLabelText("App code"), {
    target: { value: "123456" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Submit" }));
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
      {visible && (
        <ConfirmProvider>
          <AuthenticationSection value={value} onChange={setValue} />
        </ConfirmProvider>
      )}
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
  fireEvent.change(screen.getByLabelText("App code"), {
    target: { value: "123456" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Submit" }));
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
    target: { value: "48" },
  });
  fireEvent.click(screen.getByRole("switch", { name: "Enabled" }));
  pending.resolve({ ok: true, json: async () => ({ codes: ["CODE"] }) });
  await screen.findByText("CODE");
  expect(
    JSON.parse(screen.getByTestId("document").textContent!).login,
  ).toMatchObject({
    user: "new-user",
    password: "new-password",
    session_ttl_hours: 48,
    enable: false,
    totp_configured: true,
  });
});

test("reopens secret enrollment after TOTP is enabled and uses icon actions", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string) => ({
      ok: true,
      json: async () =>
        url.endsWith("/setup")
          ? {
              secret: "JBSWY3DPEHPK3PXP",
              uri: "otpauth://totp/test",
              qr_png: "data:image/png;base64,YQ==",
            }
          : { codes: ["FIRST-CODE"], backup_codes_generation: "generation-1" },
    })),
  );
  render(<Form />);
  fireEvent.click(
    screen.getByRole("switch", { name: "Require one-time code" }),
  );
  fireEvent.change(await screen.findByLabelText("App code"), {
    target: { value: "123456" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Submit" }));
  await screen.findByText("FIRST-CODE");
  expect(screen.getByText("Login", { selector: "label" })).toHaveClass(
    "settings-label",
  );
  expect(
    screen.getByText("Session duration (hours)").closest("label"),
  ).toBeTruthy();
  expect(
    screen.getByRole("button", { name: "About Session duration (hours)" }),
  ).toBeTruthy();
  for (const name of [
    "Copy backup codes",
    "Download backup codes",
    "Regenerate backup codes",
  ]) {
    const button = screen.getByRole("button", { name });
    expect(button).toHaveAttribute("title", name);
    expect(button).toHaveClass("settings-btn", "auth-backup-button");
    expect(button.textContent).not.toBe("");
  }
  fireEvent.click(screen.getByRole("button", { name: "Edit secret" }));
  const qr = document.querySelector(".auth-enrollment-qr");
  const verify = screen.getByRole("button", { name: "Submit" });
  expect(qr?.parentElement).not.toContain(verify);
  expect(verify.closest(".auth-enrollment-fields")).toBeTruthy();
  const codes = document.querySelector(".auth-enrollment-codes");
  const actions = document.querySelector(".auth-backup-actions");
  expect(
    codes!.compareDocumentPosition(actions!) & Node.DOCUMENT_POSITION_FOLLOWING,
  ).toBeTruthy();
  expect(
    await screen.findByRole("img", { name: "Authenticator QR code" }),
  ).toBeTruthy();
  expect(screen.getByText("Secret", { selector: "label" })).toHaveClass(
    "settings-label",
  );
  expect(screen.getByLabelText("Secret")).toHaveAttribute("readonly");
  const writeText = vi.fn().mockResolvedValue(undefined);
  Object.assign(navigator, { clipboard: { writeText } });
  fireEvent.click(screen.getByRole("button", { name: "Copy secret key" }));
  expect(writeText).toHaveBeenCalledWith("JBSWY3DPEHPK3PXP");
  const secretBox = screen
    .getByLabelText("Secret")
    .closest(".settings-secret-input");
  expect(secretBox).toContain(
    screen.getByRole("button", { name: "Copy secret key" }),
  );
  expect(secretBox).toContain(
    screen.getByRole("button", { name: "Refresh secret key" }),
  );
  const callsBefore = vi.mocked(fetch).mock.calls.length;
  fireEvent.click(screen.getByRole("button", { name: "Refresh secret key" }));
  expect(vi.mocked(fetch).mock.calls.length).toBeGreaterThan(callsBefore);
  expect(vi.mocked(fetch).mock.calls.at(-1)?.[1]?.body).toBe("{}");
  expect(screen.getByRole("button", { name: "Remove TOTP" })).toBeTruthy();
});

test("locks secret and code during verification", async () => {
  render(<Form />);
  const pending = await startDeferredVerification();
  expect(screen.getByLabelText("Secret")).toBeDisabled();
  expect(screen.getByLabelText("App code")).toBeDisabled();
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
  // The second switch is "Require one-time code"; the lookup must not name
  // it in English, since one test runs the section in Russian.
  fireEvent.click(screen.getAllByRole("switch")[1]!);
  await screen.findByRole("img", { name: "Authenticator QR code" });
  fireEvent.change(screen.getByLabelText("Secret"), {
    target: { value: "AAAA" },
  });
  fireEvent.change(screen.getByLabelText("Secret"), {
    target: { value: "BBBB" },
  });
  await screen.findByRole("img", { name: "Authenticator QR code" });
  fireEvent.change(screen.getByLabelText("App code"), {
    target: { value: "123456" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Submit" }));
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

function ConfiguredForm() {
  const [value, setValue] = useState<Record<string, unknown>>({
    login: {
      enable: true,
      user: "operator",
      totp_configured: true,
      backup_codes_configured: true,
      backup_codes_count: 5,
    },
  });
  return (
    <ConfirmProvider>
      <AuthenticationSection value={value} onChange={setValue} />
      <output data-testid="document">{JSON.stringify(value)}</output>
    </ConfirmProvider>
  );
}

test.each([true, false])(
  "disable confirmation handles success=%s",
  async (ok) => {
    const fetchMock = vi.fn(async () => ({
      ok,
      json: async () => ({ qr_png: "data:image/png;base64,YQ==" }),
    }));
    vi.stubGlobal("fetch", fetchMock);
    render(<ConfiguredForm />);
    fireEvent.click(screen.getByRole("button", { name: "Edit secret" }));
    fireEvent.click(screen.getByRole("button", { name: "Remove TOTP" }));
    expect(fetchMock).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByRole("button", { name: "Disable" }));
    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/coddy/auth/totp/disable",
        expect.objectContaining({ method: "POST" }),
      ),
    );
    if (ok)
      await waitFor(() =>
        expect(
          JSON.parse(screen.getByTestId("document").textContent!).login
            .totp_configured,
        ).toBe(false),
      );
    else {
      await screen.findByRole("alert");
      expect(
        JSON.parse(screen.getByTestId("document").textContent!).login
          .totp_configured,
      ).toBe(true);
    }
  },
);

test.each([true, false])(
  "backup regeneration is confirmed and handles success=%s",
  async (ok) => {
    const fetchMock = vi.fn(async () => ({
      ok,
      json: async () => ({ codes: ["NEW-CODE"] }),
    }));
    vi.stubGlobal("fetch", fetchMock);
    render(<ConfiguredForm />);
    const fieldset = screen.getByRole("group", { name: "Backup codes" });
    expect(within(fieldset).getByText(/5 unused/)).toBeTruthy();
    fireEvent.click(
      within(fieldset).getByRole("button", { name: "Regenerate backup codes" }),
    );
    expect(fetchMock).not.toHaveBeenCalled();
    fireEvent.click(screen.getByRole("button", { name: "Regenerate codes" }));
    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/coddy/auth/backup-codes/regenerate",
        expect.objectContaining({ method: "POST" }),
      ),
    );
    if (ok) {
      expect(await screen.findByText("NEW-CODE")).toBeTruthy();
      expect(screen.getByText(/1 unused/)).toBeTruthy();
    } else {
      await screen.findByRole("alert");
      expect(screen.getByText(/5 unused/)).toBeTruthy();
    }
  },
);

test("copies and downloads the displayed codes", async () => {
  const copy = vi.fn().mockResolvedValue(undefined);
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: { writeText: copy },
  });
  const create = vi.fn(() => "blob:codes");
  const revoke = vi.fn();
  vi.stubGlobal("URL", { createObjectURL: create, revokeObjectURL: revoke });
  const click = vi
    .spyOn(HTMLAnchorElement.prototype, "click")
    .mockImplementation(() => {});
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => ({
      ok: true,
      json: async () => ({ codes: ["COPY-CODE"] }),
    })),
  );
  render(<ConfiguredForm />);
  fireEvent.click(
    screen.getByRole("button", { name: "Regenerate backup codes" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Regenerate codes" }));
  await screen.findByText("COPY-CODE");
  fireEvent.click(screen.getByRole("button", { name: "Copy backup codes" }));
  await waitFor(() => expect(copy).toHaveBeenCalledWith("COPY-CODE"));
  fireEvent.click(
    screen.getByRole("button", { name: "Download backup codes" }),
  );
  expect(create).toHaveBeenCalled();
  expect(click).toHaveBeenCalled();
  click.mockRestore();
});

test("failed regeneration preserves the codes already shown", async () => {
  const fetchMock = vi
    .fn()
    .mockResolvedValueOnce({
      ok: true,
      json: async () => ({ codes: ["KEPT-CODE"] }),
    })
    .mockResolvedValueOnce({ ok: false });
  vi.stubGlobal("fetch", fetchMock);
  render(<ConfiguredForm />);
  fireEvent.click(
    screen.getByRole("button", { name: "Regenerate backup codes" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Regenerate codes" }));
  await screen.findByText("KEPT-CODE");
  fireEvent.click(
    screen.getByRole("button", { name: "Regenerate backup codes" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Regenerate codes" }));
  expect(await screen.findByRole("alert")).toHaveTextContent(
    "Could not regenerate",
  );
  expect(screen.getByText("KEPT-CODE")).toBeTruthy();
  expect(screen.getByText(/1 unused/)).toBeTruthy();
});

test("reopening with consumed codes trusts fresh metadata rather than retained results", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => ({
      ok: true,
      json: async () => ({ codes: ["PRIVATE", "OTHER"] }),
    })),
  );
  const first = render(<ConfiguredForm />);
  fireEvent.click(
    screen.getByRole("button", { name: "Regenerate backup codes" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Regenerate codes" }));
  await screen.findByText("PRIVATE");
  first.unmount();
  const onChange = vi.fn();
  render(
    <ConfirmProvider>
      <AuthenticationSection
        value={{
          login: {
            totp_configured: true,
            backup_codes_configured: true,
            backup_codes_count: 1,
          },
        }}
        onChange={onChange}
      />
    </ConfirmProvider>,
  );
  await waitFor(() => expect(screen.queryByText("PRIVATE")).toBeNull());
  expect(onChange).not.toHaveBeenCalled();
});

test("download failure reports an error and revokes its object URL", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => ({
      ok: true,
      json: async () => ({ codes: ["PRIVATE"] }),
    })),
  );
  const create = vi.fn(() => "blob:temporary");
  const revoke = vi.fn();
  vi.stubGlobal("URL", { createObjectURL: create, revokeObjectURL: revoke });
  const click = vi
    .spyOn(HTMLAnchorElement.prototype, "click")
    .mockImplementation(() => {
      throw new Error("download failed");
    });
  render(<ConfiguredForm />);
  fireEvent.click(
    screen.getByRole("button", { name: "Regenerate backup codes" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Regenerate codes" }));
  await screen.findByText("PRIVATE");
  fireEvent.click(
    screen.getByRole("button", { name: "Download backup codes" }),
  );
  expect(await screen.findByRole("alert")).toBeTruthy();
  expect(revoke).toHaveBeenCalledWith("blob:temporary");
  click.mockRestore();
});

test("reopening with a different generation hides codes even when the count is unchanged", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => ({
      ok: true,
      json: async () => ({
        codes: ["OLD-CODE"],
        backup_codes_generation: "old-generation",
      }),
    })),
  );
  const first = render(<ConfiguredForm />);
  fireEvent.click(
    screen.getByRole("button", { name: "Regenerate backup codes" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Regenerate codes" }));
  await screen.findByText("OLD-CODE");
  first.unmount();
  const onChange = vi.fn();
  render(
    <ConfirmProvider>
      <AuthenticationSection
        value={{
          login: {
            totp_configured: true,
            backup_codes_configured: true,
            backup_codes_count: 1,
            backup_codes_generation: "new-generation",
          },
        }}
        onChange={onChange}
      />
    </ConfirmProvider>,
  );
  await waitFor(() => expect(screen.queryByText("OLD-CODE")).toBeNull());
  expect(onChange).not.toHaveBeenCalled();
});

test("reopening after an external disable does not restore stale metadata or codes", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => ({
      ok: true,
      json: async () => ({
        codes: ["PRIVATE"],
        backup_codes_generation: "old-generation",
      }),
    })),
  );
  const first = render(<ConfiguredForm />);
  fireEvent.click(
    screen.getByRole("button", { name: "Regenerate backup codes" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Regenerate codes" }));
  await screen.findByText("PRIVATE");
  first.unmount();
  const onChange = vi.fn();
  render(
    <ConfirmProvider>
      <AuthenticationSection
        value={{
          login: {
            totp_configured: false,
            backup_codes_configured: false,
            backup_codes_count: 0,
            backup_codes_generation: "",
          },
        }}
        onChange={onChange}
      />
    </ConfirmProvider>,
  );
  await waitFor(() => expect(screen.queryByText("PRIVATE")).toBeNull());
  expect(onChange).not.toHaveBeenCalled();
});

test("a pending result cannot overwrite fresher server metadata after navigation", async () => {
  const pending = deferred<{
    ok: boolean;
    json: () => Promise<{ codes: string[]; backup_codes_generation: string }>;
  }>();
  vi.stubGlobal(
    "fetch",
    vi.fn(() => pending.promise),
  );
  const first = render(<ConfiguredForm />);
  fireEvent.click(
    screen.getByRole("button", { name: "Regenerate backup codes" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Regenerate codes" }));
  first.unmount();
  pending.resolve({
    ok: true,
    json: async () => ({
      codes: ["STALE-PRIVATE"],
      backup_codes_generation: "generation-old",
    }),
  });
  await act(async () => {
    await pending.promise;
  });
  const onChange = vi.fn();
  render(
    <ConfirmProvider>
      <AuthenticationSection
        value={{
          login: {
            totp_configured: true,
            backup_codes_configured: true,
            backup_codes_count: 1,
            backup_codes_generation: "generation-new",
          },
        }}
        onChange={onChange}
      />
    </ConfirmProvider>,
  );
  await waitFor(() => expect(screen.queryByText("STALE-PRIVATE")).toBeNull());
  expect(onChange).not.toHaveBeenCalled();
});

const REDUCED_MOTION_QUERY = "(prefers-reduced-motion: reduce)";

function stubReducedMotion(matches: boolean) {
  vi.stubGlobal("matchMedia", (query: string) => ({
    matches: matches && query === REDUCED_MOTION_QUERY,
    media: query,
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  }));
}

function startEnrollment() {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => ({
      ok: true,
      json: async () => ({
        secret: "JBSWY3DPEHPK3PXP",
        qr_png: "data:image/png;base64,YQ==",
      }),
    })),
  );
  render(<Form />);
  // The second switch is "Require one-time code"; the lookup must not name
  // it in English, since one test runs the section in Russian.
  fireEvent.click(screen.getAllByRole("switch")[1]!);
}

function periodFill(): HTMLElement {
  return document.querySelector<HTMLElement>(".auth-period-fill")!;
}

describe("the authenticator code period", () => {
  test("shows the seconds left and shrinks the bar as the wall clock moves", () => {
    vi.useFakeTimers({ now: 125_000 });
    try {
      startEnrollment();
      const period = screen.getByTestId("auth-period");
      expect(period.getAttribute("aria-label")).toContain(
        "New code in 25 seconds",
      );
      const at25 = parseFloat(periodFill().style.width);
      expect(at25).toBeGreaterThan(80);
      expect(at25).toBeLessThanOrEqual(83.4);
      act(() => {
        vi.advanceTimersByTime(1_000);
      });
      expect(period.getAttribute("aria-label")).toContain(
        "New code in 24 seconds",
      );
      expect(parseFloat(periodFill().style.width)).toBeLessThan(at25);
    } finally {
      vi.useRealTimers();
    }
  });

  test("reads a fresh period as a full bar and label, never zero", () => {
    vi.useFakeTimers({ now: 120_000 });
    try {
      startEnrollment();
      expect(
        screen.getByTestId("auth-period").getAttribute("aria-label"),
      ).toContain("New code in 30 seconds");
      expect(periodFill().style.width).toBe("100%");
    } finally {
      vi.useRealTimers();
    }
  });

  test("under reduced motion updates once a second instead of continuously", () => {
    stubReducedMotion(true);
    vi.useFakeTimers({ now: 125_000 });
    try {
      startEnrollment();
      const period = screen.getByTestId("auth-period");
      act(() => {
        vi.advanceTimersByTime(999);
      });
      expect(period.getAttribute("aria-label")).toContain(
        "New code in 25 seconds",
      );
      act(() => {
        vi.advanceTimersByTime(1);
      });
      expect(period.getAttribute("aria-label")).toContain(
        "New code in 24 seconds",
      );
    } finally {
      vi.useRealTimers();
      vi.unstubAllGlobals();
    }
  });

  test("in simplified mode updates discretely too", () => {
    document.documentElement.dataset.simple = "on";
    vi.useFakeTimers({ now: 125_000 });
    try {
      startEnrollment();
      const period = screen.getByTestId("auth-period");
      act(() => {
        vi.advanceTimersByTime(999);
      });
      expect(period.getAttribute("aria-label")).toContain(
        "New code in 25 seconds",
      );
      act(() => {
        vi.advanceTimersByTime(1);
      });
      expect(period.getAttribute("aria-label")).toContain(
        "New code in 24 seconds",
      );
    } finally {
      vi.useRealTimers();
      delete document.documentElement.dataset.simple;
    }
  });

  test("names the period in Russian", () => {
    setLocale("ru");
    vi.useFakeTimers({ now: 125_000 });
    try {
      startEnrollment();
      expect(
        screen.getByTestId("auth-period").getAttribute("aria-label"),
      ).toContain("Новый код через 25 с");
    } finally {
      vi.useRealTimers();
      setLocale("en");
    }
  });
});

test("a result arriving after reopening cannot replace fresh server metadata", async () => {
  const pending = deferred<{
    ok: boolean;
    json: () => Promise<{ codes: string[]; backup_codes_generation: string }>;
  }>();
  vi.stubGlobal(
    "fetch",
    vi.fn(() => pending.promise),
  );
  const first = render(<ConfiguredForm />);
  fireEvent.click(
    screen.getByRole("button", { name: "Regenerate backup codes" }),
  );
  fireEvent.click(screen.getByRole("button", { name: "Regenerate codes" }));
  first.unmount();
  const onChange = vi.fn();
  render(
    <ConfirmProvider>
      <AuthenticationSection
        value={{
          login: {
            totp_configured: true,
            backup_codes_configured: true,
            backup_codes_count: 1,
            backup_codes_generation: "generation-new",
          },
        }}
        onChange={onChange}
      />
    </ConfirmProvider>,
  );
  await act(async () => {
    pending.resolve({
      ok: true,
      json: async () => ({
        codes: ["STALE-PRIVATE"],
        backup_codes_generation: "generation-old",
      }),
    });
    await pending.promise;
  });
  await waitFor(() => expect(screen.queryByText("STALE-PRIVATE")).toBeNull());
  expect(onChange).not.toHaveBeenCalled();
});
