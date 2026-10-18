import { expect, test } from "vitest";
import {
  contextWindowToPin,
  isCoddyType,
  listedLevels,
  providerRowOfModel,
  providerUsesSubscriptionLogin,
  sharedAliasIsValid,
  sharedSubscriptionAckNeeded,
  triStateOf,
  triStateValue,
  type TriState,
} from "./sharedModels";
import type { ProviderRow } from "./useProviderModels";

// The provider rows of a form with every kind of credential: an API key, a
// subscription login, a NeuralDeep row with a key and one without.
const PROVIDERS: ProviderRow[] = [
  { name: "openai", type: "openai", api_key: "sk-1" },
  { name: "anthropic", type: "anthropic" },
  { name: "codex", type: "codex" },
  { name: "devin", type: "devin" },
  { name: "hub", type: "neuraldeep" },
  { name: "hub-key", type: "neuraldeep", api_key: "nd-1" },
  { name: "hub-cmd", type: "neuraldeep", api_key_command: "pass show nd" },
  { name: "terra", type: "coddy", api_base: "https://terra:12345" },
];

test("only the coddy type counts as a model another Coddy shares", () => {
  expect(isCoddyType("coddy")).toBe(true);
  expect(isCoddyType("  coddy  ")).toBe(true);
  for (const other of ["openai", "codex", "Coddy", "", undefined, null]) {
    expect(isCoddyType(other), String(other)).toBe(false);
  }
});

test("an empty or blank alias keeps the model private and is no mistake", () => {
  for (const blank of ["", "   ", undefined, null]) {
    expect(sharedAliasIsValid(blank), String(blank)).toBe(true);
  }
});

test("an alias of letters, digits, dots, underscores and hyphens up to 64 characters is valid", () => {
  for (const alias of [
    "terra",
    "T",
    "7",
    "qwen3.8-27b",
    "my_model.v2-final",
    "a".repeat(64),
  ]) {
    expect(sharedAliasIsValid(alias), alias).toBe(true);
  }
});

test("an alias too long, with a slash, a leading dot or hyphen or a space inside is refused", () => {
  for (const alias of [
    "a".repeat(65),
    "team/terra",
    "/terra",
    ".terra",
    "-terra",
    "_terra",
    "my model",
    "terra!",
  ]) {
    expect(sharedAliasIsValid(alias), alias).toBe(false);
  }
});

test("whitespace around an alias is trimmed before it is judged", () => {
  expect(sharedAliasIsValid("  terra  ")).toBe(true);
  expect(sharedAliasIsValid(` ${"a".repeat(64)}\t`)).toBe(true);
  expect(sharedAliasIsValid("  -terra  ")).toBe(false);
});

test("codex and devin rows sign in with a subscription, a NeuralDeep row only without a key", () => {
  const byName = (name: string) => PROVIDERS.find((p) => p.name === name);
  expect(providerUsesSubscriptionLogin(byName("codex"))).toBe(true);
  expect(providerUsesSubscriptionLogin(byName("devin"))).toBe(true);
  expect(providerUsesSubscriptionLogin(byName("hub"))).toBe(true);
  expect(providerUsesSubscriptionLogin(byName("hub-key"))).toBe(false);
  expect(providerUsesSubscriptionLogin(byName("hub-cmd"))).toBe(false);
  // A blank key is no key.
  expect(
    providerUsesSubscriptionLogin({
      name: "hub",
      type: "neuraldeep",
      api_key: "  ",
      api_key_command: "",
    }),
  ).toBe(true);
});

test("API-key providers, a coddy row and a missing row are no subscription login", () => {
  const byName = (name: string) => PROVIDERS.find((p) => p.name === name);
  expect(providerUsesSubscriptionLogin(byName("openai"))).toBe(false);
  expect(providerUsesSubscriptionLogin(byName("anthropic"))).toBe(false);
  expect(providerUsesSubscriptionLogin(byName("terra"))).toBe(false);
  expect(providerUsesSubscriptionLogin(undefined)).toBe(false);
});

test("a model id finds its provider row by the name before the first slash", () => {
  expect(providerRowOfModel("codex/gpt-5.5", PROVIDERS)?.name).toBe("codex");
  // The provider name is trimmed, as the list trims it.
  expect(providerRowOfModel("  terra /qwen", PROVIDERS)?.name).toBe("terra");
  // An API model id with slashes of its own still points at the first part.
  expect(
    providerRowOfModel("hub/meta-llama/Llama-3.3-70B", PROVIDERS)?.name,
  ).toBe("hub");
});

