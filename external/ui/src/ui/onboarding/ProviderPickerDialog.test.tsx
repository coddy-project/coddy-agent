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

/** The four that do not lead render only once the row under the grid is
 * pressed; before that they are not in the page at all. */
function showAllProviders() {
  fireEvent.click(screen.getByTestId("provider-more-toggle"));
}

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
  let codexConnected = true;
  let baseProviders: Record<string, unknown>[] = [];
  let baseConfigDelay = 0;

  beforeEach(() => {
    initLocale("en");
    devinConnected = false;
    devinSource = "none";
    codexConnected = true;
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
          // Each provider lists a model of its own, the way a real one does:
          // the picker starts a row on its preset's default where the
          // provider serves it, so a fixture that answered every type with an
          // OpenAI id would hide which branch ran.
          const id =
            body.type === "devin"
              ? "claude-sonnet-5"
              : body.type === "opencode"
                ? "gpt-5.5"
                : body.type === "neuraldeep"
                  ? "qwen3.6-35b-a3b"
                  : body.type === "codex"
                    ? "gpt-5.6-sol"
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
        if (url === "/coddy/providers/codex/codex-auth" && method === "GET") {
          // A machine the Codex CLI has already signed in on: the row the
          // picker is about to write may use that login, so the server
          // reports it connected before anything is saved.
          return new Response(
            JSON.stringify({ connected: codexConnected, source: "codex_cli" }),
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

  it("opens on NeuralDeep and draws the cards in the catalog's order", async () => {
    renderPicker();
    const drawn = () =>
      Array.from(
        document.querySelectorAll<HTMLElement>("[data-testid^=provider-card-]"),
      ).map((card) => card.dataset.testid);
    // Only the four that lead are on screen at first.
    expect(drawn()).toEqual([
      "provider-card-neuraldeep",
      "provider-card-opencode",
      "provider-card-openai",
      "provider-card-anthropic",
    ]);
    showAllProviders();
    expect(drawn()).toEqual([
      "provider-card-neuraldeep",
      "provider-card-opencode",
      "provider-card-openai",
      "provider-card-anthropic",
      "provider-card-codex",
      "provider-card-devin",
      "provider-card-compatible",
      "provider-card-acp",
    ]);
    expect(
      screen
        .getByTestId("provider-card-neuraldeep")
        .classList.contains("provider-picker-card--active"),
    ).toBe(true);
    // Its endpoint picker is the one on screen, so Enter on the open dialog
    // configures NeuralDeep without a card being clicked first.
    expect(
      await screen.findByTestId("provider-neuraldeep-domain"),
    ).toBeTruthy();
  });

  it("goes straight to the models of a Codex this machine already signed in to", async () => {
    const onSaved = renderPicker();
    showAllProviders();
    fireEvent.click(screen.getByTestId("provider-card-codex"));
    expect(
      await screen.findByText(
        "Connected via the Codex CLI login on this server.",
      ),
    ).toBeTruthy();
    // Nothing was typed and no sign-in was started: the stored login is what
    // the listing is made with.
    await waitFor(() =>
      expect(
        fetchMock.mock.calls.some(
          ([url, init]) =>
            url === "/coddy/providers/models" && init?.method === "POST",
        ),
      ).toBe(true),
    );
    expect(
      fetchMock.mock.calls.some(([url]) =>
        String(url).includes("/codex-auth/device"),
      ),
    ).toBe(false);
    fireEvent.click(screen.getByTestId("provider-save"));
    await waitFor(() => expect(onSaved).toHaveBeenCalled());
    const put = fetchMock.mock.calls.find(
      ([url, init]) => url === "/coddy/config" && init?.method === "PUT",
    );
    const body = JSON.parse(String(put?.[1]?.body));
    // A Codex row is its sign-in: no key, no endpoint of its own.
    expect(
      body.providers.find((row: { name: string }) => row.name === "codex"),
    ).toEqual({ name: "codex", type: "codex" });
  });

  it("offers the sign-in when this machine has no Codex login", async () => {
    codexConnected = false;
    renderPicker();
    showAllProviders();
    fireEvent.click(screen.getByTestId("provider-card-codex"));
    expect(await screen.findByTestId("codex-auth-sign-in")).toBeTruthy();
    expect(
      screen.queryByText("Connected via the Codex CLI login on this server."),
    ).toBeNull();
  });

  it("shows the four providers that lead and the rest on request", async () => {
    renderPicker();
    for (const id of ["codex", "devin", "compatible", "acp"]) {
      expect(screen.queryByTestId(`provider-card-${id}`)).toBeNull();
    }
    const toggle = screen.getByTestId("provider-more-toggle");
    expect(toggle.textContent).toBe("More providers");
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    // It is a row, not a fieldset: no legend and no frame around the cards.
    expect(toggle.closest("fieldset")).toBeNull();
    showAllProviders();
    expect(toggle.getAttribute("aria-expanded")).toBe("true");
    expect(toggle.textContent).toBe("Fewer providers");
    // A card in there is pickable, and picking it keeps the rest on screen.
    fireEvent.click(screen.getByTestId("provider-card-acp"));
    expect(await screen.findByTestId("provider-command")).toBeTruthy();
    fireEvent.click(toggle);
    expect(screen.getByTestId("provider-card-acp")).toBeTruthy();
  });

  it("offers no Skip, and the backdrop is the way out", () => {
    const onSkip = vi.fn();
    renderPicker(vi.fn(), onSkip);
    expect(screen.queryByTestId("provider-skip")).toBeNull();
    expect(screen.queryByText("Skip for now")).toBeNull();
    fireEvent.click(screen.getByLabelText("Close onboarding"));
    expect(onSkip).toHaveBeenCalled();
  });

  it("keeps the key out of the way of a sign-in, and in reach under Advanced", async () => {
    renderPicker();
    // NeuralDeep signs in; its key is not a second thing to answer first.
    const advanced = screen.getByTestId("provider-advanced");
    await waitFor(() =>
      expect(advanced.contains(screen.getByTestId("provider-api-key"))).toBe(
        true,
      ),
    );
    // OpenAI has no sign-in, so its key is the form's own first field.
    fireEvent.click(screen.getByTestId("provider-card-openai"));
    await waitFor(() =>
      expect(
        screen
          .getByTestId("provider-advanced")
          .contains(screen.getByTestId("provider-api-key")),
      ).toBe(false),
    );
    // Codex carries no key at all.
    showAllProviders();
    fireEvent.click(screen.getByTestId("provider-card-codex"));
    await waitFor(() =>
      expect(screen.queryByTestId("provider-api-key")).toBeNull(),
    );
  });

  it("folds the proxy away behind Advanced settings", async () => {
    renderPicker();
    const toggle = await screen.findByTestId("provider-advanced-toggle");
    expect(toggle.textContent).toBe("Advanced settings");
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    const body = screen
      .getByTestId("provider-advanced")
      .querySelector(".settings-form-group-body")!;
    expect(body.hasAttribute("hidden")).toBe(true);
    // The field is mounted while folded, so what it holds survives the fold.
    expect(body.querySelector('[data-testid="provider-proxy"]')).not.toBeNull();
    fireEvent.click(toggle);
    expect(toggle.getAttribute("aria-expanded")).toBe("true");
    expect(body.hasAttribute("hidden")).toBe(false);
  });

  it("asks for no model and starts the row on what the provider serves", async () => {
    const onSaved = renderPicker();
    // Nothing on the form names a model any more.
    expect(screen.queryByTestId("provider-model-id")).toBeNull();
    expect(screen.queryByTestId("provider-fetch-models")).toBeNull();
    expect(screen.queryByText("Default model")).toBeNull();

    showAllProviders();
    fireEvent.click(screen.getByTestId("provider-card-compatible"));
    fireEvent.change(await screen.findByTestId("provider-api-base"), {
      target: { value: "http://127.0.0.1:11434/v1" },
    });
    fireEvent.click(screen.getByTestId("provider-save"));
    await waitFor(() => expect(onSaved).toHaveBeenCalled());

    // Save asked the endpoint what it serves, because nothing had: the row
    // starts on that, not on the preset's "custom/gpt-4o".
    const put = fetchMock.mock.calls.find(
      ([url, init]) => url === "/coddy/config" && init?.method === "PUT",
    );
    const body = JSON.parse(String(put?.[1]?.body));
    expect(body.agent.model).toBe("custom/gpt-5.6-terra");
    expect(
      body.models.some(
        (row: { model: string }) => row.model === "custom/gpt-5.6-terra",
      ),
    ).toBe(true);
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
    showAllProviders();
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
    // The row that shows the rest is translated too.
    expect(screen.getByTestId("provider-more-toggle").textContent).toBe(
      "Ещё провайдеры",
    );
    showAllProviders();
    expect(screen.getByTestId("provider-more-toggle").textContent).toBe(
      "Свернуть",
    );
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
      model: "neuraldeep/qwen3.6-35b-a3b",
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
    showAllProviders();
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

  it("keeps the compatible preset unsaved until it has an API base", async () => {
    renderPicker();
    showAllProviders();
    fireEvent.click(screen.getByTestId("provider-card-compatible"));
    const save = screen.getByTestId("provider-save") as HTMLButtonElement;
    // No endpoint of its own: Save waits for a typed api_base, so a keyless
    // row with no destination cannot be written.
    expect(save.disabled).toBe(true);
    expect(screen.getByTestId("provider-api-base-required")).toBeTruthy();
    fireEvent.change(await screen.findByTestId("provider-api-base"), {
      target: { value: "https://api.deepseek.example/v1" },
    });
    expect(save.disabled).toBe(false);
    expect(screen.queryByTestId("provider-api-base-required")).toBeNull();
  });

  // Save is the only action left: the catalog it fetches is the connection
  // check that Test connection used to be, so a second button asked the
  // operator to do by hand what saving does anyway.
  it("leaves Save as the only action", () => {
    renderPicker();
    expect(screen.queryByTestId("provider-test")).toBeNull();
    expect(screen.queryByTestId("provider-fetch-models")).toBeNull();
    expect(screen.queryByTestId("provider-skip")).toBeNull();
    const actions = document.querySelectorAll(
      ".provider-picker-actions button",
    );
    expect(actions).toHaveLength(1);
    expect((actions[0] as HTMLElement).dataset.testid).toBe("provider-save");
  });
});
