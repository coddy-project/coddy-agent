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
  // The endpoint picker keeps its slot above the sign-in block.
  const base = screen.getByLabelText("API base URL") as HTMLSelectElement;
  expect(base.tagName).toBe("SELECT");
  expect(fetchMock).toHaveBeenCalledWith(
    "/coddy/providers/neuraldeep/neuraldeep-auth",
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
