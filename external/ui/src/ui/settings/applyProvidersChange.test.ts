import { describe, expect, test } from "vitest";
import {
  applyModelsChange,
  applyProvidersChange,
  createModelCascadeState,
} from "./applyModelsChange";

const providers = [
  { name: "custom", type: "openai" },
  { name: "other", type: "openai" },
];

function documentWithReferences(): Record<string, unknown> {
  return {
    providers,
    models: [{ model: "custom/deepseek/v4" }, { model: "other/qwen" }],
    agent: { model: "custom/deepseek/v4" },
    memory: {
      model: "custom/deepseek/v4",
      fallback_models: ["custom/deepseek/v4", "other/qwen"],
    },
    compaction: {
      model: "custom/deepseek/v4",
      fallback_models: ["other/qwen", "custom/deepseek/v4"],
    },
    supervisor: { model: "custom/deepseek/v4" },
    decisions: { model: "frida" },
  };
}

describe("applyProvidersChange", () => {
  test("preserves the model id after the first slash", () => {
    const doc = documentWithReferences();
    const next = applyProvidersChange(doc, [
      { name: "hub", type: "openai" },
      providers[1],
    ]);

    expect(next.models).toEqual([
      { model: "hub/deepseek/v4" },
      { model: "other/qwen" },
    ]);
    expect(next.agent).toEqual({ model: "hub/deepseek/v4" });
    expect(next.memory).toEqual({
      model: "hub/deepseek/v4",
      fallback_models: ["hub/deepseek/v4", "other/qwen"],
    });
    expect(next.compaction).toEqual({
      model: "hub/deepseek/v4",
      fallback_models: ["other/qwen", "hub/deepseek/v4"],
    });
    expect(next.supervisor).toEqual({ model: "hub/deepseek/v4" });
    expect(next.decisions).toEqual({ model: "frida" });
  });

  test("drops deleted model ids from fallback lists without reordering the rest", () => {
    const next = applyProvidersChange(documentWithReferences(), [providers[1]]);

    expect(next.models).toEqual([{ model: "other/qwen" }]);
    expect(next.agent).toEqual({ model: "" });
    expect(next.memory).toEqual({
      model: "",
      fallback_models: ["other/qwen"],
    });
    expect(next.compaction).toEqual({
      model: "",
      fallback_models: ["other/qwen"],
    });
    expect(next.supervisor).toEqual({ model: "" });
    expect(next.decisions).toEqual({ model: "frida" });
  });

  test("adding a provider leaves models and references untouched", () => {
    const doc = documentWithReferences();
    const next = applyProvidersChange(doc, [
      ...providers,
      { name: "new", type: "openai" },
    ]);

    expect(next.models).toBe(doc.models);
    expect(next.agent).toBe(doc.agent);
    expect(next.memory).toBe(doc.memory);
    expect(next.compaction).toBe(doc.compaction);
    expect(next.supervisor).toBe(doc.supervisor);
  });

  test("a rename to an existing provider name leaves model ownership unambiguous", () => {
    const doc = documentWithReferences();
    const next = applyProvidersChange(doc, [
      { name: "other", type: "openai" },
      providers[1],
    ]);

    expect(next.models).toBe(doc.models);
    expect(next.agent).toBe(doc.agent);
    expect(next.memory).toBe(doc.memory);
    expect(next.compaction).toBe(doc.compaction);
    expect(next.supervisor).toBe(doc.supervisor);
  });

  test("renames and removes manual references absent from the models list", () => {
    const doc: Record<string, unknown> = {
      providers,
      agent: { model: "custom/manual" },
      memory: { fallback_models: ["custom/manual", "other/qwen"] },
    };
    const renamed = applyProvidersChange(doc, [
      { name: "hub", type: "openai" },
      providers[1],
    ]);
    expect(renamed.agent).toEqual({ model: "hub/manual" });
    expect(renamed.memory).toEqual({
      fallback_models: ["hub/manual", "other/qwen"],
    });

    const removed = applyProvidersChange(doc, [providers[1]]);
    expect(removed.agent).toEqual({ model: "" });
    expect(removed.memory).toEqual({ fallback_models: ["other/qwen"] });
  });

  test("a newly named provider can be renamed with all of its references", () => {
    const state = createModelCascadeState();
    let doc: Record<string, unknown> = { providers: [], models: [] };
    doc = applyProvidersChange(doc, [{ name: "", type: "openai" }], state);
    doc = applyProvidersChange(
      doc,
      [{ name: "custom", type: "openai" }],
      state,
    );
    doc = applyModelsChange(doc, [{ model: "custom/deepseek/v4" }], state);
    doc = {
      ...doc,
      agent: { model: "custom/deepseek/v4" },
      memory: {
        model: "custom/deepseek/v4",
        fallback_models: ["custom/deepseek/v4"],
      },
      compaction: {
        model: "custom/deepseek/v4",
        fallback_models: ["custom/deepseek/v4"],
      },
      supervisor: { model: "custom/deepseek/v4" },
    };

    const next = applyProvidersChange(
      doc,
      [{ name: "hub", type: "openai" }],
      state,
    );
    expect(next.models).toEqual([{ model: "hub/deepseek/v4" }]);
    expect(next.agent).toEqual({ model: "hub/deepseek/v4" });
    expect(next.memory).toEqual({
      model: "hub/deepseek/v4",
      fallback_models: ["hub/deepseek/v4"],
    });
    expect(next.compaction).toEqual({
      model: "hub/deepseek/v4",
      fallback_models: ["hub/deepseek/v4"],
    });
    expect(next.supervisor).toEqual({ model: "hub/deepseek/v4" });
  });

  test("deletion uses the preserved owner during a duplicate provider edit", () => {
    const state = createModelCascadeState();
    const duplicate = applyProvidersChange(
      documentWithReferences(),
      [{ name: "other", type: "openai" }, providers[1]],
      state,
    );
    const next = applyProvidersChange(
      duplicate,
      [(duplicate.providers as unknown[])[0]],
      state,
    );

    expect(next.models).toEqual([{ model: "custom/deepseek/v4" }]);
    expect(next.agent).toEqual({ model: "custom/deepseek/v4" });
    expect(next.memory).toEqual({
      model: "custom/deepseek/v4",
      fallback_models: ["custom/deepseek/v4"],
    });
    expect(next.compaction).toEqual({
      model: "custom/deepseek/v4",
      fallback_models: ["custom/deepseek/v4"],
    });
    expect(next.supervisor).toEqual({ model: "custom/deepseek/v4" });
  });
});

