import React from "react";
import { afterEach, expect, test, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { SettingsSection } from "./SettingsSection";
import type { JsonSchema } from "./SchemaForm";
import type { SectionDescriptor } from "./settingsSections";
import { messagesEn } from "../i18n/messages/en";
import { messagesRu } from "../i18n/messages/ru";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

const providersSection: SectionDescriptor = {
  id: "providers",
  label: "LLM providers",
  kind: "array",
  schemaKey: "providers",
  labelField: "name",
};

const rootSchema: JsonSchema = {
  type: "object",
  properties: {
    providers: {
      type: "array",
      title: "LLM providers",
      items: {
        type: "object",
        properties: {
          name: { type: "string", title: "Provider name" },
          type: {
            type: "string",
            title: "Provider type",
            enum: ["openai", "anthropic", "neuraldeep", "codex"],
          },
          api_base: { type: "string", title: "API base URL" },
          api_key: { type: "string", title: "API key" },
          api_key_command: { type: "string", title: "API key command" },
          proxy: { type: "string", title: "Proxy URL" },
        },
        "x-coddy-property-order": [
          "name",
          "type",
          "api_base",
          "api_key",
          "api_key_command",
          "proxy",
        ],
      },
    },
  },
};

function Harness(props: {
  provider?: Record<string, unknown>;
  /** Sees every document the section writes. */
  onDoc?: (doc: Record<string, unknown>) => void;
}) {
  const [doc, setDoc] = React.useState<Record<string, unknown>>({
    providers: [
      props.provider ?? {
        name: "neuraldeep",
        type: "neuraldeep",
        api_base: "",
        api_key: "",
      },
    ],
  });
  return (
    <SettingsSection
      section={providersSection}
      schema={rootSchema}
      doc={doc}
      setDoc={(next) => {
        props.onDoc?.(next);
        setDoc(next);
      }}
    />
  );
}

/** The proxy, the timeout and the credential command live in the folded
 * Advanced settings fieldset of a provider form; open it. */
function openAdvanced() {
  fireEvent.click(screen.getByTestId("settings-group-advanced-toggle"));
}

/** The proxy setting of the first provider row of a settings document. */
function firstProxy(doc: Record<string, unknown>): unknown {
  const rows = doc.providers as Record<string, unknown>[] | undefined;
  return rows?.[0]?.proxy;
}

test("NeuralDeep provider picks the API endpoint from the two official ones", async () => {
  render(<Harness />);
  fireEvent.click(screen.getByTestId("settings-master-item-0"));

  const base = screen.getByLabelText("API base URL") as HTMLSelectElement;
  await waitFor(() => {
    expect(base.value).toBe("https://api.neuraldeep.ru/v1");
  });
  expect([...base.options].map((o) => o.value)).toEqual([
    "https://api.neuraldeep.ru/v1",
    "https://api.neuraldeep.tech/v1",
  ]);
});

test("NeuralDeep provider stores the mirror endpoint when it is picked", async () => {
  render(<Harness />);
  fireEvent.click(screen.getByTestId("settings-master-item-0"));

  fireEvent.change(screen.getByLabelText("API base URL"), {
    target: { value: "https://api.neuraldeep.tech/v1" },
  });
  await waitFor(() => {
    expect(
      (screen.getByLabelText("API base URL") as HTMLSelectElement).value,
    ).toBe("https://api.neuraldeep.tech/v1");
  });
});

test("NeuralDeep provider flags a stored api_base that is not a NeuralDeep endpoint", async () => {
  render(
    <Harness
      provider={{
        name: "neuraldeep",
        type: "neuraldeep",
        api_base: "https://custom.example/v1",
        api_key: "",
      }}
    />,
  );
  fireEvent.click(screen.getByTestId("settings-master-item-0"));

  // The select shows the endpoint requests actually use, and the note explains
  // why the stored value is not it.
  const base = screen.getByLabelText("API base URL") as HTMLSelectElement;
  await waitFor(() => {
    expect(base.value).toBe("https://api.neuraldeep.ru/v1");
  });
  expect(document.body.textContent).toContain(
    "The saved api_base https://custom.example/v1 is not a NeuralDeep endpoint",
  );
});

test("Codex provider replaces API credentials with ChatGPT sign in", async () => {
  const fetchMock = vi.fn(async () => ({
    ok: true,
    json: async () => ({ connected: false, source: "" }),
  }));
  vi.stubGlobal("fetch", fetchMock);

  render(
    <Harness
      provider={{
        name: "codex",
        type: "codex",
        api_base: "https://must-not-be-shown.example",
        api_key: "must-not-be-shown",
        api_key_command: "must-not-be-shown",
      }}
    />,
  );
  fireEvent.click(screen.getByTestId("settings-master-item-0"));

  expect(await screen.findByTestId("codex-auth-sign-in")).toHaveTextContent(
    "Sign In with ChatGPT",
  );
  expect(screen.queryByLabelText("API base URL")).toBeNull();
  expect(screen.queryByLabelText("API key")).toBeNull();
  expect(screen.queryByLabelText("API key command")).toBeNull();
  expect(fetchMock).toHaveBeenCalledWith(
    "/coddy/providers/codex/codex-auth",
    expect.anything(),
  );
});

test("Codex Sign In opens ChatGPT and completes device authorization", async () => {
  const fetchMock = vi.fn(
    async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (init?.method === "POST") {
        return {
          ok: true,
          json: async () => ({
            login_id: "login-1",
            verification_url: "https://auth.openai.test/codex/device",
            user_code: "ABCD-EFGH",
            status: "pending",
          }),
        };
      }
      if (url.endsWith("/device/login-1")) {
        return {
          ok: true,
          json: async () => ({ status: "completed", connected: true }),
        };
      }
      return {
        ok: true,
        json: async () => ({ connected: false, source: "" }),
      };
    },
  );
  vi.stubGlobal("fetch", fetchMock);
  const openMock = vi.spyOn(window, "open").mockImplementation(() => null);

  render(<Harness provider={{ name: "codex", type: "codex" }} />);
  fireEvent.click(screen.getByTestId("settings-master-item-0"));
  fireEvent.click(await screen.findByTestId("codex-auth-sign-in"));

  expect(await screen.findByText("ABCD-EFGH")).toBeInTheDocument();
  expect(openMock).toHaveBeenCalledWith(
    "https://auth.openai.test/codex/device",
    "_blank",
    "noopener,noreferrer",
  );
  expect(
    await screen.findByText("Connected with ChatGPT.", {}, { timeout: 2000 }),
  ).toBeInTheDocument();
  // The server refuses a device start that is not JSON (a cross-site page can
  // only send the simple content types without a preflight).
  const start = fetchMock.mock.calls.find(
    ([input, init]) =>
      init?.method === "POST" && String(input).endsWith("codex-auth/device"),
  );
  expect(
    new Headers((start?.[1] as RequestInit | undefined)?.headers).get(
      "Content-Type",
    ),
  ).toBe("application/json");
});

