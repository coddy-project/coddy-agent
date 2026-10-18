import React from "react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";
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
import { initLocale } from "../i18n/i18n";
import { AGENT_SCHEMA } from "./settingsServer.fakes";

beforeEach(() => initLocale("en"));

afterEach(() => {
  cleanup();
  initLocale("en");
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

const modelsSection: SectionDescriptor = {
  id: "models",
  label: "Logical models",
  kind: "array",
  schemaKey: "models",
  labelField: "model",
};

// The fields of the served schema (internal/config/ui_schema.go) the sharing
// of a model touches.
const schema: JsonSchema = {
  type: "object",
  properties: {
    agent: {
      type: "object",
      properties: {
        model: { type: "string", title: "Default model" },
        max_turns: { type: "integer", title: "Max turns" },
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
        "max_turns",
        "wait_for_limit_reset",
        "shared_busy_wait_ms",
      ],
    },
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
            enum: [
              "openai",
              "anthropic",
              "neuraldeep",
              "codex",
              "devin",
              "coddy",
            ],
          },
          api_base: { type: "string", title: "API base URL" },
          api_key: { type: "string", title: "API key" },
          api_key_command: { type: "string", title: "API key command" },
          proxy: { type: "string", title: "Proxy URL" },
          timeout_ms: { type: "integer", title: "Request timeout ms" },
          busy_wait_ms: { type: "integer", title: "Busy wait ms" },
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
          multimodal: { type: "boolean", title: "Multimodal" },
          allow_reasoning_off: {
            type: "boolean",
            title: "Allow disabling reasoning",
            default: false,
          },
          shared_as: { type: "string", title: "Shared as" },
          shared_subscription_ack: {
            type: "boolean",
            title: "Acknowledge subscription sharing",
            default: false,
          },
        },
        "x-coddy-property-order": [
          "model",
          "max_context_tokens",
          "multimodal",
          "allow_reasoning_off",
          "shared_as",
          "shared_subscription_ack",
        ],
      },
    },
  },
};

