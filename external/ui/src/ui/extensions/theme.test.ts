import { expect, test, vi } from "vitest";
import {
  EXTENSION_THEME_COLOR_VARS,
  ExtensionThemes,
  readExtensionTheme,
  type ExtensionTheme,
  type ExtensionThemeColor,
} from "./theme";
import { UI_THEME_IDS } from "../theme/themeCookie";
import { stylesSource } from "../../stylesSource";

function fakeTheme(id: ExtensionTheme["id"], accent: string): ExtensionTheme {
  const colors = {} as Record<ExtensionThemeColor, string>;
  for (const name of Object.keys(
    EXTENSION_THEME_COLOR_VARS,
  ) as ExtensionThemeColor[])
    colors[name] = accent;
  return Object.freeze({
    id,
    mode: id === "light" ? ("light" as const) : ("dark" as const),
    colors: Object.freeze(colors),
  });
}

/** A host stand-in: the theme it reads is a variable, the subscription is a set. */
function fakeHost(initial: ExtensionTheme) {
  let current = initial;
  const subscriptions = new Set<() => void>();
  const reports: string[] = [];
  const themes = new ExtensionThemes(
    (owner, message) => reports.push(`${owner}: ${message}`),
    () => current,
    (onChange) => {
      subscriptions.add(onChange);
      return () => subscriptions.delete(onChange);
    },
  );
  return {
    themes,
    reports,
    switchTo(theme: ExtensionTheme) {
      current = theme;
      for (const onChange of [...subscriptions]) onChange();
    },
    get subscriptions() {
      return subscriptions.size;
    },
  };
}

test("an extension reads the theme colours and follows a theme switch", () => {
  const host = fakeHost(fakeTheme("dark", "#9333ea"));
  const controller = new AbortController();
  const api = host.themes.api("demo", controller.signal);
  expect(api.get().id).toBe("dark");
  expect(api.get().mode).toBe("dark");
  expect(api.get().colors.accent).toBe("#9333ea");
  const seen: Readonly<ExtensionTheme>[] = [];
  api.onChange((theme) => {
    seen.push(theme);
  });
  host.switchTo(fakeTheme("light", "#6d28d9"));
  expect(seen).toHaveLength(1);
  expect(seen[0]).toMatchObject({ id: "light", mode: "light" });
  expect(seen[0]!.colors.accent).toBe("#6d28d9");
  expect(Object.isFrozen(seen[0])).toBe(true);
  expect(Object.isFrozen(seen[0]!.colors)).toBe(true);
  expect(api.get().id).toBe("light");
});

test("the theme snapshot names the resolved colours of the active theme", () => {
  document.documentElement.dataset.theme = "nord";
  try {
    const theme = readExtensionTheme();
    expect(theme.id).toBe("nord");
    expect(theme.mode).toBe("dark");
    expect(Object.keys(theme.colors).sort()).toEqual(
      Object.keys(EXTENSION_THEME_COLOR_VARS).sort(),
    );
    for (const name of Object.keys(theme.colors) as ExtensionThemeColor[])
      expect(typeof theme.colors[name], name).toBe("string");
    expect(Object.isFrozen(theme)).toBe(true);
  } finally {
    document.documentElement.dataset.theme = "dark";
  }
  document.documentElement.dataset.theme = "light";
  try {
    expect(readExtensionTheme().mode).toBe("light");
  } finally {
    document.documentElement.dataset.theme = "dark";
  }
});

test("the colours come from the theme tokens resolved through a probe", () => {
  document.documentElement.dataset.theme = "dark";
  const resolved: Record<string, string> = {};
  for (const [name, token] of Object.entries(EXTENSION_THEME_COLOR_VARS))
    resolved[`var(${token})`] = `rgb(${name.length}, 0, 0)`;
  const probe = document.createElement("span");
  const doc = {
    createElement: () => probe,
    body: document.body,
    defaultView: {
      getComputedStyle: (el: HTMLElement) => ({
        get color() {
          return resolved[el.style.color] ?? "";
        },
      }),
    },
  } as unknown as Document;
  try {
    const theme = readExtensionTheme(doc);
    for (const [name, token] of Object.entries(EXTENSION_THEME_COLOR_VARS))
      expect(theme.colors[name as ExtensionThemeColor], token).toBe(
        resolved[`var(${token})`],
      );
    expect(document.body.contains(probe)).toBe(false);
  } finally {
    document.documentElement.dataset.theme = "dark";
  }
});

test("every theme colour names a variable the stylesheet defines", () => {
  const css = stylesSource();
  for (const token of Object.values(EXTENSION_THEME_COLOR_VARS))
    expect(css, `${token} should be defined`).toContain(`${token}:`);
});

test("delivery runs by extension id in ascending ASCII order over one snapshot", () => {
  const read = vi.fn(() => fakeTheme("dark", "#111111"));
  const themes = new ExtensionThemes(
    () => {},
    read,
    () => () => {},
  );
  const seen: string[] = [];
  themes.on("zeta", new AbortController().signal, () => {
    seen.push("zeta");
  });
  themes.on("alpha", new AbortController().signal, () => {
    seen.push("alpha");
  });
  themes.changed();
  expect(seen).toEqual(["alpha", "zeta"]);
  expect(read).toHaveBeenCalledTimes(1);
});

test("a failing theme listener is removed and its error is reported", async () => {
  const host = fakeHost(fakeTheme("dark", "#222222"));
  const controller = new AbortController();
  const api = host.themes.api("demo", controller.signal);
  const seen: number[] = [];
  api.onChange(() => {
    throw new Error("boom");
  });
  api.onChange(() => {
    seen.push(1);
    return Promise.reject(new Error("later boom"));
  });
  api.onChange(() => {
    seen.push(2);
  });
  host.switchTo(fakeTheme("dark", "#333333"));
  await Promise.resolve();
  await Promise.resolve();
  expect(seen).toEqual([1, 2]);
  expect(host.reports).toEqual([
    "demo: Error: boom",
    "demo: Error: later boom",
  ]);
  seen.length = 0;
  host.switchTo(fakeTheme("dark", "#444444"));
  await Promise.resolve();
  await Promise.resolve();
  expect(seen).toEqual([2]);
  expect(host.reports).toHaveLength(2);
});

test("the extension lifetime removes theme listeners and stops the subscription", () => {
  const host = fakeHost(fakeTheme("dark", "#555555"));
  const controller = new AbortController();
  const api = host.themes.api("demo", controller.signal);
  const listener = vi.fn();
  api.onChange(listener);
  expect(host.subscriptions).toBe(1);
  controller.abort();
  expect(host.subscriptions).toBe(0);
  host.switchTo(fakeTheme("dark", "#666666"));
  expect(listener).not.toHaveBeenCalled();
  expect(() => api.get()).toThrow("Extension disposed");
  expect(() => api.onChange(listener)).toThrow("Extension disposed");
  expect(() => host.themes.on("demo", controller.signal, listener)).toThrow(
    "Extension disposed",
  );
});

test("a theme listener must be a function", () => {
  const host = fakeHost(fakeTheme("dark", "#777777"));
  const api = host.themes.api("demo", new AbortController().signal);
  expect(() => api.onChange(null as unknown as () => unknown)).toThrow(
    "Invalid theme change listener",
  );
  expect(UI_THEME_IDS).toContain(api.get().id);
});
