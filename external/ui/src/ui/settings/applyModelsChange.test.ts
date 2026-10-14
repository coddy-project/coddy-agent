import { describe, expect, it } from "vitest";
import { applyModelsChange, applyToolsetsChange } from "./applyModelsChange";

function baseDoc(
  models: Array<Record<string, unknown>>,
  extra?: Record<string, unknown>,
): Record<string, unknown> {
  return { providers: [], models, ...extra };
}

describe("applyModelsChange", () => {
  it("updates agent.model when the sole model id is renamed", () => {
    const doc = baseDoc([{ model: "neuraldeep/gpt-120b-oss" }], {
      agent: { model: "neuraldeep/gpt-120b-oss", max_turns: 20 },
    });
    const next = applyModelsChange(doc, [{ model: "neuraldeep/qwen-3.6" }]);
    expect((next.agent as Record<string, unknown>).model).toBe(
      "neuraldeep/qwen-3.6",
    );
    // Unrelated agent fields preserved.
    expect((next.agent as Record<string, unknown>).max_turns).toBe(20);
    // Models replaced with the new array.
    expect(next.models).toEqual([{ model: "neuraldeep/qwen-3.6" }]);
  });

  it("updates memory.model referencing the same old id", () => {
    const doc = baseDoc([{ model: "neuraldeep/gpt-120b-oss" }], {
      agent: { model: "neuraldeep/gpt-120b-oss" },
      memory: { enable: true, model: "neuraldeep/gpt-120b-oss" },
    });
    const next = applyModelsChange(doc, [{ model: "neuraldeep/qwen-3.6" }]);
    expect((next.memory as Record<string, unknown>).model).toBe(
      "neuraldeep/qwen-3.6",
    );
    expect((next.memory as Record<string, unknown>).enable).toBe(true);
  });

  it("leaves memory.model pointing at a different id untouched", () => {
    const doc = baseDoc(
      [{ model: "neuraldeep/gpt-120b-oss" }, { model: "openai/gpt-4o" }],
      {
        agent: { model: "neuraldeep/gpt-120b-oss" },
        memory: { model: "openai/gpt-4o" },
      },
    );
    const next = applyModelsChange(doc, [
      { model: "neuraldeep/qwen-3.6" },
      { model: "openai/gpt-4o" },
    ]);
    expect((next.agent as Record<string, unknown>).model).toBe(
      "neuraldeep/qwen-3.6",
    );
    expect((next.memory as Record<string, unknown>).model).toBe(
      "openai/gpt-4o",
    );
  });

  it("leaves agent.model pointing at an unchanged model untouched", () => {
    const doc = baseDoc(
      [{ model: "neuraldeep/gpt-120b-oss" }, { model: "openai/gpt-4o" }],
      { agent: { model: "openai/gpt-4o" } },
    );
    const next = applyModelsChange(doc, [
      { model: "neuraldeep/qwen-3.6" },
      { model: "openai/gpt-4o" },
    ]);
    expect((next.agent as Record<string, unknown>).model).toBe("openai/gpt-4o");
  });

  it("does not repoint references when a model is added (length grows)", () => {
    const doc = baseDoc([{ model: "neuraldeep/gpt-120b-oss" }], {
      agent: { model: "neuraldeep/gpt-120b-oss" },
    });
    const next = applyModelsChange(doc, [
      { model: "neuraldeep/gpt-120b-oss" },
      { model: "openai/gpt-4o" },
    ]);
    expect((next.agent as Record<string, unknown>).model).toBe(
      "neuraldeep/gpt-120b-oss",
    );
  });

  it("does not repoint references when a model is removed (length shrinks)", () => {
    const doc = baseDoc(
      [{ model: "neuraldeep/gpt-120b-oss" }, { model: "openai/gpt-4o" }],
      { agent: { model: "neuraldeep/gpt-120b-oss" } },
    );
    const next = applyModelsChange(doc, [{ model: "openai/gpt-4o" }]);
    expect((next.agent as Record<string, unknown>).model).toBe(
      "neuraldeep/gpt-120b-oss",
    );
  });

  it("renames only the reference matching the changed entry", () => {
    const doc = baseDoc(
      [{ model: "neuraldeep/gpt-120b-oss" }, { model: "openai/gpt-4o" }],
      {
        agent: { model: "openai/gpt-4o" },
        memory: { model: "neuraldeep/gpt-120b-oss" },
      },
    );
    const next = applyModelsChange(doc, [
      { model: "neuraldeep/gpt-120b-oss" },
      { model: "openai/gpt-4o-mini" },
    ]);
    // agent referenced the renamed one -> updated.
    expect((next.agent as Record<string, unknown>).model).toBe(
      "openai/gpt-4o-mini",
    );
    // memory referenced the untouched one -> unchanged.
    expect((next.memory as Record<string, unknown>).model).toBe(
      "neuraldeep/gpt-120b-oss",
    );
  });

  it("ignores a rename from an empty id", () => {
    const doc = baseDoc([{ model: "" }], {
      agent: { model: "" },
    });
    const next = applyModelsChange(doc, [{ model: "neuraldeep/qwen-3.6" }]);
    // Empty ids are not treated as a rename source.
    expect((next.agent as Record<string, unknown>).model).toBe("");
  });

  it("returns the doc unchanged when there are no model references", () => {
    const doc = baseDoc([{ model: "neuraldeep/gpt-120b-oss" }]);
    const next = applyModelsChange(doc, [{ model: "neuraldeep/qwen-3.6" }]);
    expect(next.models).toEqual([{ model: "neuraldeep/qwen-3.6" }]);
    expect(next.agent).toBeUndefined();
  });
});

describe("applyToolsetsChange", () => {
  it("renames the toolset of every model that named a preset renamed in place", () => {
    const doc = baseDoc(
      [
        { model: "local/qwen", toolset: "coding" },
        { model: "local/plain" },
        { model: "local/other", toolset: "no-net" },
      ],
      { toolsets: [{ name: "coding" }, { name: "no-net" }] },
    );
    const next = applyToolsetsChange(doc, [
      { name: "lean" },
      { name: "no-net" },
    ]);
    expect(next.toolsets).toEqual([{ name: "lean" }, { name: "no-net" }]);
    expect(next.models).toEqual([
      { model: "local/qwen", toolset: "lean" },
      { model: "local/plain" },
      { model: "local/other", toolset: "no-net" },
    ]);
  });

  it("leaves the references alone when a preset is added or removed", () => {
    const models = [{ model: "local/qwen", toolset: "coding" }];
    const doc = baseDoc(models, { toolsets: [{ name: "coding" }] });
    expect(applyToolsetsChange(doc, []).models).toBe(models);
    expect(
      applyToolsetsChange(doc, [{ name: "coding" }, { name: "x" }]).models,
    ).toBe(models);
  });
});