test("NeuralDeep provider keeps the manual api_key and offers hub sign in", async () => {
  const fetchMock = vi.fn(async () => ({
    ok: true,
    json: async () => ({ connected: false, source: "none" }),
  }));
  vi.stubGlobal("fetch", fetchMock);

  render(<Harness />);
  fireEvent.click(screen.getByTestId("settings-master-item-0"));

  // The manual key entry stays available; sign-in is an alternative, not a
  // replacement (an explicit key would win over the login).
  expect(screen.getByLabelText("API key")).toBeInTheDocument();
  expect(
    await screen.findByTestId("neuraldeep-auth-sign-in"),
  ).toHaveTextContent("Sign In with NeuralDeep");
  // The endpoint picker keeps its slot above the sign-in block, and the
  // status is read for the endpoint it shows (the default when none is stored).
  const base = screen.getByLabelText("API base URL") as HTMLSelectElement;
  expect(base.tagName).toBe("SELECT");
  expect(fetchMock).toHaveBeenCalledWith(
    "/coddy/providers/neuraldeep/neuraldeep-auth?api_base=" +
      encodeURIComponent("https://api.neuraldeep.ru/v1"),
    expect.anything(),
  );
});

test("NeuralDeep Sign In opens the hub and completes device authorization", async () => {
  let approved = false;
  const fetchMock = vi.fn(
    async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (init?.method === "POST") {
        return {
          ok: true,
          json: async () => ({
            login_id: "login-nd",
            verification_url:
              "https://hub.neuraldeep.test/app/device?code=BCDF-2345",
            user_code: "BCDF-2345",
            status: "pending",
          }),
        };
      }
      if (url.endsWith("/device/login-nd")) {
        approved = true;
        return {
          ok: true,
          json: async () => ({ status: "completed", connected: true }),
        };
      }
      // The widget re-reads the stored status after completion so the masked
      // key is real, not a locally invented placeholder.
      return {
        ok: true,
        json: async () =>
          approved
            ? { connected: true, masked: "sk-nd…4321", source: "oauth" }
            : { connected: false, source: "none" },
      };
    },
  );
  vi.stubGlobal("fetch", fetchMock);
  const openMock = vi.spyOn(window, "open").mockImplementation(() => null);

  render(<Harness />);
  fireEvent.click(screen.getByTestId("settings-master-item-0"));
  fireEvent.click(await screen.findByTestId("neuraldeep-auth-sign-in"));

  expect(await screen.findByText("BCDF-2345")).toBeInTheDocument();
  expect(openMock).toHaveBeenCalledWith(
    "https://hub.neuraldeep.test/app/device?code=BCDF-2345",
    "_blank",
    "noopener,noreferrer",
  );
  expect(
    await screen.findByText(
      /Signed in to NeuralDeep \(sk-nd…4321\)/,
      {},
      { timeout: 2000 },
    ),
  ).toBeInTheDocument();
});

test("NeuralDeep Sign In carries the endpoint picked in the form", async () => {
  const fetchMock = vi.fn(
    async (input: RequestInfo | URL, init?: RequestInit) => {
      if (init?.method === "POST") {
        return {
          ok: true,
          json: async () => ({
            login_id: "login-mirror",
            verification_url:
              "https://hub.neuraldeep.tech/app/device?code=MRRR-0001",
            user_code: "MRRR-0001",
            status: "pending",
          }),
        };
      }
      if (String(input).includes("/device/")) {
        return {
          ok: true,
          json: async () => ({ status: "pending", connected: false }),
        };
      }
      return {
        ok: true,
        json: async () => ({ connected: false, source: "none" }),
      };
    },
  );
  vi.stubGlobal("fetch", fetchMock);
  vi.spyOn(window, "open").mockImplementation(() => null);

  render(<Harness />);
  fireEvent.click(screen.getByTestId("settings-master-item-0"));
  fireEvent.change(await screen.findByTestId("neuraldeep-api-base"), {
    target: { value: "https://api.neuraldeep.tech/v1" },
  });
  fireEvent.click(await screen.findByTestId("neuraldeep-auth-sign-in"));
  expect(await screen.findByText("MRRR-0001")).toBeInTheDocument();

  // The pick has not been saved, so the device start must carry it: the hub
  // that mints the key is decided by the endpoint, not by the saved row.
  // The provider form also posts the row to list its models, so pick the
  // device start by its URL.
  const start = fetchMock.mock.calls.find(
    ([input, init]) =>
      (init as RequestInit | undefined)?.method === "POST" &&
      String(input).includes("neuraldeep-auth/device"),
  );
  expect(start?.[0]).toBe("/coddy/providers/neuraldeep/neuraldeep-auth/device");
  expect(JSON.parse(String((start?.[1] as RequestInit).body))).toEqual({
    api_base: "https://api.neuraldeep.tech/v1",
  });
  expect(
    new Headers((start?.[1] as RequestInit).headers).get("Content-Type"),
  ).toBe("application/json");
});

