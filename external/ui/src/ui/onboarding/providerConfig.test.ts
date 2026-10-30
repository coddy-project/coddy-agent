import { describe, expect, it } from "vitest";
import {
  buildOnboardingConfig,
  PROVIDER_PRESETS,
  providerModelRef,
} from "./providerConfig";

const preset = (id: string) => {
  const found = PROVIDER_PRESETS.find((item) => item.id === id);
  if (!found) throw new Error(`missing preset ${id}`);
  return found;
};

describe("buildOnboardingConfig", () => {
  it("merges the selected provider and model without dropping existing rows", () => {
    const doc = buildOnboardingConfig({
      preset: preset("openai"),
      apiKey: "sk-new",
      apiBase: "",
      proxy: "",
      modelId: "gpt-5.6-terra",
      baseDoc: {
        providers: [
          {
            name: "local",
            type: "openai",
            api_base: "http://localhost:1234/v1",
          },
        ],
        models: [{ model: "local/qwen", max_tokens: 0 }],
        agent: { max_turns: 0 },
      },
      catalog: [],
      managedLogin: false,
    });
    expect(doc.providers).toHaveLength(2);
    expect(doc.models).toHaveLength(2);
    expect(doc.agent).toEqual({ model: "openai/gpt-5.6-terra", max_turns: 0 });
    expect(doc.providers).toContainEqual(
      expect.objectContaining({
        name: "local",
        api_base: "http://localhost:1234/v1",
      }),
    );
    expect(doc.models).toContainEqual({ model: "local/qwen", max_tokens: 0 });
  });

  it.each(["opencode", "devin"])(
    "saves %s with a managed login and no key",
    (id) => {
      const doc = buildOnboardingConfig({
        preset: preset(id),
        apiKey: "",
        apiBase: "https://opencode.ai/inference/go/v1",
        proxy: "socks5h://127.0.0.1:1080",
        modelId: "",
        baseDoc: {
          providers: [
            {
              name: id,
              type: "openai",
              api_key: "old-key",
              api_key_command: "old-command",
            },
          ],
        },
        catalog: [],
        managedLogin: true,
      });
      const provider = (doc.providers as Record<string, unknown>[])[0]!;
      expect(provider.type).toBe(id);
      expect(provider.api_key).toBeUndefined();
      expect(provider.api_key_command).toBeUndefined();
      expect(provider.proxy).toBe("socks5h://127.0.0.1:1080");
      expect(doc.agent).toEqual({
        model: `${id}/${id === "devin" ? "claude-sonnet-5" : "gpt-5.5"}`,
        max_turns: 165,
      });
    },
  );

  it("lets a key typed after a managed login win", () => {
    const doc = buildOnboardingConfig({
      preset: preset("devin"),
      apiKey: "devin-session-token$typed",
      apiBase: "",
      proxy: "",
      modelId: "",
      baseDoc: {},
      catalog: [],
      managedLogin: true,
    });
    expect((doc.providers as Record<string, unknown>[])[0]!.api_key).toBe(
      "devin-session-token$typed",
    );
  });

  it("saves Ollama keyless on its local endpoint", () => {
    const doc = buildOnboardingConfig({
      preset: preset("ollama"),
      apiKey: "",
      apiBase: "http://127.0.0.1:11434/v1",
      proxy: "",
      modelId: "",
      baseDoc: {},
      catalog: [],
      managedLogin: false,
    });
    // A local api_base needs no key, and no fake credential is written.
    const provider = (doc.providers as Record<string, unknown>[])[0]!;
    expect(provider.api_key).toBeUndefined();
    expect(provider.api_key_command).toBeUndefined();
    expect(provider.api_base).toBe("http://127.0.0.1:11434/v1");
    expect(doc.models).toEqual([
      expect.objectContaining({
        model: "local/llama3.2",
        temperature: 0.2,
      }),
    ]);
    expect(
      (doc.models as Record<string, unknown>[])[0]!.reasoning_default,
    ).toBeUndefined();
  });

  it("uses catalog limits and image support for the picked model", () => {
    const doc = buildOnboardingConfig({
      preset: preset("opencode"),
      apiKey: "oc-key",
      apiBase: "https://opencode.ai/inference/v1",
      proxy: "",
      modelId: "gpt-5.5",
      baseDoc: {},
      catalog: [
        {
          id: "gpt-5.5",
          catalog: {
            context_window: 200000,
            max_tokens: 32000,
            multimodal: true,
          },
        },
      ],
      managedLogin: false,
    });
    expect(doc.models).toEqual([
      expect.objectContaining({
        model: "opencode/gpt-5.5",
        max_context_tokens: 200000,
        max_tokens: 32000,
        multimodal: true,
      }),
    ]);
  });

  it("saves no key when nothing is typed and none is stored", () => {
    const doc = buildOnboardingConfig({
      preset: preset("openai"),
      apiKey: "",
      apiBase: "",
      proxy: "",
      modelId: "",
      baseDoc: {},
      catalog: [],
      managedLogin: false,
    });
    const provider = (doc.providers as Record<string, unknown>[])[0]!;
    // The envKey placeholder must never be saved as a literal key - the
    // conventional OPENAI_API_KEY variable stands in for it server-side.
    expect(provider.api_key).toBeUndefined();
    expect(provider.api_key_command).toBeUndefined();
  });

  it("keeps a stored credential helper when nothing is typed", () => {
    const doc = buildOnboardingConfig({
      preset: preset("openai"),
      apiKey: "",
      apiBase: "",
      proxy: "",
      modelId: "",
      baseDoc: {
        providers: [
          { name: "openai", type: "openai", api_key_command: "op read foo" },
        ],
      },
      catalog: [],
      managedLogin: false,
    });
    const provider = (doc.providers as Record<string, unknown>[])[0]!;
    expect(provider.api_key_command).toBe("op read foo");
    expect(provider.api_key).toBeUndefined();
  });

  it("carries a listed model's window over the preset's default window", () => {
    const doc = buildOnboardingConfig({
      preset: preset("neuraldeep"),
      apiKey: "nd-key",
      apiBase: "https://api.neuraldeep.ru/v1",
      proxy: "",
      modelId: "qwen3.6-235b",
      baseDoc: {},
      catalog: [{ id: "qwen3.6-235b", context_window: 131072 }],
      managedLogin: false,
    });
    expect(doc.models).toEqual([
      expect.objectContaining({
        model: "neuraldeep/qwen3.6-235b",
        max_context_tokens: 131072,
      }),
    ]);
  });

  it("keeps the preset window for the default model nothing listed", () => {
    const doc = buildOnboardingConfig({
      preset: preset("neuraldeep"),
      apiKey: "nd-key",
      apiBase: "https://api.neuraldeep.ru/v1",
      proxy: "",
      modelId: "",
      baseDoc: {},
      catalog: [],
      managedLogin: false,
    });
    expect(doc.models).toEqual([
      expect.objectContaining({
        model: "neuraldeep/qwen3.6-35b-a3b",
        max_context_tokens: 46112,
      }),
    ]);
  });
});

describe("providerModelRef", () => {
  it("prefixes a bare id and preserves an already qualified ref", () => {
    expect(providerModelRef(preset("devin"), "claude-sonnet-5")).toBe(
      "devin/claude-sonnet-5",
    );
    expect(providerModelRef(preset("devin"), "devin/custom")).toBe(
      "devin/custom",
    );
  });

  it("prefixes a bare id that contains a slash of its own", () => {
    // SplitModelRef reads the first segment as the provider, so an id the
    // compatible preset's listing reports ("openai/gpt-4o" on Together or
    // OpenRouter) still needs the picked provider's prefix.
    expect(providerModelRef(preset("compatible"), "openai/gpt-4o")).toBe(
      "custom/openai/gpt-4o",
    );
  });
});