test("a model id without a provider part points at no row", () => {
  // A row the operator has not named yet must not answer for an id that
  // names no provider.
  const withUnnamed: ProviderRow[] = [
    ...PROVIDERS,
    { name: "", type: "codex" },
  ];
  expect(providerRowOfModel("gpt-5.5", withUnnamed)).toBeUndefined();
  expect(providerRowOfModel("/gpt-5.5", withUnnamed)).toBeUndefined();
  expect(providerRowOfModel("", withUnnamed)).toBeUndefined();
  expect(providerRowOfModel("nobody/gpt-5.5", withUnnamed)).toBeUndefined();
});

test("a private model never needs the subscription acknowledgement", () => {
  expect(sharedSubscriptionAckNeeded(undefined, PROVIDERS)).toBe(false);
  expect(
    sharedSubscriptionAckNeeded({ model: "codex/gpt-5.5" }, PROVIDERS),
  ).toBe(false);
  expect(
    sharedSubscriptionAckNeeded(
      { model: "codex/gpt-5.5", shared_as: "   " },
      PROVIDERS,
    ),
  ).toBe(false);
});

test("sharing a model of a subscription login needs the acknowledgement", () => {
  for (const model of ["codex/gpt-5.5", "devin/swe-1", "hub/qwen3"]) {
    expect(
      sharedSubscriptionAckNeeded({ model, shared_as: "terra" }, PROVIDERS),
      model,
    ).toBe(true);
  }
});

test("sharing a model of an API-key provider needs no acknowledgement", () => {
  for (const model of ["openai/gpt-4o", "hub-key/qwen3", "terra/remote"]) {
    expect(
      sharedSubscriptionAckNeeded({ model, shared_as: "terra" }, PROVIDERS),
      model,
    ).toBe(false);
  }
});

test("a model added from a coddy listing inherits its context window; others pin the reported one", () => {
  expect(contextWindowToPin("coddy", 131072)).toBeUndefined();
  expect(contextWindowToPin(" coddy ", 131072)).toBeUndefined();
  expect(contextWindowToPin("openai", 131072)).toBe(131072);
  expect(contextWindowToPin(undefined, 8192)).toBe(8192);
  expect(contextWindowToPin("openai", undefined)).toBeUndefined();
});

test("an absent or null key follows the remote, true reads yes and false reads no", () => {
  expect(triStateOf(undefined)).toBe("remote");
  expect(triStateOf(null)).toBe("remote");
  expect(triStateOf(true)).toBe("yes");
  expect(triStateOf(false)).toBe("no");
});

test("the remote position removes the key, yes writes true and no writes false", () => {
  expect(triStateValue("remote")).toBeUndefined();
  expect(triStateValue("yes")).toBe(true);
  expect(triStateValue("no")).toBe(false);
  // Every position reads back as itself once written.
  for (const state of ["remote", "yes", "no"] as TriState[]) {
    expect(triStateOf(triStateValue(state))).toBe(state);
  }
});

test("a listing without the model or without levels offers no levels", () => {
  expect(listedLevels(undefined)).toEqual([]);
  expect(listedLevels({ id: "m1" })).toEqual([]);
  expect(listedLevels({ id: "m1", reasoning_levels: [] })).toEqual([]);
  // Off alone is no list of levels.
  expect(
    listedLevels({ id: "m1", reasoning_levels: [], allow_reasoning_off: true }),
  ).toEqual([]);
});

test("the listed levels keep their order, with off after them only when the remote allows it", () => {
  const levels = ["low", "medium", "high"];
  expect(listedLevels({ id: "m1", reasoning_levels: levels })).toEqual(levels);
  expect(
    listedLevels({
      id: "m1",
      reasoning_levels: levels,
      allow_reasoning_off: false,
    }),
  ).toEqual(levels);
  expect(
    listedLevels({
      id: "m1",
      reasoning_levels: levels,
      allow_reasoning_off: true,
    }),
  ).toEqual([...levels, "off"]);
  // The listing itself is not changed by appending off.
  expect(levels).toEqual(["low", "medium", "high"]);
});