test("a logical model rename updates every selector and fallback chain", () => {
  const doc = documentWithReferences();
  const next = applyModelsChange(doc, [
    { model: "custom/deepseek/v5" },
    { model: "other/qwen" },
  ]);

  expect(next.agent).toEqual({ model: "custom/deepseek/v5" });
  expect(next.memory).toEqual({
    model: "custom/deepseek/v5",
    fallback_models: ["custom/deepseek/v5", "other/qwen"],
  });
  expect(next.compaction).toEqual({
    model: "custom/deepseek/v5",
    fallback_models: ["other/qwen", "custom/deepseek/v5"],
  });
  expect(next.supervisor).toEqual({ model: "custom/deepseek/v5" });
});

test("a transient duplicate model id does not hijack another row's references", () => {
  const state = createModelCascadeState();
  const doc: Record<string, unknown> = {
    models: [{ model: "demo/a" }, { model: "demo/b" }],
    agent: { model: "demo/a" },
    memory: { model: "demo/a", fallback_models: ["demo/a", "demo/b"] },
    compaction: { model: "demo/b", fallback_models: ["demo/a", "demo/b"] },
    supervisor: { model: "demo/b" },
  };
  let next = applyModelsChange(
    doc,
    [{ model: "demo/b" }, { model: "demo/b" }],
    state,
  );
  expect(next.agent).toBe(doc.agent);
  expect(next.memory).toBe(doc.memory);
  expect(next.compaction).toBe(doc.compaction);
  expect(next.supervisor).toBe(doc.supervisor);

  next = applyModelsChange(
    next,
    [{ model: "demo/bc" }, { model: "demo/b" }],
    state,
  );
  expect(next.agent).toEqual({ model: "demo/bc" });
  expect(next.memory).toEqual({
    model: "demo/bc",
    fallback_models: ["demo/bc", "demo/b"],
  });
  expect(next.compaction).toEqual({
    model: "demo/b",
    fallback_models: ["demo/bc", "demo/b"],
  });
  expect(next.supervisor).toEqual({ model: "demo/b" });
});

