import React from "react";
import { afterEach, expect, test, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { SettingsSection } from "./SettingsSection";
import type { JsonSchema } from "./SchemaForm";
import type { SectionDescriptor } from "./settingsSections";
import { setLocale } from "../i18n/i18n";

afterEach(() => {
  cleanup();
  setLocale("en");
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
});

// The schema the server serves for these two sections (internal/config
// UISchemaMap), cut to the keys the sharing and the coddy provider touch.
const ALIAS_PATTERN = "^$|^[A-Za-z0-9][A-Za-z0-9._\\-]{0,63}$";

const schema: JsonSchema = {
  type: "object",
  properties: {
    providers: {
      type: "array",
      title: "LLM providers",
      items: {
        type: "object",
        properties: {
          name: { type: "string", title: "Provider id" },
          type: {
            type: "string",
            title: "Provider type",
            enum: ["openai", "anthropic", "neuraldeep", "codex", "coddy"],
          },
          api_base: { type: "string", title: "API base URL" },
          api_key: { type: "string", title: "API key" },
          api_key_command: { type: "string", title: "API key command" },
          proxy: { type: "string", title: "Proxy URL" },
          timeout_ms: { type: "integer", title: "Request timeout ms" },
          busy_wait_ms: { type: "integer", title: "Wait for a free slot ms" },
          ca_file: { type: "string", title: "CA file" },
          client_cert_file: { type: "string", title: "Client certificate" },
          client_key_file: { type: "string", title: "Client key" },
          usage_limits_panel: {
            type: "boolean",
            title: "Usage limits panel",
            default: true,
          },
        },
        "x-coddy-property-order": [
          "name",
          "type",
          "api_base",
          "api_key",
          "api_key_command",
          "proxy",
          "timeout_ms",
          "busy_wait_ms",
          "ca_file",
          "client_cert_file",
          "client_key_file",
          "usage_limits_panel",
        ],
      },
    },
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
          multimodal: { type: "boolean", title: "Multimodal", default: false },
          reasoning_levels: {
            type: "array",
            title: "Reasoning levels",
            items: { type: "string" },
          },
          reasoning_default: {
            type: "string",
            title: "Default reasoning level",
          },
          allow_reasoning_off: {
            type: "boolean",
            title: "Allow disabling reasoning",
            default: false,
          },
          stream: { type: "boolean", title: "Stream responses", default: true },
          shared_as: {
            type: "string",
            title: "Shared as",
            pattern: ALIAS_PATTERN,
          },
          shared_subscription_ack: {
            type: "boolean",
            title: "I accept sharing a subscription login",
            default: false,
          },
        },
        "x-coddy-property-order": [
          "model",
          "max_context_tokens",
          "multimodal",
          "reasoning_levels",
          "reasoning_default",
          "allow_reasoning_off",
          "stream",
          "shared_as",
          "shared_subscription_ack",
        ],
      },
    },
    agent: {
      type: "object",
      title: "ReAct loop",
      properties: {
        model: { type: "string", title: "Default model" },
        wait_for_limit_reset: {
          type: "boolean",
          title: "Wait for limit reset",
        },
        shared_busy_wait_ms: {
          type: "integer",
          title: "Shared model busy wait ms",
          default: 30000,
        },
      },
      "x-coddy-property-order": [
        "model",
        "wait_for_limit_reset",
        "shared_busy_wait_ms",
      ],
    },
  },
};

const providersSection: SectionDescriptor = {
  id: "providers",
  label: "LLM providers",
  kind: "array",
  schemaKey: "providers",
  labelField: "name",
};
const modelsSection: SectionDescriptor = {
  id: "models",
  label: "Logical models",
  kind: "array",
  schemaKey: "models",
  labelField: "model",
};
const agentSection: SectionDescriptor = {
  id: "agent",
  label: "ReAct loop",
  kind: "object",
  schemaKey: "agent",
};

type Doc = Record<string, unknown>;

/** The latest document the section wrote. */
let latest: Doc = {};

