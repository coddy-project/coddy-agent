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
import { PROVIDER_PRESETS } from "./providerConfig";
import { messagesRu } from "../i18n/messages/ru";

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
        if (url.includes("/kimi-auth?") && method === "GET") {
          return new Response(
            JSON.stringify({
              connected: baseProviders.some((row) => row.name === "kimi"),
              source: "cli",
            }),
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

  it.each([
    ["api_key", "saved-key", "en"],
    ["api_key_command", "get-key", "ru"],
  ] as const)(
    "refuses Kimi subscription when %s is saved (%s)",
    async (field, value, locale) => {
      initLocale(locale);
      baseProviders.push({
        name: "kimi",
        type: "kimi",
        mode: "platform",
        [field]: value,
      });
      baseConfigDelay = 100;
      const before = structuredClone(baseProviders);
      const onSaved = renderPicker();
      fireEvent.click(screen.getByTestId("provider-card-kimi"));
      await screen.findByText(
        locale === "ru"
          ? "Используются учётные данные Kimi CLI"
          : "Using Kimi CLI credentials",
      );
      fireEvent.click(await screen.findByTestId("provider-save"));
      const error = await screen.findByTestId("provider-error");
      expect(error.textContent).toContain(locale === "ru" ? "ключ" : "key");
      expect(
        fetchMock.mock.calls.some(
          ([url, init]) =>
            (url === "/coddy/config/validate" && init?.method === "POST") ||
            (url === "/coddy/config" && init?.method === "PUT"),
        ),
      ).toBe(false);
      expect(onSaved).not.toHaveBeenCalled();
      expect(baseProviders).toEqual(before);
    },
  );

  it("retries an initial configuration read without losing the selected provider", async () => {
    let reads = 0;
    fetchMock.mockImplementation(
      async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        if (url === "/coddy/config" && !init?.method)
          return ++reads === 1
            ? new Response("<html>private proxy body</html>", { status: 503 })
            : new Response(
                JSON.stringify({ providers: [], models: [], agent: {} }),
                { status: 200 },
              );
        return new Response(
          JSON.stringify({
            connected: false,
            source: "none",
            ok: true,
            models: [],
          }),
          { status: 200 },
        );
      },
    );
    renderPicker();
    fireEvent.click(screen.getByTestId("provider-card-kimi"));
    expect(await screen.findByTestId("provider-error")).toBeTruthy();
    expect(document.body.textContent).not.toContain("private proxy body");
    fireEvent.click(
      screen.getByRole("button", { name: "Retry loading configuration" }),
    );
    expect(await screen.findByTestId("kimi-auth-field")).toBeTruthy();
    expect(reads).toBe(2);
  });
  it("retries only status after a successful save when status is unavailable", async () => {
    const id = "0123456789abcdef0123456789abcdef";
    let saved = false;
    let statusReadsAfterSave = 0;
    fetchMock.mockImplementation(
      async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input),
          method = init?.method || "GET";
        const reply = (body: unknown) =>
          new Response(JSON.stringify(body), { status: 200 });
        if (url === "/coddy/config" && method === "GET")
          return reply({ providers: [], models: [], agent: {} });
        if (url === "/coddy/config" && method === "PUT") {
          saved = true;
          return reply({ ok: true });
        }
        if (url === "/coddy/config/validate") return reply({ ok: true });
        if (url === "/coddy/providers/models")
          return reply({ ok: true, models: [] });
        if (url.endsWith("/kimi-auth/device") && method === "POST")
          return reply({
            login_id: "one",
            user_code: "1234",
            verification_url: "https://www.kimi.ai/code/authorize_device",
            mode: "subscription",
            region: "global",
            status: "pending",
            kimi_identity: id,
          });
        if (url.includes("/device/one") && method === "GET")
          return reply({
            status: "completed",
            connected: false,
            save_required: true,
          });
        if (url.includes("/kimi-auth")) {
          if (saved && ++statusReadsAfterSave === 1)
            return new Response("<html>private proxy body</html>", {
              status: 503,
            });
          return reply({
            connected: saved,
            source: saved ? "managed" : "none",
          });
        }
        return reply({});
      },
    );
    const onSaved = renderPicker();
    fireEvent.click(screen.getByTestId("provider-card-kimi"));
    fireEvent.click(await screen.findByTestId("provider-sign-in"));
    await screen.findByText(
      "Sign-in confirmed. Save the provider settings to use it.",
      {},
      { timeout: 3000 },
    );
    fireEvent.click(await screen.findByTestId("provider-save"));
    expect(await screen.findByText(/provider was saved/i)).toBeTruthy();
    expect(document.body.textContent).not.toContain("private proxy body");
    fireEvent.click(
      screen.getByRole("button", { name: "Retry sign-in status" }),
    );
    await waitFor(() => expect(onSaved).toHaveBeenCalledTimes(1));
    expect(
      fetchMock.mock.calls.filter(
        ([url, init]) => url === "/coddy/config" && init?.method === "PUT",
      ),
    ).toHaveLength(1);
  });
  it("keeps edits made while checking a saved Kimi status", async () => {
    const id = "0123456789abcdef0123456789abcdef";
    let saved = false;
    let finishStatus!: (value: Response) => void;
    fetchMock.mockImplementation(
      async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input),
          method = init?.method || "GET";
        const reply = (body: unknown) =>
          new Response(JSON.stringify(body), { status: 200 });
        if (url === "/coddy/config" && method === "GET")
          return reply({ providers: [], models: [], agent: {} });
        if (url === "/coddy/config" && method === "PUT") {
          saved = true;
          return reply({ ok: true });
        }
        if (url === "/coddy/config/validate") return reply({ ok: true });
        if (url === "/coddy/providers/models")
          return reply({ ok: true, models: [] });
        if (url.endsWith("/kimi-auth/device") && method === "POST")
          return reply({
            login_id: "one",
            user_code: "1234",
            verification_url: "https://www.kimi.ai/code/authorize_device",
            mode: "subscription",
            region: "global",
            status: "pending",
            kimi_identity: id,
          });
        if (url.includes("/device/one") && method === "GET")
          return reply({
            status: "completed",
            connected: false,
            save_required: true,
          });
        if (url.includes("/kimi-auth"))
          return saved
            ? new Promise<Response>((resolve) => {
                finishStatus = resolve;
              })
            : reply({ connected: false, source: "none" });
        return reply({});
      },
    );
    const onSaved = renderPicker();
    fireEvent.click(screen.getByTestId("provider-card-kimi"));
    fireEvent.click(await screen.findByTestId("provider-sign-in"));
    await screen.findByText(
      "Sign-in confirmed. Save the provider settings to use it.",
      {},
      { timeout: 3000 },
    );
    fireEvent.click(await screen.findByTestId("provider-save"));
    await waitFor(() => expect(finishStatus).toBeDefined());
    fireEvent.click(screen.getByTestId("provider-advanced-toggle"));
    fireEvent.change(screen.getByTestId("provider-proxy"), {
      target: { value: "http://edited.example:8080" },
    });
    finishStatus(
      new Response(JSON.stringify({ connected: true, source: "managed" }), {
        status: 200,
      }),
    );
    expect(
      await screen.findByText(/newer changes are not saved/i),
    ).toBeTruthy();
    expect(
      (screen.getByTestId("provider-proxy") as HTMLInputElement).value,
    ).toBe("http://edited.example:8080");
    expect(onSaved).not.toHaveBeenCalled();
    expect(
      fetchMock.mock.calls.filter(
        ([url, init]) => url === "/coddy/config" && init?.method === "PUT",
      ),
    ).toHaveLength(1);
  });

  it("starts Kimi Code login using the selected region and unsaved proxy", async () => {
    fetchMock.mockImplementation(
      async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        if (url.includes("/kimi-auth/device") && init?.method === "POST") {
          return new Response(
            JSON.stringify({
              login_id: "attempt",
              kimi_identity: "0123456789abcdef0123456789abcdef",
              mode: "subscription",
              region: "china",
              verification_url:
                "https://www.kimi.com/code/authorize_device?user_code=1234",
              user_code: "1234",
              status: "pending",
            }),
            { status: 200 },
          );
        }
        return new Response(
          JSON.stringify({ connected: false, source: "none" }),
          { status: 200 },
        );
      },
    );
    renderPicker();
    fireEvent.click(screen.getByTestId("provider-card-kimi"));
    fireEvent.change(screen.getByTestId("kimi-variant"), {
      target: { value: "subscription:china" },
    });
    fireEvent.click(screen.getByTestId("provider-advanced-toggle"));
    fireEvent.change(screen.getByTestId("provider-proxy"), {
      target: { value: "http://proxy.example:8080" },
    });
    fireEvent.click(await screen.findByTestId("provider-sign-in"));
    await waitFor(() =>
      expect(
        fetchMock.mock.calls.some(
          ([url, init]) =>
            String(url).endsWith("/kimi-auth/device") &&
            init?.method === "POST" &&
            JSON.parse(String(init.body)).region === "china" &&
            JSON.parse(String(init.body)).proxy === "http://proxy.example:8080",
        ),
      ).toBe(true),
    );
    expect(await screen.findByText("1234")).toBeTruthy();
  });

  it("saves the generated Kimi identity after a confirmed login", async () => {
    const id = "0123456789abcdef0123456789abcdef";
    let connected = false;
    fetchMock.mockImplementation(
      async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = String(input);
        const method = init?.method || "GET";
        const reply = (data: unknown) =>
          new Response(JSON.stringify(data), { status: 200 });
        if (url === "/coddy/config" && method === "GET")
          return reply({ providers: [], models: [], agent: {} });
        if (url === "/coddy/config" && method === "PUT") {
          connected = true;
          return reply({ ok: true });
        }
        if (url === "/coddy/config/validate") return reply({ ok: true });
        if (url === "/coddy/providers/models")
          return reply({ ok: true, models: [] });
        if (url.endsWith("/kimi-auth/device") && method === "POST")
          return reply({
            login_id: "one",
            user_code: "1234",
            verification_url: "https://www.kimi.ai/code/authorize_device",
            mode: "subscription",
            region: "global",
            status: "pending",
            kimi_identity: id,
          });
        if (url.includes("/device/one") && method === "GET") {
          return reply({
            status: "completed",
            connected: false,
            save_required: true,
          });
        }
        if (url.includes("/kimi-auth"))
          return reply({ connected, source: connected ? "managed" : "none" });
        return reply({});
      },
    );
    renderPicker();
    fireEvent.click(screen.getByTestId("provider-card-kimi"));
    fireEvent.click(await screen.findByTestId("provider-sign-in"));
    await screen.findByText("1234");
    await waitFor(
      () =>
        expect(
          fetchMock.mock.calls.some(
            ([url]) =>
              String(url).includes(`/kimi-auth?`) &&
              String(url).includes(`kimi_identity=${id}`),
          ),
        ).toBe(true),
      { timeout: 4000 },
    );
    expect(
      await screen.findByText(
        "Sign-in confirmed. Save the provider settings to use it.",
        {},
        { timeout: 4000 },
      ),
    ).toBeTruthy();
    expect(screen.queryByText("Signed in with Coddy")).toBeNull();
    fireEvent.click(await screen.findByTestId("provider-save"));
    await waitFor(() =>
      expect(
        fetchMock.mock.calls.some(
          ([url, init]) => url === "/coddy/config" && init?.method === "PUT",
        ),
      ).toBe(true),
    );
    const putIndex = fetchMock.mock.calls.findIndex(
      ([url, init]) => url === "/coddy/config" && init?.method === "PUT",
    );
    await waitFor(() =>
      expect(
        fetchMock.mock.calls
          .slice(putIndex + 1)
          .some(
            ([url, init]) =>
              String(url).includes(`/kimi-auth?`) &&
              String(url).includes(`kimi_identity=${id}`) &&
              !init?.method,
          ),
      ).toBe(true),
    );
    const request = fetchMock.mock.calls.find(
      ([url, init]) => url === "/coddy/config" && init?.method === "PUT",
    )!;
    expect(JSON.parse(String(request[1]?.body)).providers[0]).toMatchObject({
      name: "kimi",
      kimi_identity: id,
    });
  });

  it("keeps the Kimi variant and key when switching modes", async () => {
    renderPicker();
    fireEvent.click(screen.getByTestId("provider-card-kimi"));
    const choice = screen.getByTestId("kimi-variant") as HTMLSelectElement;
    expect(choice.value).toBe("subscription:global");
    expect(screen.queryByTestId("provider-api-key")).toBeNull();
    expect(await screen.findByTestId("kimi-auth-field")).toBeTruthy();
    fireEvent.change(choice, { target: { value: "platform:china" } });
    fireEvent.change(screen.getByTestId("provider-api-key"), {
      target: { value: "test-key" },
    });
    fireEvent.change(choice, { target: { value: "subscription:china" } });
    expect(screen.queryByTestId("provider-api-key")).toBeNull();
    fireEvent.change(choice, { target: { value: "platform:china" } });
    expect(
      (screen.getByTestId("provider-api-key") as HTMLInputElement).value,
    ).toBe("test-key");
  });

  it("opens on NeuralDeep and draws the cards in the catalog's order", async () => {
    renderPicker();
    const drawn = () =>
      Array.from(
        document.querySelectorAll<HTMLElement>("[data-testid^=provider-card-]"),
      ).map((card) => card.dataset.testid);
    // The whole catalog is in the page; the grid is the window over it.
    expect(drawn()).toEqual([
      "provider-card-neuraldeep",
      "provider-card-opencode",
      "provider-card-openai",
      "provider-card-anthropic",
      "provider-card-codex",
      "provider-card-devin",
      "provider-card-kimi",
      "provider-card-compatible",
      "provider-card-acp",
    ]);
    expect(
      screen
        .getByTestId("provider-card-neuraldeep")
        .classList.contains("provider-picker-card--active"),
    ).toBe(true);
    // Its account sign-in is the one on screen, so the dialog configures
    // NeuralDeep without a card being clicked first; the endpoint it opens on
    // is a refinement and lives in the fold.
    expect(await screen.findByTestId("neuraldeep-auth-field")).toBeTruthy();
  });

  it("goes straight to the models of a Codex this machine already signed in to", async () => {
    const onSaved = renderPicker();
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
    fireEvent.click(screen.getByTestId("provider-card-codex"));
    expect(await screen.findByTestId("provider-sign-in")).toBeTruthy();
    expect(
      screen.queryByText("Connected via the Codex CLI login on this server."),
    ).toBeNull();
  });

  it("keeps the whole catalog in one grid that scrolls", () => {
    renderPicker();
    const grid = screen.getByTestId("provider-grid");
    // Every provider is in the page; the window over them is the grid's own,
    // so nothing is hidden behind a word.
    expect(grid.querySelectorAll(".provider-picker-card")).toHaveLength(9);
    expect(screen.queryByTestId("provider-more-toggle")).toBeNull();
    // A card lower down is reachable without opening anything.
    fireEvent.click(screen.getByTestId("provider-card-acp"));
    expect(
      screen
        .getByTestId("provider-card-acp")
        .classList.contains("provider-picker-card--active"),
    ).toBe(true);
  });

  it("puts the endpoint in the fold where a preset has one, and in the form where it does not", async () => {
    renderPicker();
    // NeuralDeep opens on its own hub, so the choice is a refinement: it
    // lives in the fold, mounted but hidden until the legend is pressed.
    const fold = screen.getByTestId("provider-advanced");
    expect(
      fold.querySelector(".settings-form-group-body")?.hasAttribute("hidden"),
    ).toBe(true);
    fireEvent.click(screen.getByTestId("provider-advanced-toggle"));
    const advanced = screen.getByTestId("provider-advanced");
    expect(
      advanced.contains(
        await screen.findByTestId("provider-neuraldeep-domain"),
      ),
    ).toBe(true);

    // The compatible preset has no endpoint at all and Save waits for one, so
    // its field cannot be behind a fold.
    fireEvent.click(screen.getByTestId("provider-card-compatible"));
    const base = await screen.findByTestId("provider-api-base");
    expect(screen.getByTestId("provider-advanced").contains(base)).toBe(false);
    expect(screen.queryByTestId("provider-neuraldeep-domain")).toBeNull();
    expect(
      (screen.getByTestId("provider-save") as HTMLButtonElement).disabled,
    ).toBe(true);
  });

  it("shows the wait as a line where the button was", async () => {
    renderPicker();
    vi.spyOn(window, "open").mockImplementation(() => null);
    fireEvent.click(screen.getByTestId("provider-card-devin"));
    fireEvent.click(await screen.findByTestId("provider-sign-in"));
    // The sign-in is running: the actions row says so as a line of the same
    // prose as the rest of the dialog, not as a disabled button repeating
    // what the page above already asks for.
    const waiting = await screen.findByTestId("provider-signing-in");
    expect(waiting.tagName).toBe("P");
    expect(waiting.className).toContain("settings-field-desc");
  });

  it("fades each edge of the catalog by what lies past it", async () => {
    renderPicker();
    const grid = screen.getByTestId("provider-grid");
    // jsdom lays nothing out, so the scroller's numbers are given here.
    const scrollTo = (scrollTop: number) => {
      Object.defineProperties(grid, {
        scrollHeight: { value: 326, configurable: true },
        clientHeight: { value: 180, configurable: true },
        scrollTop: { value: scrollTop, configurable: true, writable: true },
      });
      fireEvent.scroll(grid);
    };
    const edges = () => [
      grid.style.getPropertyValue("--onboarding-fade-top"),
      grid.style.getPropertyValue("--onboarding-fade-bottom"),
    ];
    // At the top: nothing above, the whole catalog below.
    scrollTo(0);
    await waitFor(() => expect(edges()).toEqual(["0px", "14px"]));
    // A little way down, both edges have something past them.
    scrollTo(11);
    expect(edges()).toEqual(["11px", "14px"]);
    // In the middle both are full.
    scrollTo(73);
    expect(edges()).toEqual(["14px", "14px"]);
    // Near the end the bottom is only what is left to scroll.
    scrollTo(326 - 180 - 9);
    expect(edges()).toEqual(["14px", "9px"]);
    // At the end there is nothing below to hint at.
    scrollTo(326 - 180);
    expect(edges()).toEqual(["14px", "0px"]);
  });

  it("gives the action back to Save once a key is typed", async () => {
    renderPicker();
    // NeuralDeep signs in, so the sign-in is the action.
    expect(await screen.findByTestId("provider-sign-in")).toBeTruthy();
    // A key typed into the form is a decision: the person brought their own
    // credential and does not want the account flow.
    fireEvent.click(screen.getByTestId("provider-advanced-toggle"));
    fireEvent.change(await screen.findByTestId("provider-api-key"), {
      target: { value: "nd-my-own-key" },
    });
    await waitFor(() =>
      expect(screen.getByTestId("provider-save")).toBeTruthy(),
    );
    expect(screen.queryByTestId("provider-sign-in")).toBeNull();
    // Clearing it hands the action back to the sign-in.
    fireEvent.change(screen.getByTestId("provider-api-key"), {
      target: { value: "  " },
    });
    await waitFor(() =>
      expect(screen.getByTestId("provider-sign-in")).toBeTruthy(),
    );
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
    fireEvent.click(screen.getByTestId("provider-advanced-toggle"));
    await waitFor(() =>
      expect(
        screen
          .getByTestId("provider-advanced")
          .contains(screen.getByTestId("provider-api-key")),
      ).toBe(true),
    );
    // OpenAI has no sign-in, so its key is the form's own first field.
    fireEvent.click(screen.getByTestId("provider-card-openai"));
    // The row still holds the proxy for every provider; what moves is the key.
    await waitFor(() =>
      expect(
        screen
          .getByTestId("provider-advanced")
          .contains(screen.getByTestId("provider-api-key")),
      ).toBe(false),
    );
    expect(screen.getByTestId("provider-proxy")).toBeTruthy();
    // Codex carries no key at all.
    fireEvent.click(screen.getByTestId("provider-card-codex"));
    await waitFor(() =>
      expect(screen.queryByTestId("provider-api-key")).toBeNull(),
    );
  });

  it("folds the proxy away behind an Advanced settings fieldset", async () => {
    renderPicker();
    const toggle = await screen.findByTestId("provider-advanced-toggle");
    expect(toggle.textContent).toBe("Advanced settings");
    expect(toggle.getAttribute("aria-expanded")).toBe("false");
    const body = screen
      .getByTestId("provider-advanced")
      .querySelector(".settings-form-group-body")!;
    expect(body.hasAttribute("hidden")).toBe(true);
    // Mounted while folded, so what it holds survives the fold.
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
    fireEvent.click(screen.getByTestId("provider-card-devin"));
    fireEvent.click(await screen.findByTestId("provider-sign-in"));
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
    // The code and the link it belongs to are spent: "enter this code" beside
    // "signed in" read as a contradiction.
    expect(document.querySelector(".codex-auth-device")).toBeNull();
    expect(document.querySelector(".codex-auth-code")).toBeNull();
    expect(screen.queryByTestId("devin-callback-paste")).toBeNull();
    // And the action is Save again, not the sign-in or its wait.
    expect(screen.queryByTestId("provider-sign-in")).toBeNull();
    expect(screen.queryByTestId("provider-signing-in")).toBeNull();
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
    // Every card's description comes from the Russian dictionary, read from
    // it rather than spelled here: the copy is edited often, and a test that
    // repeats the words fails on every rewording without a defect behind it.
    for (const preset of PROVIDER_PRESETS) {
      const text = messagesRu[preset.descriptionKey];
      expect(text, preset.descriptionKey).toBeTruthy();
      expect(
        screen.getByTestId(`provider-card-${preset.id}`).textContent,
      ).toContain(text);
    }
    // The fold of the advanced fields is translated too.
    expect(screen.getByTestId("provider-advanced-toggle").textContent).toBe(
      "Расширенные настройки",
    );
  });

  it("waits for the base config before saving", async () => {
    // Save is clicked while GET /coddy/config is still in flight: the PUT
    // must carry the merged document, not a one-row one over the file.
    baseConfigDelay = 100;
    const onSaved = renderPicker();
    // Save is the action of a provider that takes a key; the one the dialog
    // opens on signs in instead.
    fireEvent.click(screen.getByTestId("provider-card-openai"));
    fireEvent.click(await screen.findByTestId("provider-save"));
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
    // Save is the action of a provider that takes a key; the default one
    // signs in first.
    fireEvent.click(screen.getByTestId("provider-card-openai"));
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
  it("shows one primary action, and which one depends on the provider", async () => {
    renderPicker();
    const actions = () =>
      Array.from(
        document.querySelectorAll<HTMLElement>(
          ".provider-picker-actions button",
        ),
      ).map((b) => b.dataset.testid);
    // A provider that signs in: the sign-in is the action, and it is not
    // drawn a second time beside its field.
    expect(actions()).toEqual(["provider-sign-in"]);
    expect(
      document.querySelectorAll(".provider-picker-form .settings-btn-primary"),
    ).toHaveLength(0);
    // A provider that takes a key: Save.
    fireEvent.click(screen.getByTestId("provider-card-openai"));
    await waitFor(() => expect(actions()).toEqual(["provider-save"]));
    expect(screen.queryByTestId("provider-test")).toBeNull();
    expect(screen.queryByTestId("provider-skip")).toBeNull();
  });
});