test("correcting pre-existing duplicate model ids leaves their references alone", () => {
  const state = createModelCascadeState();
  const doc: Record<string, unknown> = {
    models: [{ model: "demo/a" }, { model: "demo/a" }],
    agent: { model: "demo/a" },
    memory: { model: "demo/a", fallback_models: ["demo/a"] },
    compaction: { model: "demo/a", fallback_models: ["demo/a"] },
    supervisor: { model: "demo/a" },
  };
  let next = applyModelsChange(
    doc,
    [{ model: "demo/b" }, { model: "demo/a" }],
    state,
  );
  expect(next.agent).toBe(doc.agent);
  expect(next.memory).toBe(doc.memory);

  next = applyModelsChange(
    next,
    [{ model: "demo/c" }, { model: "demo/a" }],
    state,
  );
  expect(next.agent).toEqual({ model: "demo/a" });
  expect(next.memory).toEqual({
    model: "demo/a",
    fallback_models: ["demo/a"],
  });
  expect(next.compaction).toEqual({
    model: "demo/a",
    fallback_models: ["demo/a"],
  });
  expect(next.supervisor).toEqual({ model: "demo/a" });
});

test("a logical model rename survives clearing and retyping the id", () => {
  const state = createModelCascadeState();
  let doc = documentWithReferences();
  doc = applyModelsChange(doc, [{ model: "" }, { model: "other/qwen" }], state);
  doc = applyModelsChange(
    doc,
    [{ model: "custom/deepseek/v5" }, { model: "other/qwen" }],
    state,
  );

  expect(doc.agent).toEqual({ model: "custom/deepseek/v5" });
  expect(doc.memory).toEqual({
    model: "custom/deepseek/v5",
    fallback_models: ["custom/deepseek/v5", "other/qwen"],
  });
  expect(doc.compaction).toEqual({
    model: "custom/deepseek/v5",
    fallback_models: ["other/qwen", "custom/deepseek/v5"],
  });
  expect(doc.supervisor).toEqual({ model: "custom/deepseek/v5" });
});

test("resolving a duplicate through the other provider completes both renames", () => {
  const state = createModelCascadeState();
  const duplicate = applyProvidersChange(
    documentWithReferences(),
    [{ name: "other", type: "openai" }, providers[1]],
    state,
  );
  const next = applyProvidersChange(
    duplicate,
    [(duplicate.providers as unknown[])[0], { name: "other2", type: "openai" }],
    state,
  );

  expect(next.models).toEqual([
    { model: "other/deepseek/v4" },
    { model: "other2/qwen" },
  ]);
  expect(next.agent).toEqual({ model: "other/deepseek/v4" });
  expect(next.memory).toEqual({
    model: "other/deepseek/v4",
    fallback_models: ["other/deepseek/v4", "other2/qwen"],
  });
  expect(next.compaction).toEqual({
    model: "other/deepseek/v4",
    fallback_models: ["other2/qwen", "other/deepseek/v4"],
  });
  expect(next.supervisor).toEqual({ model: "other/deepseek/v4" });
});

test("a blank provider reserves its former id until it receives another", () => {
  const state = createModelCascadeState();
  let next = applyProvidersChange(
    documentWithReferences(),
    [providers[0], { name: "", type: "openai" }],
    state,
  );
  next = applyProvidersChange(
    next,
    [{ name: "other", type: "openai" }, (next.providers as unknown[])[1]],
    state,
  );
  expect(next.models).toEqual([
    { model: "custom/deepseek/v4" },
    { model: "other/qwen" },
  ]);

  next = applyProvidersChange(
    next,
    [(next.providers as unknown[])[0], { name: "new", type: "openai" }],
    state,
  );
  expect(next.models).toEqual([
    { model: "other/deepseek/v4" },
    { model: "new/qwen" },
  ]);
  expect(next.agent).toEqual({ model: "other/deepseek/v4" });
  expect(next.memory).toEqual({
    model: "other/deepseek/v4",
    fallback_models: ["other/deepseek/v4", "new/qwen"],
  });
  expect(next.compaction).toEqual({
    model: "other/deepseek/v4",
    fallback_models: ["new/qwen", "other/deepseek/v4"],
  });
  expect(next.supervisor).toEqual({ model: "other/deepseek/v4" });
});