function Harness(props: { section: SectionDescriptor; doc: Doc }) {
  const [doc, setDoc] = React.useState<Doc>(props.doc);
  return (
    <SettingsSection
      section={props.section}
      schema={schema}
      doc={doc}
      setDoc={(next) => {
        latest = next;
        setDoc(next);
      }}
    />
  );
}

function stubFetch(models: { id: string; context_window?: number }[] = []) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: unknown) => {
      const url = String(input);
      if (url.startsWith("/coddy/config/reasoning-levels")) {
        return {
          ok: true,
          json: async () => ({ ok: true, levels: [], detected: false }),
        };
      }
      return { ok: true, json: async () => ({ ok: true, models }) };
    }),
  );
}

function openFirstRow() {
  fireEvent.click(screen.getByTestId("settings-master-item-0"));
}

function modelsOf(doc: Doc): Record<string, unknown>[] {
  return doc.models as Record<string, unknown>[];
}

const SUBSCRIPTION_PROVIDERS = [
  { name: "work", type: "codex" },
  { name: "nd", type: "neuraldeep" },
  { name: "nd-key", type: "neuraldeep", api_key: "nd-key-1" },
  { name: "oa", type: "openai" },
  { name: "lab", type: "coddy", api_base: "https://lab.example:12345" },
];

function renderModel(model: Record<string, unknown>) {
  stubFetch();
  render(
    <Harness
      section={modelsSection}
      doc={{ providers: SUBSCRIPTION_PROVIDERS, models: [model] }}
    />,
  );
  openFirstRow();
}

// --- the alias field -------------------------------------------------------

test("the model row has a Sharing block with the alias field", () => {
  renderModel({ model: "oa/gpt-4o" });
  const block = screen.getByTestId("settings-group-sharing");
  expect(block.querySelector("legend")?.textContent).toBe("Sharing");
  expect(within(block).getByTestId("shared-as-input")).toBeTruthy();
  expect(screen.getByLabelText("Shared as")).toBe(
    screen.getByTestId("shared-as-input"),
  );
});

test("typing an alias writes shared_as, and clearing it keeps the model private", () => {
  renderModel({ model: "oa/gpt-4o" });
  const input = screen.getByTestId("shared-as-input") as HTMLInputElement;
  fireEvent.change(input, { target: { value: "terra" } });
  expect(modelsOf(latest)[0]?.shared_as).toBe("terra");
  expect(screen.queryByTestId("shared-as-error")).toBeNull();
  fireEvent.change(input, { target: { value: "" } });
  expect(modelsOf(latest)[0]?.shared_as).toBe("");
  // Empty is a choice (private), not a mistake.
  expect(screen.queryByTestId("shared-as-error")).toBeNull();
  expect(input.getAttribute("aria-invalid")).not.toBe("true");
});

test("an alias the server would refuse is flagged in the form", () => {
  renderModel({ model: "oa/gpt-4o" });
  const input = screen.getByTestId("shared-as-input") as HTMLInputElement;
  for (const bad of ["-terra", "ter/ra", "ter ra", "a".repeat(65), "терра"]) {
    fireEvent.change(input, { target: { value: bad } });
    const error = screen.getByTestId("shared-as-error");
    expect(error.textContent, bad).toMatch(/not a valid alias/i);
    expect(input.getAttribute("aria-invalid"), bad).toBe("true");
    expect(input.getAttribute("aria-describedby"), bad).toBe(error.id);
  }
  fireEvent.change(input, { target: { value: "gpt-4o.shared_1" } });
  expect(screen.queryByTestId("shared-as-error")).toBeNull();
  expect(input.getAttribute("aria-invalid")).not.toBe("true");
});

test("an alias with stray spaces is judged the way the server trims it", () => {
  renderModel({ model: "oa/gpt-4o", shared_as: "  terra " });
  expect(screen.queryByTestId("shared-as-error")).toBeNull();
});

// --- the subscription acknowledgement --------------------------------------

test("no acknowledgement is asked while the model is private", () => {
  renderModel({ model: "work/gpt-5" });
  expect(screen.queryByTestId("shared-subscription-ack")).toBeNull();
});