test("NeuralDeep keeps polling a pending login when the endpoint changes", async () => {
  let polls = 0;
  const fetchMock = vi.fn(
    async (input: RequestInfo | URL, init?: RequestInit) => {
      const url = String(input);
      if (init?.method === "POST") {
        return {
          ok: true,
          json: async () => ({
            login_id: "login-pending",
            verification_url:
              "https://hub.neuraldeep.test/app/device?code=PEND-0001",
            user_code: "PEND-0001",
            status: "pending",
          }),
        };
      }
      if (url.includes("/device/login-pending")) {
        polls += 1;
        return {
          ok: true,
          json: async () => ({ status: "pending", connected: false }),
        };
      }
      return {
        ok: true,
        json: async () => ({ connected: false, source: "none" }),
      };
    },
  );
  vi.stubGlobal("fetch", fetchMock);
  vi.spyOn(window, "open").mockImplementation(() => null);

  render(<Harness />);
  fireEvent.click(screen.getByTestId("settings-master-item-0"));
  fireEvent.click(await screen.findByTestId("neuraldeep-auth-sign-in"));
  expect(await screen.findByText("PEND-0001")).toBeInTheDocument();
  await waitFor(() => expect(polls).toBeGreaterThan(0), { timeout: 2000 });

  // The endpoint pick changes while the hub wait is still running: the code
  // stays on screen and the poll goes on, instead of the widget forgetting
  // the login and sitting in "Waiting for NeuralDeep…" forever.
  const before = polls;
  fireEvent.change(screen.getByTestId("neuraldeep-api-base"), {
    target: { value: "https://api.neuraldeep.tech/v1" },
  });
  expect(screen.getByText("PEND-0001")).toBeInTheDocument();
  await waitFor(() => expect(polls).toBeGreaterThan(before), {
    timeout: 3000,
  });
  expect(screen.getByText("PEND-0001")).toBeInTheDocument();
});

test("NeuralDeep flags a stored login issued by the other deployment's hub", async () => {
  const fetchMock = vi.fn(async (input: RequestInfo | URL) => {
    const url = String(input);
    const forMirror = url.includes(
      encodeURIComponent("https://api.neuraldeep.tech/v1"),
    );
    return {
      ok: true,
      json: async () => ({
        connected: true,
        masked: "sk-nd…4321",
        source: "oauth",
        hub: "https://hub.neuraldeep.ru",
        endpoint_hub: forMirror
          ? "https://hub.neuraldeep.tech"
          : "https://hub.neuraldeep.ru",
      }),
    };
  });
  vi.stubGlobal("fetch", fetchMock);

  render(<Harness />);
  fireEvent.click(screen.getByTestId("settings-master-item-0"));
  expect(
    await screen.findByText(/Signed in to NeuralDeep \(sk-nd…4321\)/),
  ).toBeInTheDocument();
  // Login and endpoint agree: no complaint.
  expect(screen.queryByTestId("neuraldeep-auth-hub-mismatch")).toBeNull();

  // Picking the mirror re-reads the status for that endpoint and, since the
  // stored key came from the default hub, says the login will not be honored.
  fireEvent.change(screen.getByTestId("neuraldeep-api-base"), {
    target: { value: "https://api.neuraldeep.tech/v1" },
  });
  const note = await screen.findByTestId("neuraldeep-auth-hub-mismatch");
  expect(note).toHaveTextContent("https://hub.neuraldeep.ru");
  expect(note).toHaveTextContent("https://api.neuraldeep.tech/v1");
  expect(fetchMock).toHaveBeenCalledWith(
    "/coddy/providers/neuraldeep/neuraldeep-auth?api_base=" +
      encodeURIComponent("https://api.neuraldeep.tech/v1"),
    expect.anything(),
  );
});

test("NeuralDeep explicit api_key reports that it shadows the login", async () => {
  const fetchMock = vi.fn(async () => ({
    ok: true,
    json: async () => ({
      connected: true,
      masked: "sk-ab…1234",
      source: "api_key",
    }),
  }));
  vi.stubGlobal("fetch", fetchMock);

  render(
    <Harness
      provider={{
        name: "neuraldeep",
        type: "neuraldeep",
        api_base: "",
        api_key: "sk-manual",
      }}
    />,
  );
  fireEvent.click(screen.getByTestId("settings-master-item-0"));

  expect(
    await screen.findByTestId("neuraldeep-auth-shadowed"),
  ).toHaveTextContent("requests use it instead of this login");
  // The stored login is still displayed, masked.
  expect(screen.getByText(/sk-ab…1234/)).toBeInTheDocument();
});

test("NeuralDeep login shadowed by the NAME_API_KEY variable names that variable", async () => {
  // A row named "openai" (the one config.example.yaml ships, switched to
  // neuraldeep) reads OPENAI_API_KEY before the login. The api_key field is
  // empty, so advice to clear it would send the operator nowhere.
  const fetchMock = vi.fn(async () => ({
    ok: true,
    json: async () => ({
      connected: true,
      masked: "sk-ab…1234",
      source: "env",
    }),
  }));
  vi.stubGlobal("fetch", fetchMock);

  render(
    <Harness
      provider={{
        name: "openai",
        type: "neuraldeep",
        api_base: "",
        api_key: "",
      }}
    />,
  );
  fireEvent.click(screen.getByTestId("settings-master-item-0"));

  const note = await screen.findByTestId("neuraldeep-auth-shadowed");
  expect(note).toHaveTextContent("OPENAI_API_KEY");
  expect(note).not.toHaveTextContent("api_key field");
});

const modelsSection: SectionDescriptor = {
  id: "models",
  label: "Logical models",
  kind: "array",
  schemaKey: "models",
  labelField: "model",
};

const modelsSchema: JsonSchema = {
  type: "object",
  properties: {
    models: {
      type: "array",
      title: "Logical models",
      items: {
        type: "object",
        properties: {
          model: { type: "string", title: "Model id" },
        },
        "x-coddy-property-order": ["model"],
      },
    },
  },
};

const reasoningModelsSchema: JsonSchema = {
  type: "object",
  properties: {
    models: {
      type: "array",
      title: "Logical models",
      items: {
        type: "object",
        properties: {
          model: { type: "string", title: "Model id" },
          reasoning_levels: {
            type: "array",
            title: "Reasoning levels",
            items: { type: "string" },
          },
          // stream is the one model key whose absence means true, so it is seeded
          // from the schema default. Keeping it here pins that the item factory
          // omits reasoning_levels only, rather than everything it does not know.
          stream: { type: "boolean", title: "Stream responses", default: true },
        },
        "x-coddy-property-order": ["model", "reasoning_levels", "stream"],
      },
    },
  },
};