test("resolving a duplicate through the other model completes both renames", () => {
  const state = createModelCascadeState();
  const doc: Record<string, unknown> = {
    models: [{ model: "demo/a" }, { model: "demo/b" }],
    agent: { model: "demo/a" },
    memory: { model: "demo/a", fallback_models: ["demo/a", "demo/b"] },
    compaction: { model: "demo/b", fallback_models: ["demo/a", "demo/b"] },
    supervisor: { model: "demo/b" },
  };
  let next = applyModelsChange(
    doc,
    [{ model: "demo/b" }, { model: "demo/b" }],
    state,
  );
  next = applyModelsChange(
    next,
    [(next.models as unknown[])[0], { model: "demo/b2" }],
    state,
  );

  expect(next.models).toEqual([{ model: "demo/b" }, { model: "demo/b2" }]);
  expect(next.agent).toEqual({ model: "demo/b" });
  expect(next.memory).toEqual({
    model: "demo/b",
    fallback_models: ["demo/b", "demo/b2"],
  });
  expect(next.compaction).toEqual({
    model: "demo/b2",
    fallback_models: ["demo/b", "demo/b2"],
  });
  expect(next.supervisor).toEqual({ model: "demo/b2" });
});

test("a blank model row reserves its former id until it receives another", () => {
  const state = createModelCascadeState();
  let next: Record<string, unknown> = {
    models: [{ model: "demo/a" }, { model: "demo/b" }],
    agent: { model: "demo/a" },
    memory: { model: "demo/a", fallback_models: ["demo/a", "demo/b"] },
    compaction: { model: "demo/b", fallback_models: ["demo/a", "demo/b"] },
    supervisor: { model: "demo/b" },
  };
  next = applyModelsChange(
    next,
    [(next.models as unknown[])[0], { model: "" }],
    state,
  );
  next = applyModelsChange(
    next,
    [{ model: "demo/b" }, (next.models as unknown[])[1]],
    state,
  );
  expect(next.agent).toEqual({ model: "demo/a" });
  expect(next.compaction).toEqual({
    model: "demo/b",
    fallback_models: ["demo/a", "demo/b"],
  });

  next = applyModelsChange(
    next,
    [(next.models as unknown[])[0], { model: "demo/c" }],
    state,
  );
  expect(next.models).toEqual([{ model: "demo/b" }, { model: "demo/c" }]);
  expect(next.agent).toEqual({ model: "demo/b" });
  expect(next.memory).toEqual({
    model: "demo/b",
    fallback_models: ["demo/b", "demo/c"],
  });
  expect(next.compaction).toEqual({
    model: "demo/c",
    fallback_models: ["demo/b", "demo/c"],
  });
  expect(next.supervisor).toEqual({ model: "demo/c" });
});

test("provider rename rebases a transient duplicate model identity", () => {
  const state = createModelCascadeState();
  let next: Record<string, unknown> = {
    providers: [{ name: "custom", type: "openai" }],
    models: [{ model: "custom/a" }, { model: "custom/b" }],
    agent: { model: "custom/a" },
  };
  next = applyModelsChange(
    next,
    [{ model: "custom/b" }, { model: "custom/b" }],
    state,
  );
  next = applyProvidersChange(next, [{ name: "hub", type: "openai" }], state);
  next = applyModelsChange(
    next,
    [{ model: "hub/c" }, { model: "hub/b" }],
    state,
  );

  expect(next.models).toEqual([{ model: "hub/c" }, { model: "hub/b" }]);
  expect(next.agent).toEqual({ model: "hub/c" });
});