test("a shared model on a provider with a key of its own asks for none", () => {
  renderModel({ model: "oa/gpt-4o", shared_as: "terra" });
  expect(screen.queryByTestId("shared-subscription-ack")).toBeNull();
  cleanup();
  renderModel({ model: "nd-key/qwen", shared_as: "q" });
  expect(screen.queryByTestId("shared-subscription-ack")).toBeNull();
});

test("a shared model on a subscription login asks for the acknowledgement, required, with the warning beside it", () => {
  renderModel({ model: "work/gpt-5", shared_as: "terra" });
  const ack = screen.getByTestId("shared-subscription-ack");
  expect(ack.getAttribute("role")).toBe("switch");
  expect(ack.getAttribute("aria-checked")).toBe("false");
  expect(ack.getAttribute("aria-required")).toBe("true");
  expect(ack.getAttribute("aria-invalid")).toBe("true");

  const row = screen.getByTestId("shared-ack");
  expect(row.textContent).toContain("I accept sharing a subscription login");
  expect(within(row).getByText("required")).toBeTruthy();
  const warning = screen.getByTestId("shared-ack-warning");
  expect(warning.textContent).toContain("work (codex)");
  expect(warning.textContent).toContain("hands its quota to every holder");
  expect(warning.textContent).toContain("terms of service");
  // While it is missing the warning is in the error tone, and the switch is
  // described by it.
  expect(warning.classList.contains("settings-error")).toBe(true);
  expect(ack.getAttribute("aria-describedby")).toBe(warning.id);
});

test("accepting writes shared_subscription_ack and calms the warning", () => {
  renderModel({ model: "work/gpt-5", shared_as: "terra" });
  fireEvent.click(screen.getByTestId("shared-subscription-ack"));
  expect(modelsOf(latest)[0]?.shared_subscription_ack).toBe(true);
  const ack = screen.getByTestId("shared-subscription-ack");
  expect(ack.getAttribute("aria-checked")).toBe("true");
  expect(ack.getAttribute("aria-invalid")).not.toBe("true");
  // The warning stays on screen: the operator accepted what it says.
  expect(
    screen
      .getByTestId("shared-ack-warning")
      .classList.contains("settings-error"),
  ).toBe(false);
});

test("a stored acknowledgement shows as accepted", () => {
  renderModel({
    model: "work/gpt-5",
    shared_as: "terra",
    shared_subscription_ack: true,
  });
  expect(
    screen.getByTestId("shared-subscription-ack").getAttribute("aria-checked"),
  ).toBe("true");
});

test("a neuraldeep provider with no key of its own is a subscription login too", () => {
  renderModel({ model: "nd/qwen", shared_as: "q" });
  const warning = screen.getByTestId("shared-ack-warning");
  expect(warning.textContent).toContain("nd (neuraldeep)");
});

test("clearing the alias takes the acknowledgement away", () => {
  renderModel({ model: "work/gpt-5", shared_as: "terra" });
  expect(screen.getByTestId("shared-subscription-ack")).toBeTruthy();
  fireEvent.change(screen.getByTestId("shared-as-input"), {
    target: { value: "" },
  });
  expect(screen.queryByTestId("shared-subscription-ack")).toBeNull();
});

test("the acknowledgement block reads in Russian", () => {
  setLocale("ru");
  renderModel({ model: "work/gpt-5", shared_as: "terra" });
  const row = screen.getByTestId("shared-ack");
  expect(row.textContent).toContain("Я согласен делиться входом по подписке");
  expect(within(row).getByText("обязательно")).toBeTruthy();
  expect(screen.getByTestId("shared-ack-warning").textContent).toContain(
    "work (codex) работает по входу с подпиской",
  );
  expect(screen.getByTestId("settings-group-sharing").textContent).toContain(
    "Общий доступ под именем",
  );
});

// --- the picker -------------------------------------------------------------

async function addFromPicker(
  provider: Record<string, unknown>,
  listing: { id: string; context_window?: number }[],
  id: string,
) {
  stubFetch(listing);
  render(
    <Harness
      section={providersSection}
      doc={{ providers: [provider], models: [] }}
    />,
  );
  openFirstRow();
  fireEvent.click(await screen.findByTestId(`provider-model-toggle-${id}`));
  return modelsOf(latest)[0] as Record<string, unknown>;
}