// stubModelsAndLevels answers both fetches the logical-model form makes, routed
// by URL: the provider model list behind "Fetch models" and the detected
// reasoning levels behind "Fetch reasoning levels".
function stubModelsAndLevels(levels: string[]) {
  const fetchMock = vi.fn(async (input: unknown) => {
    const url = String(input);
    if (url.startsWith("/coddy/config/reasoning-levels")) {
      return {
        ok: true,
        json: async () => ({
          ok: true,
          levels,
          detected: levels.length > 0,
        }),
      };
    }
    return {
      ok: true,
      json: async () => ({ ok: true, models: [{ id: "qwen3.8-27b" }] }),
    };
  });
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

function ReasoningModelsHarness() {
  const [doc, setDoc] = React.useState<Record<string, unknown>>({
    providers: [{ name: "valera", type: "openai" }],
    models: [],
  });
  return (
    <>
      <output data-testid="settings-doc">{JSON.stringify(doc)}</output>
      <SettingsSection
        section={modelsSection}
        schema={reasoningModelsSchema}
        doc={doc}
        setDoc={setDoc}
      />
    </>
  );
}

// addFetchedModel walks the form the way an operator does: Add, pick the
// provider, then type the id its API expects.
async function addFetchedModel() {
  fireEvent.click(screen.getByTestId("settings-master-add"));
  fireEvent.focus(screen.getByTestId("model-field-provider"));
  fireEvent.mouseDown(screen.getByText("valera"));
  fireEvent.change(screen.getByTestId("model-field-model"), {
    target: { value: "qwen3.8-27b" },
  });
}

function savedModels(): unknown {
  return JSON.parse(screen.getByTestId("settings-doc").textContent || "{}")
    .models;
}

test("adding a fetched model leaves reasoning auto-detection enabled", async () => {
  stubModelsAndLevels(["low", "medium", "high"]);

  render(<ReasoningModelsHarness />);
  await addFetchedModel();

  // reasoning_levels is absent (auto-detect), while stream keeps its schema
  // default: the item factory omits the one key whose empty value means
  // something, not every key it was not told about.
  expect(savedModels()).toEqual([
    { model: "valera/qwen3.8-27b", stream: true },
  ]);
});

test("fetch reasoning levels fills the field for the model being edited", async () => {
  const fetchMock = stubModelsAndLevels(["low", "medium", "high"]);

  render(<ReasoningModelsHarness />);
  await addFetchedModel();

  fireEvent.click(screen.getByTestId("reasoning-levels-fetch"));
  await waitFor(() =>
    expect(savedModels()).toEqual([
      {
        model: "valera/qwen3.8-27b",
        stream: true,
        reasoning_levels: ["low", "medium", "high"],
      },
    ]),
  );

  // The id typed into the form is what gets resolved, not a saved models[] row,
  // together with the type of the provider row it points at.
  expect(fetchMock).toHaveBeenCalledWith(
    "/coddy/config/reasoning-levels?model=valera%2Fqwen3.8-27b&provider_type=openai",
  );

  // And the operator can hand the decision back to the backend.
  fireEvent.click(screen.getByTestId("reasoning-levels-auto"));
  expect(savedModels()).toEqual([
    { model: "valera/qwen3.8-27b", stream: true },
  ]);
});

test("fetch reasoning levels sends the type of the provider row in the form", async () => {
  const fetchMock = stubModelsAndLevels(["low", "medium", "high"]);

  render(<ReasoningModelsHarness />);
  await addFetchedModel();
  fireEvent.click(screen.getByTestId("reasoning-levels-fetch"));

  // valera is an openai provider in the (unsaved) settings document, and that
  // is what decides the Codex remap server-side, not the config on disk.
  await waitFor(() =>
    expect(fetchMock).toHaveBeenCalledWith(
      "/coddy/config/reasoning-levels?model=valera%2Fqwen3.8-27b&provider_type=openai",
    ),
  );
});

test("a fetch that answers after the model row was removed does not bring it back", async () => {
  let settle: () => void = () => {};
  const fetchMock = vi.fn(async (input: unknown) => {
    const url = String(input);
    if (url.startsWith("/coddy/config/reasoning-levels")) {
      await new Promise<void>((r) => {
        settle = r;
      });
      return {
        ok: true,
        json: async () => ({
          ok: true,
          levels: ["low", "medium", "high"],
          detected: true,
        }),
      };
    }
    return {
      ok: true,
      json: async () => ({ ok: true, models: [{ id: "qwen3.8-27b" }] }),
    };
  });
  vi.stubGlobal("fetch", fetchMock);

  render(<ReasoningModelsHarness />);
  await addFetchedModel();
  fireEvent.click(screen.getByTestId("reasoning-levels-fetch"));

  // Back to the list, delete the row while the request is still in flight.
  fireEvent.click(screen.getByTestId("settings-detail-back"));
  fireEvent.click(
    screen.getByRole("button", { name: /Remove valera\/qwen3\.8-27b/ }),
  );
  expect(savedModels()).toEqual([]);

  settle();
  await new Promise((r) => setTimeout(r, 0));
  // The stale answer must not re-create the deleted entry through the
  // captured array callback.
  expect(savedModels()).toEqual([]);
});

test("a fetch that answers after a sibling field changed keeps that change", async () => {
  let settle: () => void = () => {};
  const fetchMock = vi.fn(async (input: unknown) => {
    const url = String(input);
    if (url.startsWith("/coddy/config/reasoning-levels")) {
      await new Promise<void>((r) => {
        settle = r;
      });
      return {
        ok: true,
        json: async () => ({
          ok: true,
          levels: ["low", "medium", "high"],
          detected: true,
        }),
      };
    }
    return {
      ok: true,
      json: async () => ({ ok: true, models: [{ id: "qwen3.8-27b" }] }),
    };
  });
  vi.stubGlobal("fetch", fetchMock);

  render(<ReasoningModelsHarness />);
  await addFetchedModel();
  fireEvent.click(screen.getByTestId("reasoning-levels-fetch"));

  // While the request is in flight the operator turns streaming off.
  fireEvent.click(screen.getByRole("switch", { name: /Stream responses/ }));
  expect(savedModels()).toEqual([
    { model: "valera/qwen3.8-27b", stream: false },
  ]);

  settle();
  await waitFor(() =>
    expect(
      (savedModels() as Array<Record<string, unknown>>)[0]?.[
        "reasoning_levels"
      ],
    ).toEqual(["low", "medium", "high"]),
  );
  // The answer must land on the entry as it is now, not on the snapshot the
  // form held when Fetch was pressed - that snapshot still has stream: true.
  expect(savedModels()).toEqual([
    {
      model: "valera/qwen3.8-27b",
      stream: false,
      reasoning_levels: ["low", "medium", "high"],
    },
  ]);
});

test("a model id with no reasoning family is left without an override", async () => {
  stubModelsAndLevels([]);

  render(<ReasoningModelsHarness />);
  await addFetchedModel();

  fireEvent.click(screen.getByTestId("reasoning-levels-fetch"));
  await waitFor(() =>
    expect(screen.getByTestId("reasoning-levels-status").textContent).toContain(
      "no auto-detected reasoning levels",
    ),
  );
  // An empty list here would hide the composer's reasoning selector for good.
  expect(savedModels()).toEqual([
    { model: "valera/qwen3.8-27b", stream: true },
  ]);
});

test("renaming the sole model id follows through to agent.model", async () => {
  function ModelsHarness() {
    const [doc, setDoc] = React.useState<Record<string, unknown>>({
      providers: [{ name: "neuraldeep", type: "neuraldeep" }],
      models: [{ model: "neuraldeep/gpt-120b-oss" }],
      agent: { model: "neuraldeep/gpt-120b-oss", max_turns: 20 },
    });
    return (
      <>
        <span data-testid="agent-model">
          {String((doc.agent as Record<string, unknown>).model)}
        </span>
        <SettingsSection
          section={modelsSection}
          schema={modelsSchema}
          doc={doc}
          setDoc={setDoc}
        />
      </>
    );
  }

  render(<ModelsHarness />);
  fireEvent.click(screen.getByTestId("settings-master-item-0"));

  // The id field holds the part after the provider; retyping it recomposes
  // provider/id.
  const model = screen.getByTestId("model-field-model") as HTMLInputElement;
  expect(model.value).toBe("gpt-120b-oss");
  fireEvent.change(model, { target: { value: "qwen-3.6" } });

  // The ReAct default-model reference tracked the rename automatically.
  await waitFor(() => {
    expect(screen.getByTestId("agent-model").textContent).toBe(
      "neuraldeep/qwen-3.6",
    );
  });
});

test("switching type away from NeuralDeep restores the previously entered API base", async () => {
  render(
    <Harness
      provider={{
        name: "custom",
        type: "openai",
        api_base: "https://custom.example/v1",
        api_key: "",
      }}
    />,
  );
  fireEvent.click(screen.getByTestId("settings-master-item-0"));

  // openai: the field is editable and shows the entered value.
  let base = screen.getByLabelText("API base URL") as HTMLInputElement;
  expect(base.readOnly).toBe(false);
  expect(base.value).toBe("https://custom.example/v1");

  // Switch to neuraldeep: the field becomes the endpoint picker showing the
  // endpoint requests use, and the stored value is not overwritten.
  const type = screen.getByLabelText("Provider type") as HTMLInputElement;
  fireEvent.change(type, { target: { value: "neuraldeep" } });
  await waitFor(() => {
    expect(screen.getByLabelText("API base URL").tagName).toBe("SELECT");
  });
  expect(
    (screen.getByLabelText("API base URL") as HTMLSelectElement).value,
  ).toBe("https://api.neuraldeep.ru/v1");

  // Switch back to openai: the original value is restored.
  fireEvent.change(type, { target: { value: "openai" } });
  base = screen.getByLabelText("API base URL") as HTMLInputElement;
  await waitFor(() => {
    expect(base.readOnly).toBe(false);
  });
  expect(base.value).toBe("https://custom.example/v1");
});

// The Subagents tab is hybrid: the form edits the config document, and the
// catalog below it is asked about the workspace of the session on screen.
test("the subagents tab keeps its form and asks the catalog about the session workspace", async () => {
  const calls: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn().mockImplementation((url: string) => {
      calls.push(String(url));
      return Promise.resolve({
        ok: true,
        json: async () => ({
          workspace: "/work/repo",
          policy: "ask",
          items: [],
        }),
      });
    }),
  );
  const setDoc = vi.fn();
  render(
    <SettingsSection
      section={{
        id: "subagents",
        label: "Subagents",
        kind: "subagents",
        schemaKey: "subagents",
      }}
      schema={{
        type: "object",
        properties: {
          subagents: {
            type: "object",
            title: "Subagents",
            properties: { max_depth: { type: "integer", title: "Max depth" } },
          },
        },
      }}
      doc={{ subagents: { max_depth: 1 } }}
      setDoc={setDoc}
      workspacePath="/work/repo"
    />,
  );

  expect(await screen.findByTestId("subagents-catalog")).toBeInTheDocument();
  expect(calls).toEqual(["/coddy/subagents?cwd=%2Fwork%2Frepo"]);
  fireEvent.change(screen.getByLabelText("Max depth"), {
    target: { value: "2" },
  });
  expect(setDoc).toHaveBeenCalledWith({ subagents: { max_depth: 2 } });
});