test("provider rename rebases a blank model row identity", () => {
  const state = createModelCascadeState();
  let next: Record<string, unknown> = {
    providers: [{ name: "custom", type: "openai" }],
    models: [{ model: "custom/a" }],
    agent: { model: "custom/a" },
  };
  next = applyModelsChange(next, [{ model: "" }], state);
  next = applyProvidersChange(next, [{ name: "hub", type: "openai" }], state);
  next = applyModelsChange(next, [{ model: "hub/b" }], state);

  expect(next.models).toEqual([{ model: "hub/b" }]);
  expect(next.agent).toEqual({ model: "hub/b" });
});

test("provider deletion removes a blank model by its preserved identity", () => {
  const state = createModelCascadeState();
  let next: Record<string, unknown> = {
    providers: [{ name: "custom", type: "openai" }],
    models: [{ model: "custom/a" }],
    agent: { model: "custom/a" },
  };
  next = applyModelsChange(next, [{ model: "" }], state);
  next = applyProvidersChange(next, [], state);

  expect(next.models).toEqual([]);
  expect(next.agent).toEqual({ model: "" });
});

test("provider deletion removes a duplicate visible model by its owner", () => {
  const state = createModelCascadeState();
  let next = documentWithReferences();
  next = applyModelsChange(
    next,
    [{ model: "other/qwen" }, { model: "other/qwen" }],
    state,
  );
  next = applyProvidersChange(next, [providers[1]], state);

  expect(next.models).toEqual([{ model: "other/qwen" }]);
  expect(next.agent).toEqual({ model: "" });
  expect(next.memory).toEqual({ model: "", fallback_models: ["other/qwen"] });
});

test("provider rename preserves an identity outside the rename map", () => {
  const state = createModelCascadeState();
  let next: Record<string, unknown> = {
    providers,
    models: [{ model: "other/a" }, { model: "custom/a" }],
    agent: { model: "other/a" },
  };
  next = applyModelsChange(
    next,
    [{ model: "custom/a" }, { model: "custom/a" }],
    state,
  );
  next = applyProvidersChange(
    next,
    [(next.providers as unknown[])[0], { name: "", type: "openai" }],
    state,
  );
  next = applyProvidersChange(
    next,
    [{ name: "hub", type: "openai" }, (next.providers as unknown[])[1]],
    state,
  );
  next = applyModelsChange(
    next,
    [{ model: "hub/b" }, { model: "hub/a" }],
    state,
  );

  expect(next.models).toEqual([{ model: "hub/b" }, { model: "hub/a" }]);
  expect(next.agent).toEqual({ model: "hub/b" });
});

test("provider rename rebases Kimi catalog metadata with the model", () => {
  const state = createModelCascadeState();
  const doc: Record<string, unknown> = {
    providers: [{ name: "kimi", type: "kimi" }],
    models: [
      {
        model: "kimi/k2",
        kimi_metadata: { model: "kimi/k2", credential_key: "managed" },
      },
    ],
    agent: { model: "kimi/k2" },
  };
  const next = applyProvidersChange(
    doc,
    [{ name: "hub", type: "kimi" }],
    state,
  );

  expect(next.models).toEqual([
    {
      model: "hub/k2",
      kimi_metadata: { model: "hub/k2", credential_key: "managed" },
    },
  ]);
  expect(next.agent).toEqual({ model: "hub/k2" });
});

test("provider deletion resolves visible ids by preserved ownership", () => {
  for (const deleted of ["hub", "other"]) {
    const state = createModelCascadeState();
    let next: Record<string, unknown> = {
      providers,
      models: [{ model: "other/a" }, { model: "custom/a" }],
      agent: { model: "other/a" },
    };
    next = applyModelsChange(
      next,
      [{ model: "custom/a" }, { model: "custom/a" }],
      state,
    );
    next = applyProvidersChange(
      next,
      [{ name: "hub", type: "openai" }, providers[1]],
      state,
    );
    expect(next.models).toEqual([{ model: "hub/a" }, { model: "hub/a" }]);

    next = applyProvidersChange(
      next,
      deleted === "hub"
        ? [(next.providers as unknown[])[1]]
        : [(next.providers as unknown[])[0]],
      state,
    );
    expect(next.models).toEqual([
      { model: deleted === "hub" ? "other/a" : "hub/a" },
    ]);
    expect(next.agent).toEqual({
      model: deleted === "hub" ? "other/a" : "",
    });
  }
});