test("a model picked from an openai listing is written with the window the listing reports", async () => {
  const row = await addFromPicker(
    { name: "demo", type: "openai" },
    [{ id: "m1", context_window: 131072 }],
    "m1",
  );
  expect(row.model).toBe("demo/m1");
  expect(row.max_context_tokens).toBe(131072);
});

test("a model picked from a coddy listing is written without the window: the listing stays the source", async () => {
  const row = await addFromPicker(
    { name: "lab", type: "coddy", api_base: "https://lab.example:12345" },
    [{ id: "terra", context_window: 200000 }],
    "terra",
  );
  expect(row.model).toBe("lab/terra");
  // A copied window would pin the row to what the remote said that day.
  // (0 is the seed of every row: "read it from the listing".)
  expect(row.max_context_tokens ?? 0).toBe(0);
  // Nor does it copy the other capabilities: a written false is not "unset".
  expect(row).not.toHaveProperty("multimodal");
  expect(row).not.toHaveProperty("allow_reasoning_off");
  expect(row).not.toHaveProperty("reasoning_levels");
  // What has a meaning of its own stays seeded.
  expect(row.stream).toBe(true);
});

// --- the coddy provider -----------------------------------------------------

test("the coddy provider asks for its address, with an example of one", () => {
  stubFetch();
  render(
    <Harness
      section={providersSection}
      doc={{ providers: [{ name: "lab", type: "coddy" }], models: [] }}
    />,
  );
  openFirstRow();
  const base = screen.getByLabelText("API base URL") as HTMLInputElement;
  expect(base.placeholder).toBe("https://host:12345");
  fireEvent.change(base, { target: { value: "https://lab.example:12345" } });
  const rows = latest.providers as Record<string, unknown>[];
  expect(rows[0]?.api_base).toBe("https://lab.example:12345");
  // The token of the remote goes into the ordinary key field.
  expect(screen.getByLabelText("API key")).toBeTruthy();
});

test("another provider type keeps the ordinary api_base field", () => {
  stubFetch();
  render(
    <Harness
      section={providersSection}
      doc={{ providers: [{ name: "demo", type: "openai" }], models: [] }}
    />,
  );
  openFirstRow();
  expect(
    (screen.getByLabelText("API base URL") as HTMLInputElement).placeholder,
  ).not.toBe("https://host:12345");
});

test("the busy wait is a field of the coddy provider only, among the advanced settings", () => {
  stubFetch();
  const coddy = render(
    <Harness
      section={providersSection}
      doc={{ providers: [{ name: "lab", type: "coddy" }], models: [] }}
    />,
  );
  openFirstRow();
  const advanced = screen.getByTestId("settings-group-advanced");
  const wait = within(advanced).getByLabelText("Wait for a free slot ms");
  fireEvent.change(wait, { target: { value: "45000" } });
  const rows = latest.providers as Record<string, unknown>[];
  expect(rows[0]?.busy_wait_ms).toBe(45000);
  coddy.unmount();

  render(
    <Harness
      section={providersSection}
      doc={{ providers: [{ name: "demo", type: "openai" }], models: [] }}
    />,
  );
  openFirstRow();
  expect(screen.queryByLabelText("Wait for a free slot ms")).toBeNull();
});

test("the TLS identity is three fields of the coddy provider only, among the advanced settings", () => {
  stubFetch();
  const coddy = render(
    <Harness
      section={providersSection}
      doc={{ providers: [{ name: "lab", type: "coddy" }], models: [] }}
    />,
  );
  openFirstRow();
  const advanced = screen.getByTestId("settings-group-advanced");
  fireEvent.change(within(advanced).getByLabelText("CA file"), {
    target: { value: "/etc/ca.pem" },
  });
  fireEvent.change(within(advanced).getByLabelText("Client certificate"), {
    target: { value: "/etc/c.pem" },
  });
  fireEvent.change(within(advanced).getByLabelText("Client key"), {
    target: { value: "/etc/k.pem" },
  });
  const row = (latest.providers as Record<string, unknown>[])[0];
  expect(row?.ca_file).toBe("/etc/ca.pem");
  expect(row?.client_cert_file).toBe("/etc/c.pem");
  expect(row?.client_key_file).toBe("/etc/k.pem");
  coddy.unmount();

  render(
    <Harness
      section={providersSection}
      doc={{ providers: [{ name: "demo", type: "openai" }], models: [] }}
    />,
  );
  openFirstRow();
  for (const label of ["CA file", "Client certificate", "Client key"]) {
    expect(screen.queryByLabelText(label)).toBeNull();
  }
});