test("the Ignore system proxy switch saves none and brings the URL back when turned off", () => {
  let doc: Record<string, unknown> = {};
  render(
    <Harness
      provider={{
        name: "corp",
        type: "openai",
        proxy: "http://127.0.0.1:3128",
      }}
      onDoc={(next) => {
        doc = next;
      }}
    />,
  );
  fireEvent.click(screen.getByTestId("settings-master-item-0"));
  openAdvanced();

  const direct = screen.getByRole("switch", { name: "Ignore system proxy" });
  const url = screen.getByLabelText("Proxy URL") as HTMLInputElement;
  expect(direct).toHaveAttribute("aria-checked", "false");
  expect(url.value).toBe("http://127.0.0.1:3128");

  fireEvent.click(direct);
  expect(firstProxy(doc)).toBe("none");
  expect(direct).toHaveAttribute("aria-checked", "true");
  expect(url).toBeDisabled();
  expect(url.placeholder).toBe("Direct connection");

  fireEvent.click(direct);
  expect(firstProxy(doc)).toBe("http://127.0.0.1:3128");
  expect(url).not.toBeDisabled();
  expect(url.value).toBe("http://127.0.0.1:3128");
});

test("a provider saved as none shows the switch on and follows the system proxy once it is off", () => {
  let doc: Record<string, unknown> = {};
  render(
    <Harness
      provider={{ name: "local", type: "openai", proxy: "none" }}
      onDoc={(next) => {
        doc = next;
      }}
    />,
  );
  fireEvent.click(screen.getByTestId("settings-master-item-0"));
  openAdvanced();

  const direct = screen.getByRole("switch", { name: "Ignore system proxy" });
  const url = screen.getByLabelText("Proxy URL") as HTMLInputElement;
  expect(direct).toHaveAttribute("aria-checked", "true");
  expect(url).toBeDisabled();
  expect(url.value).toBe("");

  fireEvent.click(direct);
  expect(firstProxy(doc)).toBe("");
  expect(url.placeholder).toBe("Follows the system proxy");
});

