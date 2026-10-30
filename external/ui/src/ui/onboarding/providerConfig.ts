import type { FetchedModel } from "../settings/useProviderModels";

export type ProviderPresetId =
  | "openai"
  | "anthropic"
  | "ollama"
  | "compatible"
  | "neuraldeep"
  | "codex"
  | "opencode"
  | "devin";

export type ProviderPreset = {
  id: ProviderPresetId;
  label: string;
  descriptionKey: string;
  providerName: string;
  providerType:
    | "openai"
    | "anthropic"
    | "neuraldeep"
    | "codex"
    | "opencode"
    | "devin";
  apiBase?: string;
  multimodal: boolean;
  defaultModel: string;
  maxContextTokens?: number;
  envKey?: string;
};

export const NEURALDEEP_API_BASE_OPTIONS = [
  {
    value: "https://api.neuraldeep.ru/v1",
    labelKey: "neuralDeepApiBase.optionRu",
  },
  {
    value: "https://api.neuraldeep.tech/v1",
    labelKey: "neuralDeepApiBase.optionTech",
  },
] as const;
export const OPENCODE_API_BASE_OPTIONS = [
  {
    value: "https://opencode.ai/inference/go/v1",
    labelKey: "openCodeApiBase.optionGo",
  },
  {
    value: "https://opencode.ai/inference/v1",
    labelKey: "openCodeApiBase.optionConsole",
  },
] as const;

export const PROVIDER_PRESETS: [ProviderPreset, ...ProviderPreset[]] = [
  {
    id: "openai",
    label: "OpenAI",
    descriptionKey: "onboarding.provider.openai.description",
    providerName: "openai",
    providerType: "openai",
    multimodal: true,
    defaultModel: "openai/gpt-5.6-terra",
    envKey: "${OPENAI_API_KEY}",
  },
  {
    id: "anthropic",
    label: "Anthropic",
    descriptionKey: "onboarding.provider.anthropic.description",
    providerName: "anthropic",
    providerType: "anthropic",
    multimodal: true,
    defaultModel: "anthropic/claude-sonnet-5",
    envKey: "${ANTHROPIC_API_KEY}",
  },
  {
    id: "ollama",
    label: "Ollama",
    descriptionKey: "onboarding.provider.ollama.description",
    providerName: "local",
    providerType: "openai",
    apiBase: "http://127.0.0.1:11434/v1",
    multimodal: true,
    defaultModel: "local/llama3.2",
  },
  {
    id: "compatible",
    label: "OpenAI-compatible",
    descriptionKey: "onboarding.provider.compatible.description",
    providerName: "custom",
    providerType: "openai",
    multimodal: true,
    defaultModel: "custom/gpt-4o",
  },
  {
    id: "neuraldeep",
    label: "NeuralDeep",
    descriptionKey: "onboarding.provider.neuraldeep.description",
    providerName: "neuraldeep",
    providerType: "neuraldeep",
    apiBase: NEURALDEEP_API_BASE_OPTIONS[0].value,
    multimodal: false,
    defaultModel: "neuraldeep/qwen3.6-35b-a3b",
    maxContextTokens: 46112,
    envKey: "${NEURALDEEP_API_KEY}",
  },
  {
    id: "codex",
    label: "Codex",
    descriptionKey: "onboarding.provider.codex.description",
    providerName: "codex",
    providerType: "codex",
    multimodal: false,
    defaultModel: "codex/gpt-5.6-sol",
  },
  {
    id: "opencode",
    label: "OpenCode",
    descriptionKey: "onboarding.provider.opencode.description",
    providerName: "opencode",
    providerType: "opencode",
    apiBase: OPENCODE_API_BASE_OPTIONS[0].value,
    multimodal: false,
    defaultModel: "opencode/gpt-5.5",
    envKey: "${OPENCODE_API_KEY}",
  },
  {
    id: "devin",
    label: "Devin",
    descriptionKey: "onboarding.provider.devin.description",
    providerName: "devin",
    providerType: "devin",
    multimodal: true,
    defaultModel: "devin/claude-sonnet-5",
    envKey: "${DEVIN_API_KEY}",
  },
];