test("the TLS identity of the coddy provider reads in Russian", async () => {
  setLocale("ru");
  stubFetch();
  render(
    <Harness
      section={providersSection}
      doc={{ providers: [{ name: "lab", type: "coddy" }], models: [] }}
    />,
  );
  openFirstRow();
  await waitFor(() => {
    expect(screen.getByLabelText("Файл CA")).toBeTruthy();
    expect(screen.getByLabelText("Клиентский сертификат")).toBeTruthy();
    expect(screen.getByLabelText("Клиентский ключ")).toBeTruthy();
  });
});

test("the usage panel switch is on the coddy provider form: the remote's account usage is read for it", () => {
  stubFetch();
  render(
    <Harness
      section={providersSection}
      doc={{ providers: [{ name: "lab", type: "coddy" }], models: [] }}
    />,
  );
  openFirstRow();
  expect(screen.getByText("Usage limits panel")).toBeTruthy();
});

test("a provider type with no usage source still has no usage panel switch", () => {
  stubFetch();
  render(
    <Harness
      section={providersSection}
      doc={{ providers: [{ name: "demo", type: "openai" }], models: [] }}
    />,
  );
  openFirstRow();
  expect(screen.queryByText("Usage limits panel")).toBeNull();
});

// --- the agent --------------------------------------------------------------

test("the agent tab carries the global busy wait of shared models in a block of its own", () => {
  render(
    <Harness section={agentSection} doc={{ agent: { model: "oa/gpt-4o" } }} />,
  );
  const block = screen.getByTestId("settings-group-shared");
  expect(block.querySelector("legend")?.textContent).toBe("Shared models");
  const input = within(block).getByLabelText(
    "Shared model busy wait ms",
  ) as HTMLInputElement;
  // Absent means 30000, the schema default the form draws.
  expect(input.value).toBe("30000");
  fireEvent.change(input, { target: { value: "0" } });
  expect((latest.agent as Record<string, unknown>).shared_busy_wait_ms).toBe(0);
});

test("the busy wait field of the coddy provider reads in Russian", async () => {
  setLocale("ru");
  stubFetch();
  render(
    <Harness
      section={providersSection}
      doc={{ providers: [{ name: "lab", type: "coddy" }], models: [] }}
    />,
  );
  openFirstRow();
  await waitFor(() =>
    expect(screen.getByLabelText("Ожидание свободного слота, мс")).toBeTruthy(),
  );
});

// --- the three-state keys of a coddy row -------------------------------------

function renderLabModel(model: Record<string, unknown>) {
  stubFetch();
  render(
    <Harness
      section={modelsSection}
      doc={{
        providers: SUBSCRIPTION_PROVIDERS,
        models: [{ model: "lab/terra", ...model }],
      }}
    />,
  );
  openFirstRow();
}

/** The row as the server receives it: JSON drops a key set to undefined. */
function savedRow(): Record<string, unknown> {
  return (JSON.parse(JSON.stringify(latest)) as { models: unknown[] })
    .models[0] as Record<string, unknown>;
}

test("a coddy row draws multimodal and allow_reasoning_off as Remote / Yes / No, not as switches", () => {
  renderLabModel({});
  for (const key of ["multimodal", "allow_reasoning_off"]) {
    const field = screen.getByTestId(`tristate-${key}`);
    expect(within(field).getByText("Remote")).toBeTruthy();
    expect(within(field).getByText("Yes")).toBeTruthy();
    expect(within(field).getByText("No")).toBeTruthy();
    expect(
      (screen.getByTestId(`tristate-${key}-remote`) as HTMLInputElement)
        .checked,
    ).toBe(true);
  }
  expect(screen.queryByRole("switch", { name: /multimodal/i })).toBeNull();
  expect(
    screen.queryByRole("switch", { name: /allow disabling reasoning/i }),
  ).toBeNull();
  // The rest of the form is the ordinary one.
  expect(
    screen.getByRole("switch", { name: /stream responses/i }),
  ).toBeTruthy();
});