test("a typed proxy URL replaces the system proxy and clearing it follows the system proxy again", () => {
  let doc: Record<string, unknown> = {};
  render(
    <Harness
      provider={{ name: "corp", type: "anthropic" }}
      onDoc={(next) => {
        doc = next;
      }}
    />,
  );
  fireEvent.click(screen.getByTestId("settings-master-item-0"));
  openAdvanced();

  const url = screen.getByLabelText("Proxy URL") as HTMLInputElement;
  expect(url.value).toBe("");
  expect(url.placeholder).toBe("Follows the system proxy");

  fireEvent.change(url, { target: { value: "socks5h://127.0.0.1:1080" } });
  expect(firstProxy(doc)).toBe("socks5h://127.0.0.1:1080");
  expect(
    screen.getByRole("switch", { name: "Ignore system proxy" }),
  ).toHaveAttribute("aria-checked", "false");

  fireEvent.change(url, { target: { value: "" } });
  expect(firstProxy(doc)).toBe("");
});

test("an explicit inherit reads as the system proxy and survives a round trip through the switch", () => {
  let doc: Record<string, unknown> = {};
  render(
    <Harness
      provider={{ name: "corp", type: "openai", proxy: "Inherit" }}
      onDoc={(next) => {
        doc = next;
      }}
    />,
  );
  fireEvent.click(screen.getByTestId("settings-master-item-0"));
  openAdvanced();

  const direct = screen.getByRole("switch", { name: "Ignore system proxy" });
  const url = screen.getByLabelText("Proxy URL") as HTMLInputElement;
  expect(direct).toHaveAttribute("aria-checked", "false");
  expect(url.value).toBe("");

  fireEvent.click(direct);
  expect(firstProxy(doc)).toBe("none");
  fireEvent.click(direct);
  expect(firstProxy(doc)).toBe("Inherit");
});

test("a Codex provider keeps the proxy setting next to its ChatGPT sign in", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => ({
      ok: true,
      json: async () => ({ connected: false, source: "" }),
    })),
  );
  render(
    <Harness provider={{ name: "codex", type: "codex", proxy: "none" }} />,
  );
  fireEvent.click(screen.getByTestId("settings-master-item-0"));
  openAdvanced();

  expect(await screen.findByTestId("codex-auth-sign-in")).toBeInTheDocument();
  expect(
    screen.getByRole("switch", { name: "Ignore system proxy" }),
  ).toHaveAttribute("aria-checked", "true");
});

test("turning the switch off after none was pasted into the field brings back the URL typed before it", () => {
  let doc: Record<string, unknown> = {};
  render(
    <Harness
      provider={{ name: "corp", type: "openai", proxy: "http://a:3128" }}
      onDoc={(next) => {
        doc = next;
      }}
    />,
  );
  fireEvent.click(screen.getByTestId("settings-master-item-0"));
  openAdvanced();

  const url = screen.getByLabelText("Proxy URL") as HTMLInputElement;
  fireEvent.change(url, { target: { value: "http://b:3128" } });
  fireEvent.change(url, { target: { value: "none" } });
  const direct = screen.getByRole("switch", { name: "Ignore system proxy" });
  expect(direct).toHaveAttribute("aria-checked", "true");

  fireEvent.click(direct);
  expect(firstProxy(doc)).toBe("http://b:3128");
});

test("renaming the provider while it connects directly keeps the URL the switch brings back", () => {
  let doc: Record<string, unknown> = {};
  render(
    <Harness
      provider={{ name: "corp", type: "openai", proxy: "http://a:3128" }}
      onDoc={(next) => {
        doc = next;
      }}
    />,
  );
  fireEvent.click(screen.getByTestId("settings-master-item-0"));
  openAdvanced();

  fireEvent.click(screen.getByRole("switch", { name: "Ignore system proxy" }));
  expect(firstProxy(doc)).toBe("none");
  fireEvent.change(screen.getByLabelText("Provider id"), {
    target: { value: "corp2" },
  });
  fireEvent.click(screen.getByRole("switch", { name: "Ignore system proxy" }));
  expect(firstProxy(doc)).toBe("http://a:3128");
});

const gatewaysSection: SectionDescriptor = {
  id: "gateways",
  label: "Gateways",
  kind: "object",
  schemaKey: "gateways",
};

const gatewaysRootSchema: JsonSchema = {
  type: "object",
  properties: {
    gateways: {
      type: "object",
      title: "Messenger gateways",
      properties: {
        telegram: {
          type: "object",
          title: "Telegram",
          properties: {
            enable: { type: "boolean", title: "Enabled" },
            token: { type: "string", title: "Bot token" },
            proxy: { type: "string", title: "Proxy" },
          },
          "x-coddy-property-order": ["enable", "token", "proxy"],
        },
      },
    },
  },
} as unknown as JsonSchema;

function GatewaysHarness(props: {
  proxy?: string;
  onDoc: (doc: Record<string, unknown>) => void;
}) {
  const [doc, setDoc] = React.useState<Record<string, unknown>>({
    gateways: {
      telegram: {
        enable: true,
        token: "123:abc",
        ...(props.proxy === undefined ? {} : { proxy: props.proxy }),
      },
    },
  });
  return (
    <SettingsSection
      section={gatewaysSection}
      schema={gatewaysRootSchema}
      doc={doc}
      setDoc={(next) => {
        props.onDoc(next);
        setDoc(next);
      }}
    />
  );
}

function telegramProxy(doc: Record<string, unknown>): unknown {
  const gateways = doc.gateways as Record<string, unknown> | undefined;
  const telegram = gateways?.telegram as Record<string, unknown> | undefined;
  return telegram?.proxy;
}