test("a new provider does not inherit an id reserved by a blank row", () => {
  const state = createModelCascadeState();
  let next: Record<string, unknown> = {
    providers: [{ name: "custom", type: "openai" }],
    models: [{ model: "custom/a" }],
    agent: { model: "custom/a" },
  };
  next = applyProvidersChange(next, [{ name: "", type: "openai" }], state);
  next = applyProvidersChange(
    next,
    [(next.providers as unknown[])[0], { name: "custom", type: "openai" }],
    state,
  );
  next = applyProvidersChange(
    next,
    [
      (next.providers as unknown[])[0],
      {
        ...((next.providers as unknown[])[1] as Record<string, unknown>),
        type: "custom",
      },
    ],
    state,
  );
  next = applyProvidersChange(next, [(next.providers as unknown[])[1]], state);

  expect(next.models).toEqual([]);
  expect(next.agent).toEqual({ model: "" });
});

test("a new model does not inherit an id reserved by a blank row", () => {
  const state = createModelCascadeState();
  let next: Record<string, unknown> = {
    models: [{ model: "custom/a" }],
    agent: { model: "custom/a" },
  };
  next = applyModelsChange(next, [{ model: "" }], state);
  next = applyModelsChange(
    next,
    [(next.models as unknown[])[0], { model: "custom/a" }],
    state,
  );
  next = applyModelsChange(next, [(next.models as unknown[])[1]], state);
  next = applyModelsChange(next, [{ model: "custom/b" }], state);

  expect(next.models).toEqual([{ model: "custom/b" }]);
  expect(next.agent).toEqual({ model: "custom/b" });
});

test("an unrelated provider rename preserves a conflicting model id edit", () => {
  const state = createModelCascadeState();
  let next: Record<string, unknown> = {
    providers: [
      { name: "a", type: "openai" },
      { name: "b", type: "openai" },
      { name: "c", type: "openai" },
    ],
    models: [{ model: "a/x" }, { model: "b/x" }, { model: "c/x" }],
    agent: { model: "a/x" },
  };
  next = applyModelsChange(
    next,
    [{ model: "b/x" }, { model: "b/x" }, { model: "c/x" }],
    state,
  );
  next = applyProvidersChange(
    next,
    [
      { name: "a", type: "openai" },
      { name: "b", type: "openai" },
      { name: "d", type: "openai" },
    ],
    state,
  );

  expect(next.models).toEqual([
    { model: "b/x" },
    { model: "b/x" },
    { model: "d/x" },
  ]);
  expect(next.agent).toEqual({ model: "a/x" });
});

test("provider deletion preserves an unowned duplicate model row", () => {
  const state = createModelCascadeState();
  let next: Record<string, unknown> = {
    providers: [{ name: "custom", type: "openai" }],
    models: [{ model: "custom/a" }],
    agent: { model: "custom/a" },
  };
  next = applyModelsChange(
    next,
    [(next.models as unknown[])[0], { model: "custom/a" }],
    state,
  );
  next = applyProvidersChange(next, [], state);

  expect(next.models).toEqual([{ model: "custom/a" }]);
  expect(next.agent).toEqual({ model: "" });
});

test("reserved provider deletion follows the surviving owner's identity", () => {
  for (const removedIndex of [0, 1]) {
    const state = createModelCascadeState();
    let next: Record<string, unknown> = {
      providers: [{ name: "a", type: "openai" }],
      models: [],
      agent: { model: "" },
    };
    next = applyProvidersChange(next, [{ name: "", type: "openai" }], state);
    next = applyProvidersChange(
      next,
      [(next.providers as unknown[])[0], { name: "a", type: "openai" }],
      state,
    );
    next = applyModelsChange(next, [{ model: "a/x" }], state);
    next = { ...next, agent: { model: "a/x" } };
    next = applyProvidersChange(
      next,
      [(next.providers as unknown[])[removedIndex === 0 ? 1 : 0]],
      state,
    );

    expect(next.models).toEqual(removedIndex === 0 ? [] : [{ model: "a/x" }]);
    expect(next.agent).toEqual({
      model: removedIndex === 0 ? "" : "a/x",
    });
  }
});