test("the control stays in the block of the key it replaces", () => {
  renderLabModel({});
  expect(
    within(screen.getByTestId("settings-group-model")).getByTestId(
      "tristate-multimodal",
    ),
  ).toBeTruthy();
  expect(
    within(screen.getByTestId("settings-group-reasoning")).getByTestId(
      "tristate-allow_reasoning_off",
    ),
  ).toBeTruthy();
});

test("choosing Yes or No writes the key, choosing Remote takes it out of the row", () => {
  renderLabModel({});
  fireEvent.click(screen.getByTestId("tristate-multimodal-yes"));
  expect(savedRow().multimodal).toBe(true);
  fireEvent.click(screen.getByTestId("tristate-multimodal-no"));
  expect(savedRow().multimodal).toBe(false);
  expect(savedRow()).toHaveProperty("multimodal");
  fireEvent.click(screen.getByTestId("tristate-multimodal-remote"));
  expect(savedRow()).not.toHaveProperty("multimodal");
  // The sibling key is not touched by any of it.
  expect(savedRow()).not.toHaveProperty("allow_reasoning_off");
  fireEvent.click(screen.getByTestId("tristate-allow_reasoning_off-yes"));
  expect(savedRow().allow_reasoning_off).toBe(true);
  fireEvent.click(screen.getByTestId("tristate-allow_reasoning_off-remote"));
  expect(savedRow()).not.toHaveProperty("allow_reasoning_off");
});

test("a row phase 1 saved with both keys false shows No, with the way back in the hint", () => {
  renderLabModel({ multimodal: false, allow_reasoning_off: false });
  for (const key of ["multimodal", "allow_reasoning_off"]) {
    expect(
      (screen.getByTestId(`tristate-${key}-no`) as HTMLInputElement).checked,
    ).toBe(true);
    expect(screen.getByTestId(`tristate-${key}-hint`).textContent).toContain(
      "Choose Remote",
    );
  }
});

test("a row of another provider type keeps the switch, and a written false stays false", () => {
  renderModel({ model: "oa/gpt-4o", multimodal: false });
  expect(screen.queryByTestId("tristate-multimodal")).toBeNull();
  const sw = screen.getByRole("switch", { name: /multimodal/i });
  expect(sw.getAttribute("aria-checked")).toBe("false");
  fireEvent.click(sw);
  expect(modelsOf(latest)[0]?.multimodal).toBe(true);
  fireEvent.click(screen.getByRole("switch", { name: /multimodal/i }));
  expect(modelsOf(latest)[0]?.multimodal).toBe(false);
});

test("the provider decides, read from the form as it stands: a model of a row not typed coddy keeps the switch", () => {
  stubFetch();
  render(
    <Harness
      section={modelsSection}
      doc={{
        providers: [{ name: "lab", type: "openai" }],
        models: [{ model: "lab/terra" }],
      }}
    />,
  );
  openFirstRow();
  expect(screen.queryByTestId("tristate-multimodal")).toBeNull();
  expect(screen.getByRole("switch", { name: /multimodal/i })).toBeTruthy();
});

test("the control reads in Russian", () => {
  setLocale("ru");
  renderLabModel({});
  const field = screen.getByTestId("tristate-multimodal");
  expect(within(field).getByText("Как у удалённого")).toBeTruthy();
  expect(within(field).getByText("Да")).toBeTruthy();
  expect(within(field).getByText("Нет")).toBeTruthy();
});

