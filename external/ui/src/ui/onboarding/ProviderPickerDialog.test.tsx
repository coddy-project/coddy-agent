import { beforeEach, afterEach, describe, expect, it, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { I18nProvider } from "../i18n/I18nProvider";
import { initLocale } from "../i18n/i18n";
import { ProviderPickerDialog } from "./ProviderPickerDialog";

function renderPicker(onSaved = vi.fn(), onSkip = vi.fn()) {
  render(
    <I18nProvider>
      <ProviderPickerDialog open onSaved={onSaved} onSkip={onSkip} />
    </I18nProvider>,
  );
  return onSaved;
}

describe("ProviderPickerDialog", () => {
  const fetchMock = vi.fn();
  let devinConnected = false;
  let devinSource = "none";
  let baseProviders: Record<string, unknown>[] = [];
  let baseConfigDelay = 0;

  beforeEach(() => {
    initLocale("en");
    devinConnected = false;
    devinSource = "none";
    baseProviders = [
      {
        name: "local",
        type: "openai",
        api_base: "http://localhost:11434/v1",
      },
    ];
    baseConfigDelay = 0;
    fetchMock.mockReset();
    fetchMock.mockImplementation(
      async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        const method = init?.method || "GET";
        if (url === "/coddy/config" && method === "GET") {
          if (baseConfigDelay) {
            await new Promise((resolve) =>
              setTimeout(resolve, baseConfigDelay),
            );
          }
          return new Response(
            JSON.stringify({
              providers: baseProviders,
              models: [{ model: "local/qwen", max_tokens: 4096 }],
              agent: { model: "local/qwen", max_turns: 40 },
            }),
            { status: 200 },
          );
        }
        if (url === "/coddy/providers/models" && method === "POST") {
          const body = JSON.parse(String(init?.body || "{}"));
          const id =
            body.type === "devin"
              ? "claude-sonnet-5"
              : body.type === "opencode"
                ? "gpt-5.5"
                : "gpt-5.6-terra";
          return new Response(
            JSON.stringify({
              ok: true,
              models: [
                {
                  id,
                  context_window: 200000,
                  catalog: {
                    context_window: 200000,
                    max_tokens: 32000,
                    multimodal: true,
                  },
                },
              ],
            }),
            { status: 200 },
          );
        }
        if (
          url === "/coddy/providers/opencode/opencode-auth" &&
          method === "GET"
        ) {
          return new Response(
            JSON.stringify({
              connected: true,
              source: "oauth",
              organization: "Test Org",
            }),
            { status: 200 },
          );
        }
        if (url === "/coddy/providers/devin/devin-auth" && method === "GET") {
          return new Response(
            JSON.stringify({
              connected: devinConnected,
              source: devinSource,
              email: devinConnected ? "dev@example.com" : undefined,
            }),
            { status: 200 },
          );
        }
        if (
          url === "/coddy/providers/devin/devin-auth/device" &&
          method === "POST"
        ) {
          return new Response(
            JSON.stringify({
              login_id: "login-devin",
              verification_url: "https://devin.example/login",
              status: "pending",
            }),
            { status: 200 },
          );
        }
        if (
          url ===
            "/coddy/providers/devin/devin-auth/device/login-devin/paste" &&
          method === "POST"
        ) {
          return new Response(JSON.stringify({ status: "pending" }), {
            status: 202,
          });
        }
        if (
          url === "/coddy/providers/devin/devin-auth/device/login-devin" &&
          method === "GET"
        ) {
          devinConnected = true;
          devinSource = "coddy";
          return new Response(
            JSON.stringify({ status: "completed", connected: true }),
            { status: 200 },
          );
        }
        if (url === "/coddy/config/validate" && method === "POST") {
          return new Response(JSON.stringify({ ok: true }), { status: 200 });
        }
        if (url === "/coddy/config" && method === "PUT") {
          return new Response(JSON.stringify({ ok: true }), { status: 200 });
        }
        return new Response("not found", { status: 404 });
      },
    );
    vi.stubGlobal("fetch", fetchMock);
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });

  it("saves OpenCode with its managed login and keeps the existing config", async () => {
    const onSaved = renderPicker();
    fireEvent.click(screen.getByTestId("provider-card-opencode"));
    expect(
      await screen.findByText("Signed in to OpenCode (Test Org)."),
    ).toBeTruthy();
    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/coddy/providers/models",
        expect.objectContaining({ method: "POST" }),
      ),
    );
    fireEvent.click(screen.getByTestId("provider-save"));
    await waitFor(() => expect(onSaved).toHaveBeenCalled());
    const put = fetchMock.mock.calls.find(
      ([url, init]) => url === "/coddy/config" && init?.method === "PUT",
    );
    const body = JSON.parse(String(put?.[1]?.body));
    expect(body.providers).toHaveLength(2);
    expect(body.models).toHaveLength(2);
    expect(body.agent).toEqual({ model: "opencode/gpt-5.5", max_turns: 40 });
    const provider = body.providers.find(
      (row: { name: string }) => row.name === "opencode",
    );
    expect(provider).toEqual({
      name: "opencode",
      type: "opencode",
      api_base: "https://opencode.ai/inference/go/v1",
    });
    expect(
      body.models.find(
        (row: { model: string }) => row.model === "opencode/gpt-5.5",
      ),
    ).toEqual(
      expect.objectContaining({
        max_context_tokens: 200000,
        max_tokens: 32000,
        multimodal: true,
      }),
    );
  });

  it("completes Devin sign-in from a pasted callback and saves no key", async () => {
    const onSaved = renderPicker();
    vi.spyOn(window, "open").mockImplementation(() => null);
    fireEvent.click(screen.getByTestId("provider-card-devin"));
    fireEvent.click(await screen.findByTestId("devin-auth-sign-in"));
    expect(await screen.findByTestId("devin-callback-paste")).toBeTruthy();
    fireEvent.change(screen.getByTestId("devin-callback-paste"), {
      target: { value: "http://127.0.0.1:1234/callback?code=abc&state=xyz" },
    });
    fireEvent.click(screen.getByTestId("devin-paste-submit"));
    await waitFor(() =>
      expect(fetchMock).toHaveBeenCalledWith(
        "/coddy/providers/devin/devin-auth/device/login-devin/paste",
        expect.objectContaining({ method: "POST" }),
      ),
    );
    expect(
      await screen.findByText("Signed in to Devin (dev@example.com)."),
    ).toBeTruthy();
    fireEvent.click(screen.getByTestId("provider-save"));
    await waitFor(() => expect(onSaved).toHaveBeenCalled());
    const put = fetchMock.mock.calls.find(
      ([url, init]) => url === "/coddy/config" && init?.method === "PUT",
    );
    const body = JSON.parse(String(put?.[1]?.body));
    const provider = body.providers.find(
      (row: { name: string }) => row.name === "devin",
    );
    expect(provider).toEqual({ name: "devin", type: "devin" });
    expect(body.agent.model).toBe("devin/claude-sonnet-5");
  });

  it("renders the provider cards in Russian", () => {
    initLocale("ru");
    renderPicker();
    expect(screen.getByText("Выберите провайдера")).toBeTruthy();
    expect(
      screen.getByText("OpenCode Go или Console со входом в аккаунт"),
    ).toBeTruthy();
    expect(
      screen.getByText("Модели Devin со входом через браузер"),
    ).toBeTruthy();
  });

  it("waits for the base config before saving", async () => {
    // Save is clicked while GET /coddy/config is still in flight: the PUT
    // must carry the merged document, not a one-row one over the file.
    baseConfigDelay = 100;
    const onSaved = renderPicker();
    fireEvent.click(screen.getByTestId("provider-save"));
    await waitFor(() => expect(onSaved).toHaveBeenCalled(), { timeout: 3000 });
    const put = fetchMock.mock.calls.find(
      ([url, init]) => url === "/coddy/config" && init?.method === "PUT",
    );
    const body = JSON.parse(String(put?.[1]?.body));
    expect(body.providers).toHaveLength(2);
    expect(body.models).toHaveLength(2);
    expect(body.agent).toEqual({
      model: "openai/gpt-5.6-terra",
      max_turns: 40,
    });
  });

  it("keeps an explicit Devin key when no managed login exists", async () => {
    baseProviders.push({
      name: "devin",
      type: "devin",
      api_key: "devin-session-token$saved",
    });
    devinConnected = true;
    devinSource = "api_key";
    const onSaved = renderPicker();
    fireEvent.click(screen.getByTestId("provider-card-devin"));
    expect(
      await screen.findByText("Signed in to Devin (dev@example.com)."),
    ).toBeTruthy();
    fireEvent.click(screen.getByTestId("provider-save"));
    await waitFor(() => expect(onSaved).toHaveBeenCalled());
    const put = fetchMock.mock.calls.find(
      ([url, init]) => url === "/coddy/config" && init?.method === "PUT",
    );
    const body = JSON.parse(String(put?.[1]?.body));
    const provider = body.providers.find(
      (row: { name: string }) => row.name === "devin",
    );
    expect(provider.api_key).toBe("devin-session-token$saved");
  });

  it("does not close through the backdrop while saving", async () => {
    const onSaved = vi.fn();
    const onSkip = vi.fn();
    renderPicker(onSaved, onSkip);
    fireEvent.click(screen.getByTestId("provider-save"));
    const backdrop = screen.getByRole("button", {
      name: "Close onboarding",
    }) as HTMLButtonElement;
    expect(backdrop.disabled).toBe(true);
    fireEvent.click(backdrop);
    expect(onSkip).not.toHaveBeenCalled();
    await waitFor(() => expect(onSaved).toHaveBeenCalled());
  });

  it("keeps the compatible preset unsaved until it has an API base", () => {
    renderPicker();
    fireEvent.click(screen.getByTestId("provider-card-compatible"));
    const save = screen.getByTestId("provider-save") as HTMLButtonElement;
    const test = screen.getByTestId("provider-test") as HTMLButtonElement;
    // No endpoint of its own: Test and Save wait for a typed api_base, so a
    // keyless row with no destination cannot be written.
    expect(save.disabled).toBe(true);
    expect(test.disabled).toBe(true);
    expect(screen.getByTestId("provider-api-base-required")).toBeTruthy();
    fireEvent.change(screen.getByTestId("provider-api-base"), {
      target: { value: "https://api.deepseek.example/v1" },
    });
    expect(save.disabled).toBe(false);
    expect(test.disabled).toBe(false);
    expect(screen.queryByTestId("provider-api-base-required")).toBeNull();
  });
});