function object(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}

function rows(value: unknown): Record<string, unknown>[] {
  return Array.isArray(value)
    ? value.filter(
        (row): row is Record<string, unknown> =>
          !!row && typeof row === "object" && !Array.isArray(row),
      )
    : [];
}

export function catalogEntry(
  preset: ProviderPreset,
  modelId: string,
  catalog: FetchedModel[],
): FetchedModel | undefined {
  const raw = modelId.trim();
  if (!raw) return undefined;
  return catalog.find(
    (model) => model.id === raw || `${preset.providerName}/${model.id}` === raw,
  );
}

export function providerModelRef(
  preset: ProviderPreset,
  modelId: string,
): string {
  const raw = modelId.trim();
  if (!raw) return preset.defaultModel;
  return raw.includes("/") ? raw : `${preset.providerName}/${raw}`;
}

export function buildOnboardingConfig(input: {
  preset: ProviderPreset;
  apiKey: string;
  apiBase: string;
  proxy: string;
  modelId: string;
  baseDoc: Record<string, unknown>;
  catalog: FetchedModel[];
  managedLogin: boolean;
}): Record<string, unknown> {
  const { preset, baseDoc } = input;
  const providers = rows(baseDoc.providers);
  const existingProvider = providers.find(
    (row) => String(row.name || "") === preset.providerName,
  );
  const provider: Record<string, unknown> = {
    ...existingProvider,
    name: preset.providerName,
    type: preset.providerType,
  };
  const keyless =
    preset.providerType === "codex" ||
    (input.managedLogin &&
      !input.apiKey.trim() &&
      ["neuraldeep", "opencode", "devin"].includes(preset.providerType));
  if (keyless) {
    delete provider.api_key;
    delete provider.api_key_command;
  } else {
    provider.api_key =
      input.apiKey.trim() ||
      String(existingProvider?.api_key || "") ||
      preset.envKey ||
      "";
    delete provider.api_key_command;
  }
  if (preset.providerType === "codex" || preset.providerType === "devin") {
    delete provider.api_base;
  } else {
    const base = input.apiBase.trim() || preset.apiBase || "";
    if (base) provider.api_base = base;
    else delete provider.api_base;
  }
  if (input.proxy.trim()) provider.proxy = input.proxy.trim();
  else delete provider.proxy;
  const providerIndex = providers.findIndex(
    (row) => String(row.name || "") === preset.providerName,
  );
  if (providerIndex < 0) providers.push(provider);
  else providers[providerIndex] = provider;

  const model = providerModelRef(preset, input.modelId);
  const listed = catalogEntry(preset, input.modelId, input.catalog);
  const models = rows(baseDoc.models);
  const existingModel = models.find((row) => String(row.model || "") === model);
  const modelRow: Record<string, unknown> = { ...existingModel, model };
  modelRow.max_tokens =
    listed?.catalog?.max_tokens ?? existingModel?.max_tokens ?? 8192;
  const contextWindow =
    preset.maxContextTokens ||
    listed?.context_window ||
    listed?.catalog?.context_window;
  if (contextWindow) modelRow.max_context_tokens = contextWindow;
  modelRow.multimodal = listed?.catalog
    ? listed.catalog.multimodal === true
    : preset.multimodal;
  if (["openai", "codex", "devin"].includes(preset.id)) {
    modelRow.reasoning_default = existingModel?.reasoning_default ?? "medium";
    delete modelRow.temperature;
  } else if (!existingModel || existingModel.temperature === undefined) {
    modelRow.temperature = 0.2;
  }
  const modelIndex = models.findIndex(
    (row) => String(row.model || "") === model,
  );
  if (modelIndex < 0) models.push(modelRow);
  else models[modelIndex] = modelRow;

  return {
    ...baseDoc,
    providers,
    models,
    agent: {
      ...object(baseDoc.agent),
      model,
      max_turns: object(baseDoc.agent).max_turns ?? 35,
    },
  };
}