test("the picker never writes the tri-state keys for a new coddy row, nor the levels", async () => {
  const row = await addFromPicker(
    { name: "lab", type: "coddy", api_base: "https://lab.example:12345" },
    [
      {
        id: "terra",
        context_window: 200000,
        multimodal: true,
        reasoning_levels: ["low", "high"],
        allow_reasoning_off: true,
      } as { id: string; context_window?: number },
    ],
    "terra",
  );
  const written = JSON.parse(JSON.stringify(row)) as Record<string, unknown>;
  expect(written).not.toHaveProperty("multimodal");
  expect(written).not.toHaveProperty("allow_reasoning_off");
  expect(written).not.toHaveProperty("reasoning_levels");
  // The seed's empty default is "not written", as for every row.
  expect(written.reasoning_default ?? "").toBe("");
});

test("a model picked from another provider's listing is seeded as before", async () => {
  const row = await addFromPicker(
    { name: "demo", type: "openai" },
    [{ id: "m1" }],
    "m1",
  );
  // Both keys keep the seed every row of another type has had.
  expect(row.multimodal).toBe(false);
  expect(row.allow_reasoning_off).toBe(false);
});

// --- a row added with the Add button -----------------------------------------

/**
 * addRowWithId walks the form the way an operator does: Add, pick the
 * provider, type the id its API expects. The Add button seeds the row before
 * any provider is known, so what it writes must not decide the row's type.
 */
function addRowWithId(provider: string, id: string) {
  stubFetch();
  render(
    <Harness
      section={modelsSection}
      doc={{ providers: SUBSCRIPTION_PROVIDERS, models: [] }}
    />,
  );
  fireEvent.click(screen.getByTestId("settings-master-add"));
  fireEvent.focus(screen.getByTestId("model-field-provider"));
  fireEvent.mouseDown(screen.getByText(provider));
  fireEvent.change(screen.getByTestId("model-field-model"), {
    target: { value: id },
  });
}

test("Add seeds an empty row without the tri-state keys: its provider is not chosen yet", () => {
  stubFetch();
  render(
    <Harness
      section={modelsSection}
      doc={{ providers: SUBSCRIPTION_PROVIDERS, models: [] }}
    />,
  );
  fireEvent.click(screen.getByTestId("settings-master-add"));
  const row = savedRow();
  expect(row).not.toHaveProperty("multimodal");
  expect(row).not.toHaveProperty("allow_reasoning_off");
  // What has a meaning of its own stays seeded, as for every row.
  expect(row.stream).toBe(true);
});

test("a row added with Add and given a coddy provider follows the remote: both keys Remote, none written", () => {
  addRowWithId("lab", "terra");
  expect(savedRow().model).toBe("lab/terra");
  const row = savedRow();
  expect(row).not.toHaveProperty("multimodal");
  expect(row).not.toHaveProperty("allow_reasoning_off");
  for (const key of ["multimodal", "allow_reasoning_off"]) {
    expect(
      (screen.getByTestId(`tristate-${key}-remote`) as HTMLInputElement)
        .checked,
      key,
    ).toBe(true);
    expect(
      (screen.getByTestId(`tristate-${key}-no`) as HTMLInputElement).checked,
      key,
    ).toBe(false);
  }
});

test("a row added with Add and given another provider keeps the switches, off until the operator turns one on", () => {
  addRowWithId("oa", "gpt-x");
  expect(savedRow().model).toBe("oa/gpt-x");
  expect(screen.queryByTestId("tristate-multimodal")).toBeNull();
  const multimodal = screen.getByRole("switch", { name: /multimodal/i });
  const reasoningOff = screen.getByRole("switch", {
    name: /allow disabling reasoning/i,
  });
  expect(multimodal.getAttribute("aria-checked")).toBe("false");
  expect(reasoningOff.getAttribute("aria-checked")).toBe("false");
  // An absent key and a written false mean the same for these providers.
  expect(savedRow().multimodal ?? false).toBe(false);
  expect(savedRow().allow_reasoning_off ?? false).toBe(false);
  fireEvent.click(multimodal);
  expect(savedRow().multimodal).toBe(true);
  fireEvent.click(screen.getByRole("switch", { name: /multimodal/i }));
  expect(savedRow().multimodal).toBe(false);
  fireEvent.click(reasoningOff);
  expect(savedRow().allow_reasoning_off).toBe(true);
});
