import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  act,
  fireEvent,
  render,
  screen,
  waitFor,
  cleanup,
} from "@testing-library/react";
import { KimiAuthField as Field } from "./KimiAuthField";
import { useState, type ComponentProps } from "react";
function KimiAuthField(props: ComponentProps<typeof Field>) {
  const [identity, setIdentity] = useState("");
  return (
    <Field
      {...props}
      identity={props.identity ?? identity}
      onIdentity={
        props.onIdentity ??
        ((value) => {
          setIdentity(value);
          return true;
        })
      }
    />
  );
}

const json = (value: unknown) =>
  new Response(JSON.stringify(value), { status: 200 });

describe("KimiAuthField", () => {
  const fetchMock = vi.fn();
  beforeEach(() => {
    vi.stubGlobal("fetch", fetchMock);
    fetchMock.mockReset();
  });
  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });
  it("disables sign out for CLI credentials without deleting them", async () => {
    fetchMock.mockResolvedValue(json({ connected: true, source: "cli" }));
    render(
      <KimiAuthField providerName="kimi" mode="subscription" region="global" />,
    );
    const button = await screen.findByRole("button", { name: "Sign out" });
    expect(button).toBeDisabled();
    expect(button.getAttribute("title")).toContain("CLI");
    fireEvent.click(button);
    expect(
      fetchMock.mock.calls.some(([, init]) => init?.method === "DELETE"),
    ).toBe(false);
  });
  it.each([
    { providerName: "other", mode: "subscription", region: "global" },
    { providerName: "kimi", mode: "platform", region: "global" },
    { providerName: "kimi", mode: "subscription", region: "china" },
    {
      providerName: "kimi",
      mode: "subscription",
      region: "global",
      identity: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
    },
  ])(
    "ignores an old start response after changing $providerName $mode $region",
    async (next) => {
      let finish!: (response: Response) => void;
      fetchMock.mockImplementation(async (_url: string, init?: RequestInit) =>
        init?.method === "POST"
          ? new Promise<Response>((resolve) => {
              finish = resolve;
            })
          : json({ connected: false, source: "none" }),
      );
      const view = render(
        <KimiAuthField
          providerName="kimi"
          mode="subscription"
          region="global"
        />,
      );
      fireEvent.click(screen.getByText("Sign in with Kimi"));
      view.rerender(<KimiAuthField {...next} />);
      finish(
        json({
          login_id: "old",
          kimi_identity: "0123456789abcdef0123456789abcdef",
          mode: "subscription",
          region: "global",
          user_code: "OLD-CODE",
          verification_url: "https://www.kimi.ai/code/authorize_device",
          status: "pending",
        }),
      );
      await waitFor(() =>
        expect(
          fetchMock.mock.calls.some(
            ([url, init]) =>
              String(url).includes("/device/old") && init?.method === "DELETE",
          ),
        ).toBe(true),
      );
      expect(screen.queryByText("OLD-CODE")).toBeNull();
      expect(screen.queryByText("Signed in with Coddy")).toBeNull();
    },
  );
  it("delivers a generated identity to the live draft", async () => {
    const onIdentity = vi.fn(() => true);
    fetchMock.mockImplementation(async (_url: string, init?: RequestInit) =>
      init?.method === "POST"
        ? json({
            login_id: "x",
            user_code: "1234",
            verification_url: "https://www.kimi.ai/code/authorize_device",
            status: "pending",
            region: "global",
            mode: "subscription",
            kimi_identity: "0123456789abcdef0123456789abcdef",
          })
        : json({ connected: false, source: "none" }),
    );
    render(
      <KimiAuthField
        providerName="kimi"
        mode="subscription"
        region="global"
        onIdentity={onIdentity}
      />,
    );
    fireEvent.click(screen.getByText("Sign in with Kimi"));
    await waitFor(() =>
      expect(onIdentity).toHaveBeenCalledWith(
        "0123456789abcdef0123456789abcdef",
        "",
      ),
    );
  });
  it("uses the current identity for status, start and signout", async () => {
    const id = "0123456789abcdef0123456789abcdef";
    let connected = true;
    fetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
      if (init?.method === "DELETE") {
        connected = false;
        return json({ connected: false, source: "none" });
      }
      if (init?.method === "POST")
        return json({
          login_id: "x",
          user_code: "1234",
          verification_url: "https://www.kimi.ai/code/authorize_device",
          mode: "subscription",
          region: "global",
          kimi_identity: id,
        });
      return json({ connected, source: connected ? "managed" : "none" });
    });
    render(
      <KimiAuthField
        providerName="kimi"
        mode="subscription"
        region="global"
        identity={id}
      />,
    );
    fireEvent.click(await screen.findByText("Sign out"));
    fireEvent.click(await screen.findByText("Sign in with Kimi"));
    await screen.findByText("1234");
    expect(
      fetchMock.mock.calls.some(
        ([url, init]) =>
          init?.method === "DELETE" &&
          String(url).includes(`kimi_identity=${id}`),
      ),
    ).toBe(true);
    expect(
      fetchMock.mock.calls.some(
        ([url, init]) =>
          !init?.method && String(url).includes(`kimi_identity=${id}`),
      ),
    ).toBe(true);
    expect(
      fetchMock.mock.calls.some(
        ([, init]) =>
          init?.method === "POST" &&
          JSON.parse(String(init.body)).kimi_identity === id,
      ),
    ).toBe(true);
  });
  it("cancels a start rejected by the row lifecycle guard", async () => {
    fetchMock.mockImplementation(async (_url: string, init?: RequestInit) =>
      init?.method === "POST"
        ? json({
            login_id: "x",
            user_code: "1234",
            verification_url: "https://www.kimi.ai/code/authorize_device",
            mode: "subscription",
            region: "global",
            kimi_identity: "0123456789abcdef0123456789abcdef",
          })
        : json({ connected: false, source: "none", cancelled: true }),
    );
    render(
      <KimiAuthField
        providerName="kimi"
        mode="subscription"
        region="global"
        onIdentity={() => false}
      />,
    );
    fireEvent.click(screen.getByText("Sign in with Kimi"));
    await waitFor(() =>
      expect(
        fetchMock.mock.calls.some(
          ([url, init]) =>
            String(url).includes("/device/x") && init?.method === "DELETE",
        ),
      ).toBe(true),
    );
    expect(screen.queryByText("1234")).toBeNull();
    expect(
      await screen.findByText(
        "The provider changed during sign-in. Start again.",
      ),
    ).toBeTruthy();
  });
  it("abandons the old login locally when durable cancel fails", async () => {
    let finishPoll!: (response: Response) => void;
    fetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
      if (init?.method === "POST")
        return json({
          login_id: "old",
          kimi_identity: "0123456789abcdef0123456789abcdef",
          mode: "subscription",
          region: "global",
          user_code: "1234",
          verification_url: "https://www.kimi.ai/code/authorize_device",
        });
      if (init?.method === "DELETE")
        return new Response("<html>private proxy body</html>", { status: 500 });
      if (String(url).includes("/device/old"))
        return new Promise<Response>((resolve) => {
          finishPoll = resolve;
        });
      return json({ connected: url.includes("region=china"), source: "cli" });
    });
    const view = render(
      <KimiAuthField providerName="kimi" mode="subscription" region="global" />,
    );
    fireEvent.click(screen.getByText("Sign in with Kimi"));
    await screen.findByText("1234");
    await waitFor(() => expect(finishPoll).toBeDefined(), { timeout: 3000 });
    view.rerender(
      <KimiAuthField providerName="kimi" mode="subscription" region="china" />,
    );
    expect(await screen.findByText("Using Kimi CLI credentials")).toBeTruthy();
    expect(await screen.findByText(/HTTP 500/)).toBeTruthy();
    await act(async () =>
      finishPoll(
        json({ status: "completed", connected: false, save_required: true }),
      ),
    );
    expect(screen.queryByTestId("kimi-auth-save-required")).toBeNull();
    expect(document.body.textContent).not.toContain("private proxy body");
  });
  it("reports a failed cancellation of a late start response without hiding the new status", async () => {
    let finishStart!: (response: Response) => void;
    fetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
      if (init?.method === "POST")
        return new Promise<Response>((resolve) => {
          finishStart = resolve;
        });
      if (init?.method === "DELETE")
        return new Response("<html>private proxy body</html>", { status: 500 });
      return json({
        connected: String(url).includes("region=china"),
        source: "cli",
      });
    });
    const view = render(
      <KimiAuthField providerName="kimi" mode="subscription" region="global" />,
    );
    fireEvent.click(screen.getByText("Sign in with Kimi"));
    view.rerender(
      <KimiAuthField providerName="kimi" mode="subscription" region="china" />,
    );
    expect(await screen.findByText("Using Kimi CLI credentials")).toBeTruthy();
    await act(async () =>
      finishStart(
        json({
          login_id: "old",
          kimi_identity: "0123456789abcdef0123456789abcdef",
          mode: "subscription",
          region: "global",
          user_code: "1234",
          verification_url: "https://www.kimi.ai/code/authorize_device",
        }),
      ),
    );
    expect(await screen.findByText(/HTTP 500/)).toBeTruthy();
    expect(screen.queryByText("1234")).toBeNull();
    expect(document.body.textContent).not.toContain("private proxy body");
  });
  it("keeps the active login cancellable and forbids a second start after poll network failure", async () => {
    fetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
      if (init?.method === "POST")
        return json({
          login_id: "x",
          kimi_identity: "0123456789abcdef0123456789abcdef",
          mode: "subscription",
          region: "global",
          user_code: "1234",
          verification_url: "https://www.kimi.ai/code/authorize_device",
        });
      if (String(url).includes("/device/x") && init?.method !== "DELETE")
        throw new Error("offline");
      return json({ connected: false, source: "none", cancelled: true });
    });
    render(
      <KimiAuthField providerName="kimi" mode="subscription" region="global" />,
    );
    fireEvent.click(screen.getByText("Sign in with Kimi"));
    await screen.findByText("offline", {}, { timeout: 3000 });
    expect(screen.getByText("Cancel")).toBeEnabled();
    expect(screen.getByText("Sign in with Kimi")).toBeDisabled();
    fireEvent.click(screen.getByText("Sign in with Kimi"));
    expect(
      fetchMock.mock.calls.filter(([, init]) => init?.method === "POST"),
    ).toHaveLength(1);
  });
  it("does not accept an untrusted verification URL", async () => {
    fetchMock.mockImplementation(async (_url: string, init?: RequestInit) =>
      init?.method === "POST"
        ? json({
            login_id: "x",
            kimi_identity: "0123456789abcdef0123456789abcdef",
            mode: "subscription",
            region: "global",
            user_code: "1234",
            verification_url: "https://evil.example/code/authorize_device",
          })
        : json({ connected: false, source: "none" }),
    );
    render(
      <KimiAuthField providerName="kimi" mode="subscription" region="global" />,
    );
    fireEvent.click(screen.getByText("Sign in with Kimi"));
    expect(await screen.findByText(/invalid verification URL/)).toBeTruthy();
    expect(screen.queryByText("1234")).toBeNull();
  });
  it("finishes a confirmed unsaved login without claiming it is connected", async () => {
    const ready = vi.fn();
    const connected = vi.fn();
    fetchMock.mockImplementation(async (url: string, init?: RequestInit) =>
      init?.method === "POST"
        ? json({
            login_id: "x",
            kimi_identity: "0123456789abcdef0123456789abcdef",
            mode: "subscription",
            region: "global",
            user_code: "1234",
            verification_url: "https://www.kimi.ai/code/authorize_device",
            status: "pending",
          })
        : url.includes("/device/x") && init?.method !== "DELETE"
          ? json({ status: "completed", connected: false, save_required: true })
          : json({ connected: false, source: "none", cancelled: true }),
    );
    render(
      <KimiAuthField
        providerName="kimi"
        mode="subscription"
        region="global"
        onReadyChange={ready}
        onConnectedChange={connected}
      />,
    );
    fireEvent.click(screen.getByText("Sign in with Kimi"));
    expect(
      await screen.findByText(
        "Sign-in confirmed. Save the provider settings to use it.",
        {},
        { timeout: 3000 },
      ),
    ).toBeTruthy();
    expect(screen.queryByText("1234")).toBeNull();
    expect(screen.queryByText("Signed in with Coddy")).toBeNull();
    expect(ready).toHaveBeenLastCalledWith(true);
    expect(connected).not.toHaveBeenCalledWith(true);
  });
  it.each([
    { status: "completed", connected: false },
    { status: "completed", connected: true, save_required: true },
    { status: "unexpected" },
  ])("rejects an ambiguous polling response %#", async (poll) => {
    const ready = vi.fn();
    fetchMock.mockImplementation(async (url: string, init?: RequestInit) =>
      init?.method === "POST"
        ? json({
            login_id: "x",
            kimi_identity: "0123456789abcdef0123456789abcdef",
            mode: "subscription",
            region: "global",
            user_code: "1234",
            verification_url: "https://www.kimi.ai/code/authorize_device",
            status: "pending",
          })
        : url.includes("/device/x") && init?.method !== "DELETE"
          ? json(poll)
          : json({ connected: false, source: "none", cancelled: true }),
    );
    render(
      <KimiAuthField
        providerName="kimi"
        mode="subscription"
        region="global"
        onReadyChange={ready}
      />,
    );
    fireEvent.click(screen.getByText("Sign in with Kimi"));
    expect(
      await screen.findByText(
        "The sign-in response was incomplete or contradictory. Start again.",
        {},
        { timeout: 3000 },
      ),
    ).toBeTruthy();
    expect(ready).not.toHaveBeenCalledWith(true);
    expect(screen.queryByTestId("kimi-auth-save-required")).toBeNull();
  });
  it("ignores a late connected status after an ambiguous poll", async () => {
    let finishStatus!: (value: Response) => void;
    const ready = vi.fn();
    fetchMock.mockImplementation(async (url: string, init?: RequestInit) => {
      if (init?.method === "POST")
        return json({
          login_id: "x",
          kimi_identity: "0123456789abcdef0123456789abcdef",
          mode: "subscription",
          region: "global",
          user_code: "1234",
          verification_url: "https://www.kimi.ai/code/authorize_device",
          status: "pending",
        });
      if (String(url).includes("/device/x") && init?.method !== "DELETE")
        return json({ status: "completed", connected: false });
      if (
        String(url).includes(
          "kimi_identity=0123456789abcdef0123456789abcdef",
        ) &&
        !init?.method
      )
        return new Promise<Response>((resolve) => {
          finishStatus = resolve;
        });
      return json({ connected: false, source: "none", cancelled: true });
    });
    render(
      <KimiAuthField
        providerName="kimi"
        mode="subscription"
        region="global"
        onReadyChange={ready}
      />,
    );
    fireEvent.click(screen.getByText("Sign in with Kimi"));
    await screen.findByText(
      "The sign-in response was incomplete or contradictory. Start again.",
      {},
      { timeout: 3000 },
    );
    expect(finishStatus).toBeDefined();
    finishStatus(json({ connected: true, source: "managed" }));
    await waitFor(() => expect(ready).toHaveBeenLastCalledWith(false));
    expect(screen.queryByText("Signed in with Coddy")).toBeNull();
  });
  it("ignores confirmed polling from the previous region", async () => {
    let finish!: (value: Response) => void;
    const ready = vi.fn();
    fetchMock.mockImplementation(async (url: string, init?: RequestInit) =>
      init?.method === "POST"
        ? json({
            login_id: "x",
            kimi_identity: "0123456789abcdef0123456789abcdef",
            mode: "subscription",
            region: "global",
            user_code: "1234",
            verification_url: "https://www.kimi.ai/code/authorize_device",
            status: "pending",
          })
        : url.includes("/device/x") && init?.method !== "DELETE"
          ? new Promise<Response>((resolve) => {
              finish = resolve;
            })
          : json({ connected: false, source: "none", cancelled: true }),
    );
    const view = render(
      <KimiAuthField
        providerName="kimi"
        mode="subscription"
        region="global"
        onReadyChange={ready}
      />,
    );
    fireEvent.click(screen.getByText("Sign in with Kimi"));
    await waitFor(() => expect(finish).toBeDefined(), { timeout: 3000 });
    view.rerender(
      <KimiAuthField
        providerName="kimi"
        mode="subscription"
        region="china"
        onReadyChange={ready}
      />,
    );
    finish(
      json({ status: "completed", connected: false, save_required: true }),
    );
    await waitFor(() =>
      expect(
        fetchMock.mock.calls.some(
          ([url, init]) =>
            String(url).includes("/device/x") && init?.method === "DELETE",
        ),
      ).toBe(true),
    );
    expect(ready).not.toHaveBeenCalledWith(true);
    expect(screen.queryByTestId("kimi-auth-save-required")).toBeNull();
  });
  it("reports server failure instead of a successful login", async () => {
    fetchMock.mockImplementation(async (url: string, init?: RequestInit) =>
      init?.method === "POST"
        ? json({
            login_id: "x",
            kimi_identity: "0123456789abcdef0123456789abcdef",
            mode: "subscription",
            region: "global",
            user_code: "1234",
            verification_url: "https://www.kimi.ai/code/authorize_device",
            status: "pending",
          })
        : url.includes("/device/x")
          ? json({ status: "failed", error: "authorization failed" })
          : json({ connected: false, source: "none" }),
    );
    render(
      <KimiAuthField providerName="kimi" mode="subscription" region="global" />,
    );
    fireEvent.click(screen.getByText("Sign in with Kimi"));
    expect(
      await screen.findByText("authorization failed", {}, { timeout: 3000 }),
    ).toBeTruthy();
    expect(screen.queryByText("Signed in with Coddy")).toBeNull();
  });
  it("uses the returned CLI status after sign out", async () => {
    fetchMock.mockImplementation(async (_url: string, init?: RequestInit) =>
      init?.method === "DELETE"
        ? json({ connected: true, source: "cli" })
        : json({ connected: true, source: "managed" }),
    );
    render(
      <KimiAuthField providerName="kimi" mode="subscription" region="global" />,
    );
    fireEvent.click(await screen.findByText("Sign out"));
    expect(await screen.findByText("Using Kimi CLI credentials")).toBeTruthy();
  });
  it("allows retrying cancellation after the server refuses it", async () => {
    let deletes = 0;
    fetchMock.mockImplementation(async (_url: string, init?: RequestInit) => {
      if (init?.method === "POST")
        return json({
          login_id: "x",
          kimi_identity: "0123456789abcdef0123456789abcdef",
          mode: "subscription",
          region: "global",
          user_code: "1234",
          verification_url: "https://www.kimi.ai/code/authorize_device",
          status: "pending",
        });
      if (init?.method === "DELETE")
        return ++deletes === 1
          ? new Response("cancel failed", { status: 500 })
          : json({ cancelled: true });
      return json({ connected: false, source: "none" });
    });
    render(
      <KimiAuthField providerName="kimi" mode="subscription" region="global" />,
    );
    fireEvent.click(screen.getByText("Sign in with Kimi"));
    await screen.findByText("1234");
    fireEvent.click(screen.getByText("Cancel"));
    await screen.findByText(/HTTP 500|cancel failed/);
    fireEvent.click(screen.getByText("Cancel"));
    await waitFor(() => expect(deletes).toBe(2));
    expect(screen.queryByText("1234")).toBeNull();
  });
  it("cancels a pending attempt on unmount", async () => {
    fetchMock.mockImplementation(async (_url: string, init?: RequestInit) =>
      init?.method === "POST"
        ? json({
            login_id: "x",
            kimi_identity: "0123456789abcdef0123456789abcdef",
            mode: "subscription",
            region: "global",
            user_code: "1234",
            verification_url: "https://www.kimi.ai/code/authorize_device",
            status: "pending",
          })
        : json({ connected: false, source: "none" }),
    );
    const view = render(
      <KimiAuthField providerName="kimi" mode="subscription" region="global" />,
    );
    fireEvent.click(screen.getByText("Sign in with Kimi"));
    await screen.findByText("1234");
    view.unmount();
    await waitFor(() =>
      expect(
        fetchMock.mock.calls.some(
          ([url, init]) =>
            String(url).includes("/device/x") && init?.method === "DELETE",
        ),
      ).toBe(true),
    );
  });
});
