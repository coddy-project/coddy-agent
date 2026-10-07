import { describe, expect, test } from "vitest";
import {
  contextWindowToPin,
  providerRowOfModel,
  providerUsesSubscriptionLogin,
  sharedAliasIsValid,
  sharedSubscriptionAckNeeded,
} from "./sharedModels";

describe("sharedAliasIsValid", () => {
  test("an empty alias keeps the model private and is not a mistake", () => {
    for (const empty of ["", "   ", undefined, null]) {
      expect(sharedAliasIsValid(empty), String(empty)).toBe(true);
    }
  });

  test("accepts what the server pattern accepts", () => {
    for (const alias of [
      "terra",
      "T",
      "0x",
      "gpt-5.6_sol",
      "a.b-c_d",
      "a".repeat(64),
    ]) {
      expect(sharedAliasIsValid(alias), alias).toBe(true);
    }
  });

  test("the server trims before it checks, so does the form", () => {
    expect(sharedAliasIsValid("  terra  ")).toBe(true);
  });

  test("refuses what the server pattern refuses", () => {
    for (const alias of [
      "-terra",
      ".terra",
      "_terra",
      "ter/ra",
      "ter ra",
      "терра",
      "terra!",
      "a".repeat(65),
    ]) {
      expect(sharedAliasIsValid(alias), alias).toBe(false);
    }
  });
});

describe("providerUsesSubscriptionLogin", () => {
  test("codex and devin always run on a subscription login", () => {
    expect(providerUsesSubscriptionLogin({ type: "codex" })).toBe(true);
    expect(providerUsesSubscriptionLogin({ type: "devin" })).toBe(true);
    // Their key fields are not credentials of the row: a key typed there
    // changes nothing about whose quota is lent.
    expect(
      providerUsesSubscriptionLogin({ type: "codex", api_key: "sk-123" }),
    ).toBe(true);
  });

  test("neuraldeep does only while the row names no key of its own", () => {
    expect(providerUsesSubscriptionLogin({ type: "neuraldeep" })).toBe(true);
    expect(
      providerUsesSubscriptionLogin({
        type: "neuraldeep",
        api_key: " ",
        api_key_command: "",
      }),
    ).toBe(true);
    expect(
      providerUsesSubscriptionLogin({ type: "neuraldeep", api_key: "nd-key" }),
    ).toBe(false);
    expect(
      providerUsesSubscriptionLogin({
        type: "neuraldeep",
        api_key_command: "pass show nd",
      }),
    ).toBe(false);
  });

  test("the other types, an unknown type and no row at all do not", () => {
    for (const type of ["openai", "anthropic", "coddy", "", "mystery"]) {
      expect(providerUsesSubscriptionLogin({ type }), type).toBe(false);
    }
    expect(providerUsesSubscriptionLogin({})).toBe(false);
    expect(providerUsesSubscriptionLogin(undefined)).toBe(false);
  });

  test("the type is trimmed like the server trims it", () => {
    expect(providerUsesSubscriptionLogin({ type: " codex " })).toBe(true);
  });
});

describe("providerRowOfModel", () => {
  const providers = [
    { name: "work", type: "codex" },
    { name: " lab ", type: "openai" },
  ];

  test("finds the row by the prefix of provider/model", () => {
    expect(providerRowOfModel("work/gpt-5", providers)?.type).toBe("codex");
    expect(providerRowOfModel("lab/m", providers)?.type).toBe("openai");
  });

  test("splits at the first slash only", () => {
    expect(providerRowOfModel("work/org/model", providers)?.type).toBe("codex");
  });

  test("an id without a provider prefix or with an unknown one has no row", () => {
    expect(providerRowOfModel("gpt-5", providers)).toBeUndefined();
    expect(providerRowOfModel("/gpt-5", providers)).toBeUndefined();
    expect(providerRowOfModel("nobody/gpt-5", providers)).toBeUndefined();
    expect(providerRowOfModel("", providers)).toBeUndefined();
  });
});

describe("sharedSubscriptionAckNeeded", () => {
  const providers = [
    { name: "work", type: "codex" },
    { name: "nd", type: "neuraldeep" },
    { name: "nd-key", type: "neuraldeep", api_key: "k" },
    { name: "oa", type: "openai" },
  ];

  test("needs a shared alias on a subscription-backed provider", () => {
    expect(
      sharedSubscriptionAckNeeded(
        { model: "work/gpt-5", shared_as: "terra" },
        providers,
      ),
    ).toBe(true);
    expect(
      sharedSubscriptionAckNeeded(
        { model: "nd/qwen", shared_as: "q" },
        providers,
      ),
    ).toBe(true);
  });

  test("a private model needs none, whatever runs it", () => {
    expect(
      sharedSubscriptionAckNeeded({ model: "work/gpt-5" }, providers),
    ).toBe(false);
    expect(
      sharedSubscriptionAckNeeded(
        { model: "work/gpt-5", shared_as: "  " },
        providers,
      ),
    ).toBe(false);
  });

  test("a provider with a key of its own needs none", () => {
    expect(
      sharedSubscriptionAckNeeded(
        { model: "nd-key/qwen", shared_as: "q" },
        providers,
      ),
    ).toBe(false);
    expect(
      sharedSubscriptionAckNeeded(
        { model: "oa/gpt", shared_as: "g" },
        providers,
      ),
    ).toBe(false);
  });

  test("a model whose provider is not in the document needs none", () => {
    expect(
      sharedSubscriptionAckNeeded(
        { model: "ghost/x", shared_as: "g" },
        providers,
      ),
    ).toBe(false);
    expect(sharedSubscriptionAckNeeded(undefined, providers)).toBe(false);
  });
});

describe("contextWindowToPin", () => {
  test("a row of a provider type that reports no live window pins what the listing says", () => {
    expect(contextWindowToPin("openai", 131072)).toBe(131072);
    expect(contextWindowToPin(undefined, 8192)).toBe(8192);
    expect(contextWindowToPin("codex", undefined)).toBeUndefined();
  });

  test("a coddy row never pins it: the remote's listing is the source", () => {
    expect(contextWindowToPin("coddy", 131072)).toBeUndefined();
    expect(contextWindowToPin(" coddy ", 131072)).toBeUndefined();
  });
});