test("provider rename leaves metadata of an unrelated model unchanged", () => {
  const state = createModelCascadeState();
  const next = applyProvidersChange(
    {
      providers,
      models: [
        {
          model: "other/model",
          kimi_metadata: { model: "custom/upstream" },
        },
      ],
      agent: { model: "other/model" },
    },
    [{ name: "hub", type: "openai" }, providers[1]],
    state,
  );

  expect(next.models).toEqual([
    { model: "other/model", kimi_metadata: { model: "custom/upstream" } },
  ]);
  expect(next.agent).toEqual({ model: "other/model" });
});

test("provider rename leaves unsupported fallback fields untouched", () => {
  const next = applyProvidersChange(
    {
      providers,
      models: [{ model: "custom/a" }, { model: "other/b" }],
      agent: { model: "custom/a", fallback_models: ["custom/a"] },
      memory: { model: "custom/a", fallback_models: ["custom/a"] },
      compaction: { model: "custom/a", fallback_models: ["custom/a"] },
      supervisor: { model: "custom/a", fallback_models: ["custom/a"] },
    },
    [{ name: "hub", type: "openai" }, providers[1]],
  );

  expect(next.agent).toEqual({
    model: "hub/a",
    fallback_models: ["custom/a"],
  });
  expect(next.memory).toEqual({
    model: "hub/a",
    fallback_models: ["hub/a"],
  });
  expect(next.compaction).toEqual({
    model: "hub/a",
    fallback_models: ["hub/a"],
  });
  expect(next.supervisor).toEqual({
    model: "hub/a",
    fallback_models: ["custom/a"],
  });
});

test("provider deletion leaves unsupported fallback fields untouched", () => {
  const next = applyProvidersChange(
    {
      providers,
      models: [{ model: "custom/a" }, { model: "other/b" }],
      agent: { model: "custom/a", fallback_models: ["custom/a"] },
      memory: { model: "custom/a", fallback_models: ["custom/a"] },
      compaction: { model: "custom/a", fallback_models: ["custom/a"] },
      supervisor: { model: "custom/a", fallback_models: ["custom/a"] },
    },
    [providers[1]],
  );

  expect(next.agent).toEqual({ model: "", fallback_models: ["custom/a"] });
  expect(next.memory).toEqual({ model: "", fallback_models: [] });
  expect(next.compaction).toEqual({ model: "", fallback_models: [] });
  expect(next.supervisor).toEqual({
    model: "",
    fallback_models: ["custom/a"],
  });
});

test("visible provider rename and deletion preserve the other model owner", () => {
  const state = createModelCascadeState();
  let next: Record<string, unknown> = {
    providers: [
      { name: "custom", type: "openai" },
      { name: "hub", type: "openai" },
    ],
    models: [{ model: "custom/m1" }, { model: "hub/m1" }],
    agent: { model: "custom/m1" },
  };
  next = applyModelsChange(
    next,
    [{ model: "hub/m1" }, { model: "hub/m1" }],
    state,
  );
  next = applyProvidersChange(
    next,
    [
      { name: "custom", type: "openai" },
      { name: "zzz", type: "openai" },
    ],
    state,
  );
  expect(next.models).toEqual([{ model: "zzz/m1" }, { model: "zzz/m1" }]);
  expect(next.agent).toEqual({ model: "custom/m1" });

  next = applyProvidersChange(next, [(next.providers as unknown[])[0]], state);
  expect(next.models).toEqual([{ model: "custom/m1" }]);
  expect(next.agent).toEqual({ model: "custom/m1" });
});

test("provider deletion does not promote a recorded-empty survivor", () => {
  const state = createModelCascadeState();
  let next: Record<string, unknown> = {
    providers: [{ name: "a", type: "openai" }],
    models: [],
    agent: { model: "" },
  };
  next = applyProvidersChange(next, [{ name: "", type: "openai" }], state);
  next = applyProvidersChange(
    next,
    [(next.providers as unknown[])[0], { name: "a", type: "openai" }],
    state,
  );
  next = applyProvidersChange(next, [(next.providers as unknown[])[1]], state);

  const survivor = (next.providers as unknown[])[0] as object;
  expect(state.providerRows.get(survivor)).toBe("");
  next = applyModelsChange(next, [{ model: "a/x" }], state);
  next = { ...next, agent: { model: "a/x" } };
  next = applyProvidersChange(next, [{ ...survivor, type: "custom" }], state);
  next = applyProvidersChange(next, [], state);

  expect(next.models).toEqual([{ model: "a/x" }]);
  expect(next.agent).toEqual({ model: "a/x" });
});