/** Answers every provider listing the form asks for with these models. */
function stubListing(models: unknown[] = []) {
  const fetchMock = vi.fn(async (_input: unknown, _init?: RequestInit) => ({
    ok: true,
    json: async () => ({ ok: true, models }),
  }));
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

function Harness(props: {
  section: SectionDescriptor;
  doc: Record<string, unknown>;
  schema?: JsonSchema;
}) {
  const [doc, setDoc] = React.useState<Record<string, unknown>>(props.doc);
  return (
    <>
      <output data-testid="settings-doc">{JSON.stringify(doc)}</output>
      <SettingsSection
        section={props.section}
        schema={props.schema ?? schema}
        doc={doc}
        setDoc={setDoc}
      />
    </>
  );
}

/** The settings document as it would be saved. */
function saved(): Record<string, unknown> {
  return JSON.parse(screen.getByTestId("settings-doc").textContent || "{}");
}

function savedModel(i = 0): Record<string, unknown> {
  return (saved().models as Record<string, unknown>[])[i]!;
}

/** Opens the form of one model row whose provider is the given row. */
function openModel(
  provider: Record<string, unknown>,
  model: Record<string, unknown>,
) {
  render(
    <Harness
      section={modelsSection}
      doc={{ providers: [provider], models: [model] }}
    />,
  );
  fireEvent.click(screen.getByTestId("settings-master-item-0"));
}

const TERRA = { name: "terra", type: "coddy", api_base: "https://terra:1" };

test("a remote Coddy's model draws Allow disabling reasoning in three positions, and Remote takes the key out", () => {
  stubListing();
  openModel(TERRA, { model: "terra/qwen", allow_reasoning_off: true });

  const field = screen.getByTestId("tristate-allow_reasoning_off");
  expect(
    within(field).getByRole("radiogroup", {
      name: "Allow disabling reasoning",
    }),
  ).toBeTruthy();
  expect(
    (screen.getByTestId("tristate-allow_reasoning_off-yes") as HTMLInputElement)
      .checked,
  ).toBe(true);
  // No switch stands in for it: a switch has no position for "the remote's".
  expect(
    screen.queryByRole("switch", { name: "Allow disabling reasoning" }),
  ).toBeNull();
  // It stays in the Reasoning group of the model form.
  expect(screen.getByTestId("settings-group-reasoning").contains(field)).toBe(
    true,
  );

  fireEvent.click(screen.getByTestId("tristate-allow_reasoning_off-no"));
  expect(savedModel().allow_reasoning_off).toBe(false);

  fireEvent.click(screen.getByTestId("tristate-allow_reasoning_off-remote"));
  expect(savedModel()).not.toHaveProperty("allow_reasoning_off");
  expect(savedModel().model).toBe("terra/qwen");
  expect(
    (
      screen.getByTestId(
        "tristate-allow_reasoning_off-remote",
      ) as HTMLInputElement
    ).checked,
  ).toBe(true);
});

test("a model of any other provider keeps Allow disabling reasoning as its switch", () => {
  stubListing();
  openModel({ name: "demo", type: "openai" }, { model: "demo/m1" });

  expect(screen.queryByTestId("tristate-allow_reasoning_off")).toBeNull();
  const toggle = screen.getByRole("switch", {
    name: "Allow disabling reasoning",
  });
  expect(toggle.getAttribute("aria-checked")).toBe("false");
  fireEvent.click(toggle);
  expect(savedModel().allow_reasoning_off).toBe(true);
});

// The served schema (internal/config/ui_schema.go) puts the sharing keys after
// the reasoning ones, and a group stands where its first field stands.
test("in the served form the Sharing group follows Reasoning and comes before Tools", () => {
  stubListing();
  render(
    <Harness
      section={modelsSection}
      schema={AGENT_SCHEMA}
      doc={{
        providers: [{ name: "demo", type: "openai" }],
        models: [{ model: "demo/m1" }],
      }}
    />,
  );
  fireEvent.click(screen.getByTestId("settings-master-item-0"));
  const legends = [
    ...document.querySelectorAll(".settings-schema-root > fieldset > legend"),
  ].map((l) => l.textContent);
  expect(legends).toEqual([
    "Model",
    "Generation",
    "Reasoning",
    "Sharing",
    "Tools",
  ]);
});

test("a model another Coddy serves offers no alias, so its form has no Sharing group", () => {
  stubListing();
  openModel(TERRA, { model: "terra/qwen" });
  expect(screen.queryByTestId("shared-as-field")).toBeNull();
  expect(screen.queryByTestId("settings-group-sharing")).toBeNull();
  // The rest of the form is there.
  expect(screen.getByTestId("settings-group-reasoning")).toBeTruthy();
});

test("an alias written on a model another Coddy serves stays on show, marked, until it is removed", () => {
  stubListing();
  openModel(TERRA, { model: "terra/qwen", shared_as: "again" });
  const input = screen.getByTestId("shared-as-input") as HTMLInputElement;
  expect(input.getAttribute("aria-invalid")).toBe("true");
  expect(screen.getByTestId("shared-as-error").textContent).toBe(
    "A model served by another Coddy cannot be shared again: remove the alias.",
  );
  fireEvent.change(input, { target: { value: "" } });
  expect(savedModel().shared_as).toBe("");
  expect(screen.queryByTestId("shared-as-field")).toBeNull();
  expect(screen.queryByTestId("settings-group-sharing")).toBeNull();
});

test("the alias a model is shared as is its own field in a Sharing group", () => {
  stubListing();
  openModel({ name: "demo", type: "openai" }, { model: "demo/m1" });

  const group = screen.getByTestId("settings-group-sharing");
  expect(group.querySelector("legend")?.textContent).toBe("Sharing");
  const field = screen.getByTestId("shared-as-field");
  expect(group.contains(field)).toBe(true);
  expect(screen.queryByTestId("shared-as-error")).toBeNull();

  const input = screen.getByTestId("shared-as-input");
  fireEvent.change(input, { target: { value: "team/terra" } });
  expect(savedModel().shared_as).toBe("team/terra");
  expect(screen.getByTestId("shared-as-error")).toBeTruthy();
  expect(input.getAttribute("aria-invalid")).toBe("true");

  fireEvent.change(input, { target: { value: "terra" } });
  expect(savedModel().shared_as).toBe("terra");
  expect(screen.queryByTestId("shared-as-error")).toBeNull();
});

test("sharing a model of a subscription login asks for the acknowledgement, naming the provider", () => {
  stubListing();
  for (const provider of [
    { name: "my-codex", type: "codex" },
    { name: "my-devin", type: "devin" },
    { name: "hub", type: "neuraldeep", api_key: "" },
  ]) {
    openModel(provider, {
      model: `${provider.name}/m1`,
      shared_as: "terra",
    });
    const ack = screen.getByTestId("shared-ack");
    expect(
      screen.getByTestId("settings-group-sharing").contains(ack),
      provider.type,
    ).toBe(true);
    expect(screen.getByTestId("shared-ack-warning").textContent).toContain(
      `${provider.name} (${provider.type}) runs on a subscription login.`,
    );

    fireEvent.click(screen.getByTestId("shared-subscription-ack"));
    expect(savedModel().shared_subscription_ack, provider.type).toBe(true);
    cleanup();
  }
});

test("the acknowledgement is not asked for a private model or a model with an API key", () => {
  stubListing();
  for (const [provider, model] of [
    [{ name: "my-codex", type: "codex" }, { model: "my-codex/m1" }],
    [
      { name: "my-codex", type: "codex" },
      { model: "my-codex/m1", shared_as: "  " },
    ],
    [
      { name: "hub", type: "neuraldeep", api_key: "nd-1" },
      { model: "hub/m1", shared_as: "terra" },
    ],
    [
      { name: "hub", type: "neuraldeep", api_key_command: "pass show nd" },
      { model: "hub/m1", shared_as: "terra" },
    ],
    [
      { name: "demo", type: "openai" },
      { model: "demo/m1", shared_as: "terra" },
    ],
  ] as const) {
    openModel(provider, model);
    expect(
      screen.queryByTestId("shared-ack"),
      JSON.stringify([provider, model]),
    ).toBeNull();
    expect(screen.queryByTestId("shared-subscription-ack")).toBeNull();
    // Nor is the schema's own switch drawn in its place.
    expect(
      screen.queryByRole("switch", {
        name: "Acknowledge subscription sharing",
      }),
    ).toBeNull();
    cleanup();
  }
});

test("typing an alias into a subscription model's row brings the acknowledgement in at once", () => {
  stubListing();
  openModel({ name: "my-codex", type: "codex" }, { model: "my-codex/m1" });
  expect(screen.queryByTestId("shared-ack")).toBeNull();

  fireEvent.change(screen.getByTestId("shared-as-input"), {
    target: { value: "terra" },
  });
  expect(screen.getByTestId("shared-ack")).toBeTruthy();

  fireEvent.change(screen.getByTestId("shared-as-input"), {
    target: { value: "" },
  });
  expect(screen.queryByTestId("shared-ack")).toBeNull();
});

test("a provider row offers busy_wait_ms only when it is a remote Coddy", () => {
  stubListing();
  render(
    <Harness
      section={providersSection}
      doc={{ providers: [TERRA, { name: "demo", type: "openai" }] }}
    />,
  );

  fireEvent.click(screen.getByTestId("settings-master-item-0"));
  const advanced = screen.getByTestId("settings-group-advanced");
  expect(within(advanced).getByLabelText("Busy wait ms")).toBeTruthy();
  fireEvent.click(screen.getByTestId("settings-detail-back"));

  fireEvent.click(screen.getByTestId("settings-master-item-1"));
  expect(screen.queryByLabelText("Busy wait ms")).toBeNull();
  // The rest of the advanced block is the same for both.
  expect(
    within(screen.getByTestId("settings-group-advanced")).getByLabelText(
      "Request timeout ms",
    ),
  ).toBeTruthy();
});

test("a remote Coddy's API base is a URL field, and another provider's stays the plain one", () => {
  stubListing();
  render(
    <Harness
      section={providersSection}
      doc={{ providers: [TERRA, { name: "demo", type: "openai" }] }}
    />,
  );

  fireEvent.click(screen.getByTestId("settings-master-item-0"));
  const base = screen.getByTestId("coddy-api-base") as HTMLInputElement;
  expect(base.inputMode).toBe("url");
  expect(base.value).toBe("https://terra:1");
  expect(screen.getByLabelText("API base URL")).toBe(base);
  fireEvent.change(base, { target: { value: "https://luna:2" } });
  expect((saved().providers as Record<string, unknown>[])[0]!.api_base).toBe(
    "https://luna:2",
  );
  fireEvent.click(screen.getByTestId("settings-detail-back"));

  fireEvent.click(screen.getByTestId("settings-master-item-1"));
  expect(screen.queryByTestId("coddy-api-base")).toBeNull();
  expect(screen.getByLabelText("API base URL")).toBeTruthy();
});

/** Adds the listed model m1 from the provider form of the given row. */
async function addFromListing(provider: Record<string, unknown>) {
  stubListing([{ id: "m1", context_window: 131072, multimodal: true }]);
  render(
    <Harness
      section={providersSection}
      doc={{ providers: [provider], models: [] }}
    />,
  );
  fireEvent.click(screen.getByTestId("settings-master-item-0"));
  fireEvent.click(await screen.findByTestId("provider-model-toggle-m1"));
  await waitFor(() => expect(saved().models).toHaveLength(1));
  return savedModel();
}

test("a model added from a remote Coddy's listing leaves its window, pictures and reasoning off to the remote", async () => {
  const row = await addFromListing(TERRA);

  expect(row.model).toBe("terra/m1");
  for (const key of [
    "max_context_tokens",
    "multimodal",
    "allow_reasoning_off",
  ]) {
    expect(row, key).not.toHaveProperty(key);
  }
  // The keys that are not the remote's are seeded as for any model.
  expect(row).toMatchObject({ shared_as: "", shared_subscription_ack: false });
});

test("a model added from another provider's listing keeps the context window it reports", async () => {
  const row = await addFromListing({ name: "demo", type: "openai" });

  expect(row).toMatchObject({
    model: "demo/m1",
    max_context_tokens: 131072,
    multimodal: false,
  });
});

test("the agent tab gathers the shared models' busy wait in a group of its own", () => {
  render(
    <Harness
      section={{
        id: "agent",
        label: "ReAct loop",
        kind: "object",
        schemaKey: "agent",
      }}
      doc={{ agent: { max_turns: 20 } }}
    />,
  );

  const group = screen.getByTestId("settings-group-shared");
  expect(group.querySelector("legend")?.textContent).toBe("Shared models");
  const input = within(group).getByLabelText("Shared model busy wait ms");
  expect(screen.getByTestId("settings-group-limits").contains(input)).toBe(
    false,
  );
  fireEvent.change(input, { target: { value: "5000" } });
  expect((saved().agent as Record<string, unknown>).shared_busy_wait_ms).toBe(
    5000,
  );
});