test("the Telegram proxy has the Ignore system proxy switch too", () => {
  let doc: Record<string, unknown> = {};
  render(
    <GatewaysHarness
      proxy="socks5h://127.0.0.1:1080"
      onDoc={(next) => {
        doc = next;
      }}
    />,
  );

  const direct = screen.getByRole("switch", { name: "Ignore system proxy" });
  const url = screen.getByLabelText("Proxy URL") as HTMLInputElement;
  expect(url.value).toBe("socks5h://127.0.0.1:1080");

  fireEvent.click(direct);
  expect(telegramProxy(doc)).toBe("none");
  expect(url).toBeDisabled();
  // The switch's description names whose requests go direct; it is the (i)
  // beside the switch label.
  const hint = direct
    .closest(".settings-switch-field")!
    .querySelector(".field-hint")!;
  fireEvent.mouseEnter(hint);
  expect(screen.getByRole("tooltip").textContent).toContain(
    "the bot's requests ignore",
  );
  fireEvent.mouseLeave(hint);

  fireEvent.click(direct);
  expect(telegramProxy(doc)).toBe("socks5h://127.0.0.1:1080");
});

test("the proxy copy says an empty value follows the system proxy, in every language", () => {
  // Sentence by sentence: the one about an empty field names HTTPS_PROXY
  // and never promises a direct connection.
  for (const [messages, empty, direct] of [
    [messagesEn, /empty/i, /direct/i],
    [messagesRu, /пуст/i, /прям/i],
  ] as const) {
    for (const key of [
      "settings.schema.providers.proxy.desc",
      "settings.schema.gateways.telegram.proxy.desc",
    ]) {
      const sentences = (messages[key] ?? "").split(/[.;]/);
      const aboutEmpty = sentences.filter((s) => empty.test(s));
      expect(aboutEmpty.length, key).toBeGreaterThan(0);
      for (const sentence of aboutEmpty) {
        expect(sentence, key).toContain("HTTPS_PROXY");
        expect(sentence, key).not.toMatch(direct);
      }
    }
  }
});

// The provider form closes with the list of models the provider advertises,
// fetched with the row as the form holds it; one click files an id under
// Logical models in the same unsaved document, seeded like the Add button
// seeds a row there.
test("the provider form lists the advertised models and adds one to Logical models", async () => {
  const fetchMock = vi.fn(async (_input: unknown, _init?: RequestInit) => ({
    ok: true,
    json: async () => ({
      ok: true,
      models: [{ id: "m1", context_window: 131072 }, { id: "m2" }],
    }),
  }));
  vi.stubGlobal("fetch", fetchMock);
  let latest: Record<string, unknown> = {};
  render(
    <Harness
      provider={{ name: "demo", type: "openai", api_key: "sk-unsaved" }}
      onDoc={(next) => {
        latest = next;
      }}
    />,
  );

  fireEvent.click(screen.getByTestId("settings-master-item-0"));
  fireEvent.click(await screen.findByTestId("provider-model-toggle-m1"));

  const post = fetchMock.mock.calls.find(
    ([input, init]) =>
      String(input) === "/coddy/providers/models" &&
      (init as RequestInit | undefined)?.method === "POST",
  );
  expect(JSON.parse(String((post?.[1] as RequestInit).body))).toMatchObject({
    name: "demo",
    type: "openai",
    api_key: "sk-unsaved",
  });
  // The harness schema has no max_context_tokens, so only the id is seeded;
  // the real schema also receives the reported window (next test).
  const models = latest.models as { model: string }[] | undefined;
  expect(models?.[0]?.model).toBe("demo/m1");
  // The row the list added is checked on the next render, and unchecking it
  // takes the model out of the document again.
  await waitFor(() =>
    expect(
      screen
        .getByTestId("provider-model-toggle-m1")
        .getAttribute("aria-pressed"),
    ).toBe("true"),
  );
  fireEvent.click(screen.getByTestId("provider-model-toggle-m1"));
  expect(latest.models).toEqual([]);
});

test("the usage panel switch shows only for a provider type with a usage source", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => ({
      ok: true,
      json: async () => ({
        connected: false,
        source: "",
        ok: true,
        models: [],
      }),
    })),
  );
  const schema: JsonSchema = {
    type: "object",
    properties: {
      providers: {
        type: "array",
        title: "LLM providers",
        items: {
          type: "object",
          properties: {
            name: { type: "string", title: "Provider name" },
            type: {
              type: "string",
              title: "Provider type",
              enum: ["openai", "codex"],
            },
            usage_limits_panel: {
              type: "boolean",
              title: "Usage limits panel",
              default: true,
            },
          },
          "x-coddy-property-order": ["name", "type", "usage_limits_panel"],
        },
      },
    },
  };
  function TypedHarness(props: { type: string }) {
    const [doc, setDoc] = React.useState<Record<string, unknown>>({
      providers: [{ name: "p", type: props.type }],
    });
    return (
      <SettingsSection
        section={providersSection}
        schema={schema}
        doc={doc}
        setDoc={setDoc}
      />
    );
  }

  const { unmount } = render(<TypedHarness type="openai" />);
  fireEvent.click(screen.getByTestId("settings-master-item-0"));
  expect(
    screen.queryByRole("switch", { name: "Usage limits panel" }),
  ).toBeNull();
  unmount();

  render(<TypedHarness type="codex" />);
  fireEvent.click(screen.getByTestId("settings-master-item-0"));
  expect(
    await screen.findByRole("switch", { name: "Usage limits panel" }),
  ).toBeTruthy();
});

// The list and the row form are one component for every array tab. Without a
// key per section, a row form opened under LLM providers stayed on screen
// when the operator switched to Logical models, showing the first model's
// form instead of the model list.
test("a row form open under LLM providers does not survive switching to Logical models", () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => ({
      ok: true,
      json: async () => ({ ok: true, models: [] }),
    })),
  );
  const schema: JsonSchema = {
    type: "object",
    properties: {
      providers: rootSchema.properties!.providers!,
      models: {
        type: "array",
        title: "Logical models",
        items: {
          type: "object",
          properties: { model: { type: "string", title: "Model id" } },
        },
      },
    },
  };
  const modelsDescriptor: SectionDescriptor = {
    id: "models",
    label: "Logical models",
    kind: "array",
    schemaKey: "models",
    labelField: "model",
  };
  function TabsHarness(props: { section: SectionDescriptor }) {
    const [doc, setDoc] = React.useState<Record<string, unknown>>({
      providers: [{ name: "demo", type: "openai" }],
      models: [{ model: "demo/m1" }],
    });
    return (
      <SettingsSection
        section={props.section}
        schema={schema}
        doc={doc}
        setDoc={setDoc}
      />
    );
  }

  const { rerender } = render(<TabsHarness section={providersSection} />);
  fireEvent.click(screen.getByTestId("settings-master-item-0"));
  expect(screen.getByTestId("settings-detail-back")).toBeTruthy();

  rerender(<TabsHarness section={modelsDescriptor} />);
  expect(screen.queryByTestId("settings-detail-back")).toBeNull();
  expect(screen.getByTestId("settings-master-item-0").textContent).toBe(
    "demo/m1",
  );
});

