import React from "react";
import { afterEach, expect, test, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { createModelCascadeState } from "./applyModelsChange";
import { SettingsSection } from "./SettingsSection";
import type { JsonSchema } from "./SchemaForm";
import type { SectionDescriptor } from "./settingsSections";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const providersSection: SectionDescriptor = {
  id: "providers",
  label: "LLM providers",
  kind: "array",
  schemaKey: "providers",
  labelField: "name",
};

const schema = {
  type: "object",
  properties: {
    providers: {
      type: "array",
      title: "LLM providers",
      items: {
        type: "object",
        properties: {
          name: { type: "string", title: "Provider name" },
          type: { type: "string", title: "Provider type" },
        },
        "x-coddy-property-order": ["name", "type"],
      },
    },
  },
} as unknown as JsonSchema;

const oldModel = "custom/deepseek/deepseek-v4.1-flash-1017";
const otherModel = "other/qwen3.8-27b";

function initialDocument(): Record<string, unknown> {
  return {
    providers: [
      { name: "custom", type: "openai" },
      { name: "other", type: "openai" },
    ],
    models: [{ model: oldModel }, { model: otherModel }],
    agent: { model: oldModel, max_turns: 20 },
    memory: { model: oldModel, fallback_models: [oldModel, otherModel] },
    compaction: {
      model: oldModel,
      fallback_models: [otherModel, oldModel],
    },
    supervisor: { model: oldModel },
    decisions: { model: "frida" },
  };
}

function ProvidersHarness() {
  const [doc, setDoc] =
    React.useState<Record<string, unknown>>(initialDocument());
  return (
    <>
      <pre data-testid="settings-document">{JSON.stringify(doc)}</pre>
      <SettingsSection
        section={providersSection}
        schema={schema}
        doc={doc}
        setDoc={setDoc}
      />
    </>
  );
}

function ProviderTabSwitchHarness() {
  const [doc, setDoc] =
    React.useState<Record<string, unknown>>(initialDocument());
  const [shown, setShown] = React.useState(true);
  const modelCascade = React.useRef(createModelCascadeState()).current;
  return (
    <>
      <pre data-testid="settings-document">{JSON.stringify(doc)}</pre>
      <button onClick={() => setShown((value) => !value)}>
        {shown ? "Other tab" : "Providers"}
      </button>
      {shown ? (
        <SettingsSection
          section={providersSection}
          schema={schema}
          doc={doc}
          setDoc={setDoc}
          modelCascade={modelCascade}
        />
      ) : (
        <div>Other settings</div>
      )}
    </>
  );
}

function documentOnScreen(): Record<string, unknown> {
  return JSON.parse(
    screen.getByTestId("settings-document").textContent ?? "{}",
  );
}

function stubProviderModels() {
  vi.stubGlobal(
    "fetch",
    vi.fn(async () => ({
      ok: true,
      json: async () => ({ ok: true, models: [] }),
    })),
  );
}

test("renaming a provider moves its models and every model reference", () => {
  stubProviderModels();
  render(<ProvidersHarness />);
  fireEvent.click(screen.getByTestId("settings-master-item-0"));
  fireEvent.change(screen.getByLabelText("Provider id"), {
    target: { value: "hub" },
  });

  const doc = documentOnScreen();
  expect(doc.providers).toEqual([
    { name: "hub", type: "openai" },
    { name: "other", type: "openai" },
  ]);
  expect(doc.models).toEqual([
    { model: "hub/deepseek/deepseek-v4.1-flash-1017" },
    { model: otherModel },
  ]);
  expect(doc.agent).toEqual({
    model: "hub/deepseek/deepseek-v4.1-flash-1017",
    max_turns: 20,
  });
  expect(doc.memory).toEqual({
    model: "hub/deepseek/deepseek-v4.1-flash-1017",
    fallback_models: ["hub/deepseek/deepseek-v4.1-flash-1017", otherModel],
  });
  expect(doc.compaction).toEqual({
    model: "hub/deepseek/deepseek-v4.1-flash-1017",
    fallback_models: [otherModel, "hub/deepseek/deepseek-v4.1-flash-1017"],
  });
  expect(doc.supervisor).toEqual({
    model: "hub/deepseek/deepseek-v4.1-flash-1017",
  });
  expect(doc.decisions).toEqual({ model: "frida" });
});

test("deleting a provider removes its models and clears their references", () => {
  render(<ProvidersHarness />);
  fireEvent.click(screen.getByLabelText(/Remove custom/));

  const doc = documentOnScreen();
  expect(doc.providers).toEqual([{ name: "other", type: "openai" }]);
  expect(doc.models).toEqual([{ model: otherModel }]);
  expect(doc.agent).toEqual({ model: "", max_turns: 20 });
  expect(doc.memory).toEqual({ model: "", fallback_models: [otherModel] });
  expect(doc.compaction).toEqual({
    model: "",
    fallback_models: [otherModel],
  });
  expect(doc.supervisor).toEqual({ model: "" });
  expect(doc.decisions).toEqual({ model: "frida" });
});

test("provider rename survives clearing and retyping the id", () => {
  stubProviderModels();
  render(<ProvidersHarness />);
  fireEvent.click(screen.getByTestId("settings-master-item-0"));
  for (const value of ["", "hub"]) {
    fireEvent.change(screen.getByLabelText("Provider id"), {
      target: { value },
    });
  }

  const doc = documentOnScreen();
  expect(doc.models).toEqual([
    { model: "hub/deepseek/deepseek-v4.1-flash-1017" },
    { model: otherModel },
  ]);
  expect(doc.agent).toEqual({
    model: "hub/deepseek/deepseek-v4.1-flash-1017",
    max_turns: 20,
  });
  expect(doc.memory).toEqual({
    model: "hub/deepseek/deepseek-v4.1-flash-1017",
    fallback_models: ["hub/deepseek/deepseek-v4.1-flash-1017", otherModel],
  });
  expect(doc.compaction).toEqual({
    model: "hub/deepseek/deepseek-v4.1-flash-1017",
    fallback_models: [otherModel, "hub/deepseek/deepseek-v4.1-flash-1017"],
  });
  expect(doc.supervisor).toEqual({
    model: "hub/deepseek/deepseek-v4.1-flash-1017",
  });
});

test("provider rename survives a transient duplicate id", () => {
  stubProviderModels();
  render(<ProvidersHarness />);
  fireEvent.click(screen.getByTestId("settings-master-item-0"));
  for (const value of ["oth", "other", "other2"]) {
    fireEvent.change(screen.getByLabelText("Provider id"), {
      target: { value },
    });
  }

  const doc = documentOnScreen();
  expect(doc.providers).toEqual([
    { name: "other2", type: "openai" },
    { name: "other", type: "openai" },
  ]);
  expect(doc.models).toEqual([
    { model: "other2/deepseek/deepseek-v4.1-flash-1017" },
    { model: otherModel },
  ]);
  expect(doc.agent).toEqual({
    model: "other2/deepseek/deepseek-v4.1-flash-1017",
    max_turns: 20,
  });
  expect(doc.memory).toEqual({
    model: "other2/deepseek/deepseek-v4.1-flash-1017",
    fallback_models: ["other2/deepseek/deepseek-v4.1-flash-1017", otherModel],
  });
});

test("provider rename survives a settings tab switch", () => {
  stubProviderModels();
  render(<ProviderTabSwitchHarness />);
  fireEvent.click(screen.getByTestId("settings-master-item-0"));
  fireEvent.change(screen.getByLabelText("Provider id"), {
    target: { value: "" },
  });
  fireEvent.click(screen.getByRole("button", { name: "Other tab" }));
  fireEvent.click(screen.getByRole("button", { name: "Providers" }));
  fireEvent.click(screen.getByTestId("settings-master-item-0"));
  fireEvent.change(screen.getByLabelText("Provider id"), {
    target: { value: "hub" },
  });

  const doc = documentOnScreen();
  expect(doc.models).toEqual([
    { model: "hub/deepseek/deepseek-v4.1-flash-1017" },
    { model: otherModel },
  ]);
  expect(doc.agent).toEqual({
    model: "hub/deepseek/deepseek-v4.1-flash-1017",
    max_turns: 20,
  });
});