it("does not save a Kimi catalog after the selected variant changes during its request", async () => {
  initLocale("en");
  let answer!: (value: Response) => void;
  const pending = new Promise<Response>((resolve) => {
    answer = resolve;
  });
  const fetchMock = vi.fn(
    async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (url === "/coddy/config" && !init?.method)
        return new Response(
          JSON.stringify({
            providers: [{ name: "kimi", type: "kimi" }],
            models: [],
          }),
          { status: 200 },
        );
      if (url.includes("kimi-auth?"))
        return new Response(
          JSON.stringify({ connected: true, source: "cli" }),
          { status: 200 },
        );
      if (url === "/coddy/providers/models") return pending;
      if (url === "/coddy/config/validate")
        return new Response(JSON.stringify({ ok: true }), { status: 200 });
      if (url === "/coddy/config" && init?.method === "PUT")
        return new Response(JSON.stringify({ ok: true }), { status: 200 });
      return new Response("not found", { status: 404 });
    },
  );
  vi.stubGlobal("fetch", fetchMock);
  renderPicker();
  fireEvent.click(screen.getByTestId("provider-card-kimi"));
  await screen.findByText("Using Kimi CLI credentials");
  fireEvent.click(screen.getByTestId("provider-save"));
  await waitFor(() =>
    expect(
      fetchMock.mock.calls.some(
        ([url]) => String(url) === "/coddy/providers/models",
      ),
    ).toBe(true),
  );
  fireEvent.change(screen.getByTestId("kimi-variant"), {
    target: { value: "platform-global" },
  });
  answer(
    new Response(
      JSON.stringify({
        ok: true,
        models: [
          {
            id: "subscription-only",
            kimi_protocol: "kimi",
            kimi_image_input: true,
          },
        ],
      }),
      { status: 200 },
    ),
  );
  await waitFor(() =>
    expect(screen.getByTestId("provider-save")).not.toBeDisabled(),
  );
  expect(
    fetchMock.mock.calls.some(
      ([url, init]) => url === "/coddy/config" && init?.method === "PUT",
    ),
  ).toBe(false);
  cleanup();
  vi.unstubAllGlobals();
});