// A model added from the provider's list arrives with the context window the
// provider reported for it, written into max_context_tokens.
test("a model added from the provider list carries the context window the provider reports", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => ({
      ok: true,
      json: async () => ({
        ok: true,
        models: [{ id: "m1", context_window: 131072 }],
      }),
    })),
  );
  const schema: JsonSchema = {
    type: "object",
    properties: {
      providers: rootSchema.properties!.providers!,
      models: {
        type: "array",
        title: "Logical models",
        items: {
          type: "object",
          properties: {
            model: { type: "string", title: "Model id" },
            max_context_tokens: {
              type: "integer",
              title: "Context window (tokens)",
            },
            reasoning_levels: {
              type: "array",
              title: "Reasoning levels",
              items: { type: "string" },
            },
          },
          "x-coddy-property-order": [
            "model",
            "max_context_tokens",
            "reasoning_levels",
          ],
        },
      },
    },
  };
  let latest: Record<string, unknown> = {};
  function Seeded() {
    const [doc, setDoc] = React.useState<Record<string, unknown>>({
      providers: [{ name: "demo", type: "openai" }],
      models: [],
    });
    return (
      <SettingsSection
        section={providersSection}
        schema={schema}
        doc={doc}
        setDoc={(next) => {
          latest = next;
          setDoc(next);
        }}
      />
    );
  }

  render(<Seeded />);
  fireEvent.click(screen.getByTestId("settings-master-item-0"));
  fireEvent.click(await screen.findByTestId("provider-model-toggle-m1"));

  expect(latest.models).toEqual([
    { model: "demo/m1", max_context_tokens: 131072 },
  ]);
});

// The provider form is three blocks: Provider settings, Advanced settings
// (folded until its legend is clicked: the credential command, the proxy and
// the timeout) and the Models list.
test("the provider form groups its fields: provider settings, folded advanced settings, models", () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => ({
      ok: true,
      json: async () => ({ ok: true, models: [] }),
    })),
  );
  render(<Harness provider={{ name: "demo", type: "openai", api_key: "k" }} />);
  fireEvent.click(screen.getByTestId("settings-master-item-0"));

  const main = screen.getByTestId("settings-group-provider");
  const advanced = screen.getByTestId("settings-group-advanced");
  expect(main.querySelector("legend")?.textContent).toBe("Provider settings");
  expect(main.querySelector('[aria-label="Provider id"]')).not.toBeNull();
  expect(main.querySelector('[aria-label="API key"]')).not.toBeNull();
  expect(main.querySelector('[aria-label="API key command"]')).toBeNull();

  const toggle = screen.getByTestId("settings-group-advanced-toggle");
  expect(toggle.textContent).toBe("Advanced settings");
  expect(toggle.getAttribute("aria-expanded")).toBe("false");
  const body = advanced.querySelector(".settings-form-group-body")!;
  expect(body.hasAttribute("hidden")).toBe(true);
  expect(body.querySelector('[aria-label="API key command"]')).not.toBeNull();
  expect(
    body.querySelector('[data-testid="proxy-setting-url"]'),
  ).not.toBeNull();

  fireEvent.click(toggle);
  expect(toggle.getAttribute("aria-expanded")).toBe("true");
  expect(body.hasAttribute("hidden")).toBe(false);

  // Blocks in order, the models list last.
  const blocks = [
    ...document.querySelectorAll(
      '[data-testid="settings-group-provider"], [data-testid="settings-group-advanced"], [data-testid="provider-models"]',
    ),
  ].map((el) => el.getAttribute("data-testid"));
  expect(blocks).toEqual([
    "settings-group-provider",
    "settings-group-advanced",
    "provider-models",
  ]);
});

// A logical model's form reads in three blocks: which model it is and what it
// takes in, how it answers, and how hard it thinks.
test("the logical model form groups its fields: model, generation, reasoning", async () => {
  stubModelsAndLevels([]);
  const schema: JsonSchema = {
    type: "object",
    properties: {
      models: {
        type: "array",
        title: "Logical models",
        items: {
          type: "object",
          properties: {
            model: { type: "string", title: "Model id" },
            max_tokens: { type: "integer", title: "Max tokens" },
            temperature: { type: "number", title: "Temperature" },
            max_context_tokens: {
              type: "integer",
              title: "Context window (tokens)",
            },
            multimodal: { type: "boolean", title: "Multimodal" },
            stream: { type: "boolean", title: "Stream responses" },
            reasoning_levels: {
              type: "array",
              title: "Reasoning levels",
              items: { type: "string" },
            },
            reasoning_default: {
              type: "string",
              title: "Default reasoning level",
            },
          },
          "x-coddy-property-order": [
            "model",
            "max_tokens",
            "temperature",
            "max_context_tokens",
            "multimodal",
            "stream",
            "reasoning_levels",
            "reasoning_default",
          ],
        },
      },
    },
  } as JsonSchema;
  render(
    <SettingsSection
      section={modelsSection}
      schema={schema}
      doc={{ providers: [{ name: "demo" }], models: [{ model: "demo/m1" }] }}
      setDoc={() => {}}
    />,
  );
  fireEvent.click(screen.getByTestId("settings-master-item-0"));

  const legends = [
    ...document.querySelectorAll(".settings-schema-root > fieldset > legend"),
  ].map((l) => l.textContent);
  expect(legends).toEqual(["Model", "Generation", "Reasoning"]);
  const model = screen.getByTestId("settings-group-model");
  expect(model.textContent).toContain("Model id");
  expect(model.textContent).toContain("Context window (tokens)");
  expect(model.textContent).toContain("Multimodal");
  const generation = screen.getByTestId("settings-group-generation");
  expect(generation.textContent).toContain("Max tokens");
  expect(generation.textContent).toContain("Temperature");
  expect(generation.textContent).toContain("Stream responses");
  const reasoning = screen.getByTestId("settings-group-reasoning");
  expect(reasoning.textContent).toContain("Reasoning levels");
  expect(reasoning.textContent).toContain("Default reasoning level");
});
