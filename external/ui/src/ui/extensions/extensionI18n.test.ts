import { describe, expect, test, vi } from "vitest";
import { ExtensionI18n } from "./extensionI18n";

describe("extension dictionaries", () => {
  test("keys are forced under ext.<id> and fall back to en then the key", () => {
    let locale = "ru";
    const i18n = new ExtensionI18n(() => locale, vi.fn());
    const api = i18n.api("demo", new AbortController().signal);
    api.registerMessages("en", { hello: "Hello {name}" });
    api.registerMessages("ru", { hello: "Привет {name}" });
    expect(api.t("hello", { name: "Coddy" })).toBe("Привет Coddy");
    locale = "de";
    expect(api.t("hello", { name: "Coddy" })).toBe("Hello Coddy");
    expect(api.t("missing")).toBe("ext.demo.missing");
  });

  test("a registered key may carry its prefix already", () => {
    const i18n = new ExtensionI18n(() => "en", vi.fn());
    const api = i18n.api("demo", new AbortController().signal);
    api.registerMessages("en", { "ext.demo.k": "v" });
    expect(api.t("k")).toBe("v");
  });

  test("re-registering a locale replaces the dictionary; remove drops it", () => {
    const i18n = new ExtensionI18n(() => "en", vi.fn());
    const api = i18n.api("demo", new AbortController().signal);
    api.registerMessages("en", { k: "one" });
    const second = api.registerMessages("en", { k: "two" });
    expect(api.t("k")).toBe("two");
    second.remove();
    expect(api.t("k")).toBe("ext.demo.k");
  });

  test("locale listeners fire on notify and lose on throw", () => {
    const onError = vi.fn();
    const i18n = new ExtensionI18n(() => "en", onError);
    const signal = new AbortController().signal;
    const api = i18n.api("demo", signal);
    const good = vi.fn();
    api.onChange(() => {
      throw new Error("boom");
    });
    api.onChange(good);
    i18n.notifyLocale("ru");
    i18n.notifyLocale("en");
    expect(good).toHaveBeenCalledTimes(2);
    expect(onError).toHaveBeenCalledOnce();
  });

  test("invalid locales and non-string values are refused", () => {
    const i18n = new ExtensionI18n(() => "en", vi.fn());
    const api = i18n.api("demo", new AbortController().signal);
    expect(() => api.registerMessages("nope!", { k: "v" })).toThrow(
      /Invalid locale/,
    );
    expect(() => api.registerMessages("en", { k: 1 as never })).toThrow(
      /strings/,
    );
  });
});