test("logical model rename keeps matching Kimi metadata consistent", () => {
  const state = createModelCascadeState();
  const next = applyModelsChange(
    {
      models: [
        {
          model: "kimi/k2",
          kimi_metadata: { model: "kimi/k2", credential_key: "managed" },
        },
      ],
      agent: { model: "kimi/k2" },
    },
    [
      {
        model: "kimi/k3",
        kimi_metadata: { model: "kimi/k2", credential_key: "managed" },
      },
    ],
    state,
  );

  expect(next.models).toEqual([
    {
      model: "kimi/k3",
      kimi_metadata: { model: "kimi/k3", credential_key: "managed" },
    },
  ]);
  expect(next.agent).toEqual({ model: "kimi/k3" });
});

test("renaming a recorded-empty provider survivor adopts its visible models", () => {
  const state = createModelCascadeState();
  let next: Record<string, unknown> = {
    providers: [{ name: "a", type: "openai" }],
    models: [],
    agent: { model: "" },
  };
  next = applyProvidersChange(next, [{ name: "", type: "openai" }], state);
  next = applyProvidersChange(
    next,
    [(next.providers as unknown[])[0], { name: "a", type: "openai" }],
    state,
  );
  next = applyProvidersChange(next, [(next.providers as unknown[])[1]], state);
  next = applyModelsChange(next, [{ model: "a/x" }], state);
  next = { ...next, agent: { model: "a/x" } };

  next = applyProvidersChange(next, [{ name: "hub", type: "openai" }], state);
  expect(next.models).toEqual([{ model: "hub/x" }]);
  expect(next.agent).toEqual({ model: "hub/x" });

  next = applyProvidersChange(next, [], state);
  expect(next.models).toEqual([]);
  expect(next.agent).toEqual({ model: "" });
});

test("a recorded-empty provider reserves its visible name for later additions", () => {
  const state = createModelCascadeState();
  let next: Record<string, unknown> = {
    providers: [{ name: "a", type: "openai" }],
    models: [{ model: "a/x" }],
    agent: { model: "a/x" },
  };
  next = applyProvidersChange(
    next,
    [(next.providers as unknown[])[0], { name: "a", type: "openai" }],
    state,
  );
  next = applyProvidersChange(next, [(next.providers as unknown[])[1]], state);
  next = applyModelsChange(next, [{ model: "a/x" }], state);
  next = { ...next, agent: { model: "a/x" } };
  next = applyProvidersChange(
    next,
    [(next.providers as unknown[])[0], { name: "a", type: "openai" }],
    state,
  );
  next = applyProvidersChange(next, [(next.providers as unknown[])[0]], state);

  expect(next.models).toEqual([{ model: "a/x" }]);
  expect(next.agent).toEqual({ model: "a/x" });
});

test("logical model rename updates metadata matching the previous visible id", () => {
  const state = createModelCascadeState();
  let next: Record<string, unknown> = {
    models: [{ model: "demo/a" }, { model: "other/b" }],
    agent: { model: "demo/a" },
  };
  next = applyModelsChange(
    next,
    [
      {
        model: "other/b",
        kimi_metadata: { model: "other/b", credential_key: "managed" },
      },
      { model: "other/b" },
    ],
    state,
  );
  next = applyModelsChange(
    next,
    [
      {
        model: "other/c",
        kimi_metadata: { model: "other/b", credential_key: "managed" },
      },
      { model: "other/b" },
    ],
    state,
  );

  expect(next.models).toEqual([
    {
      model: "other/c",
      kimi_metadata: { model: "other/c", credential_key: "managed" },
    },
    { model: "other/b" },
  ]);
  expect(next.agent).toEqual({ model: "other/c" });
});
