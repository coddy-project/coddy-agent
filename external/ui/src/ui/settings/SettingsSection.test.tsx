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
        },
        "x-coddy-property-order": [
          "name",
          "type",
          "api_base",
          "api_key",
          "api_key_command",
        ],
      },
    },
  },
};

function Harness(props: { provider?: Record<string, unknown> }) {
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
      setDoc={setDoc}
    />
  );
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
  const start = fetchMock.mock.calls.find(
    ([, init]) => (init as RequestInit | undefined)?.method === "POST",
  );
  expect(start?.[0]).toBe(
    "/coddy/providers/neuraldeep/neuraldeep-auth/device",
  );
  expect(JSON.parse(String((start?.[1] as RequestInit).body))).toEqual({
    api_base: "https://api.neuraldeep.tech/v1",
  });
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
            verification_url: "https://hub.neuraldeep.test/app/device?code=PEND-0001",
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

// addFetchedModel walks the form the way an operator does: Add, Fetch models,
// then pick the fetched id out of the combobox.
async function addFetchedModel() {
  fireEvent.click(screen.getByTestId("settings-master-add"));
  fireEvent.click(screen.getByTestId("model-field-fetch"));
  await waitFor(() =>
    expect(screen.getByTestId("model-field-fetch").textContent).toBe(
      "Fetch models",
    ),
  );
  fireEvent.focus(screen.getByTestId("model-field-model"));
  fireEvent.mouseDown(await screen.findByText("valera/qwen3.8-27b"));
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

  const model = screen.getByTestId("model-field-model") as HTMLInputElement;
  expect(model.value).toBe("neuraldeep/gpt-120b-oss");
  fireEvent.change(model, { target: { value: "neuraldeep/qwen-3.6" } });

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
