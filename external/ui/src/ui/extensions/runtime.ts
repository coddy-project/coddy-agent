import * as React from "react";
import { requireEnvironmentReload } from "../env/remoteEnv";
import { extensionNetwork, type ExtensionNetwork } from "./network";
import {
  Contributions,
  validateField,
  validateRenderer,
  type SettingsField,
  type ToolRenderer,
  type Owned,
  type ContributionHandle,
} from "./contributions";
import { ExtensionEvents, type ExtensionEventAPI } from "./events";
import { extensionSettings, type ExtensionSettingsAPI } from "./settings";
import { knownSectionLabel } from "../settings/settingsSections";
import {
  IncomingStream,
  type IncomingRegistration,
  type IncomingHandle,
} from "./incomingStream";

export type ExtensionEntry = {
  api: number;
  id: string;
  name: string;
  version: string;
  description?: string;
  sha256: string;
  entry_sha256: string;
  enabled: boolean;
  ui: { entry: string; styles?: string[] };
};

export type ExtensionPage = {
  id: string;
  title: string;
  mount: (element: HTMLElement, api: ExtensionAPI) => void | (() => void);
};
export type MenuItem = { id: string; title: string; onSelect: () => void };
export type Handle<T> = {
  update: (patch: Partial<Omit<T, "id">>) => void;
  remove: () => void;
};
export type ExtensionAPI = {
  api: 1;
  id: string;
  signal: AbortSignal;
  React: typeof React;
  assets: { text: (path: string) => Promise<string> };
  net: ExtensionNetwork;
  events: ExtensionEventAPI;
  settings: ExtensionSettingsAPI;
  stream: {
    registerIncoming: (registration: IncomingRegistration) => IncomingHandle;
  };
  ui: {
    registerPage: (page: ExtensionPage) => Handle<ExtensionPage>;
    registerMenuItem: (item: MenuItem) => Handle<MenuItem>;
    openPage: (id: string) => void;
    registerToolRenderer: (
      renderer: ToolRenderer,
    ) => ContributionHandle<ToolRenderer>;
  };
};
export type RuntimePage = ExtensionPage & {
  key: string;
  owner: string;
  api: ExtensionAPI;
};
export type RuntimeMenu = MenuItem & { key: string; owner: string };
export type RuntimeSnapshot = {
  pages: RuntimePage[];
  menu: RuntimeMenu[];
  fields: Owned<SettingsField>[];
  renderers: Owned<ToolRenderer>[];
  errors: Record<string, string>;
};

/** One lifetime per environment and exact bundle digest. Every host-owned
 * registration and request is disposed; evaluated origin code still needs a reload. */
export class ExtensionRuntime {
  private listeners = new Set<() => void>();
  private lifetimes = new Map<
    string,
    { digest: string; controller: AbortController; dispose: (() => void)[] }
  >();
  private state: RuntimeSnapshot = {
    pages: [],
    menu: [],
    fields: [],
    renderers: [],
    errors: {},
  };
  readonly fields = new Contributions<SettingsField>(() =>
    this.publish({ fields: this.fields.rows }),
  );
  readonly renderers = new Contributions<ToolRenderer>(() =>
    this.publish({ renderers: this.renderers.rows }),
  );
  readonly events = new ExtensionEvents((owner, message) =>
    this.reportError(owner, message),
  );
  reportError(owner: string, message: string) {
    this.publish({ errors: { ...this.state.errors, [owner]: message } });
  }
  failRenderer(key: string, owner: string, error: unknown) {
    this.renderers.remove(key);
    this.reportError(owner, String(error));
  }
  private evaluated = false;
  private evaluatedDigests = new Map<string, string>();
  private failed = new Map<string, string>();
  readonly incoming = new IncomingStream((owner, message) => {
    this.publish({ errors: { ...this.state.errors, [owner]: message } });
  });
  constructor(
    private open: (page: RuntimePage) => void,
    private loadModule = (
      url: string,
    ): Promise<{ activate: (api: ExtensionAPI) => unknown }> =>
      import(/* @vite-ignore */ url),
    private reload = () => window.location.reload(),
  ) {}
  snapshot = () => this.state;
  subscribe = (fn: () => void) => {
    this.listeners.add(fn);
    return () => {
      this.listeners.delete(fn);
    };
  };
  private publish(patch: Partial<RuntimeSnapshot>) {
    this.state = { ...this.state, ...patch };
    this.listeners.forEach((fn) => fn());
  }
  private dispose(id: string) {
    const life = this.lifetimes.get(id);
    if (!life) return;
    life.controller.abort();
    this.lifetimes.delete(id);
    for (const cleanup of life.dispose.reverse()) {
      try {
        cleanup();
      } catch (e) {
        console.error(e);
      }
    }
    this.publish({
      pages: this.state.pages.filter((p) => p.owner !== id),
      menu: this.state.menu.filter((p) => p.owner !== id),
    });
  }
  stop() {
    for (const id of this.lifetimes.keys()) this.dispose(id);
  }
  async reconcile(entries: ExtensionEntry[]) {
    for (const [id, digest] of this.evaluatedDigests) {
      if (
        !entries.some((e) => e.id === id && e.enabled && e.sha256 === digest)
      ) {
        this.dispose(id);
        this.reload();
        return;
      }
    }
    for (const [id, life] of this.lifetimes) {
      if (
        !entries.some(
          (e) => e.id === id && e.enabled && e.sha256 === life.digest,
        )
      ) {
        this.dispose(id);
        if (this.evaluated) {
          this.reload();
          return;
        }
      }
    }
    await Promise.all(
      entries
        .filter(
          (e) =>
            e.enabled &&
            !this.lifetimes.has(e.id) &&
            this.failed.get(e.id) !== e.sha256,
        )
        .map((e) => this.activate(e)),
    );
  }
  private async activate(entry: ExtensionEntry) {
    const controller = new AbortController();
    const life = {
      digest: entry.sha256,
      controller,
      dispose: [] as (() => void)[],
    };
    this.lifetimes.set(entry.id, life);
    const active = () =>
      !controller.signal.aborted && this.lifetimes.get(entry.id) === life;
    const assetBytes = async (path: string) => {
      if (!active()) throw new DOMException("Extension disposed", "AbortError");
      const encoded = path.split("/").map(encodeURIComponent).join("/");
      const response = await fetch(
        `/coddy/extensions/${encodeURIComponent(entry.id)}/assets/${encoded}?sha256=${encodeURIComponent(entry.sha256)}`,
        { signal: controller.signal },
      );
      if (!response.ok)
        throw new Error(`Asset ${path}: HTTP ${response.status}`);
      const bytes = await response.arrayBuffer();
      if (!active()) throw new DOMException("Extension disposed", "AbortError");
      return bytes;
    };
    const assetText = async (path: string) =>
      new TextDecoder().decode(await assetBytes(path));
    const register = <T extends ExtensionPage | MenuItem>(
      kind: "pages" | "menu",
      value: T,
    ): Handle<T> => {
      if (!active()) throw new Error("Extension disposed");
      if (!/^[a-z][a-z0-9-]{0,63}$/.test(value.id) || !value.title?.trim())
        throw new Error("A control needs an id and title");
      const key = `${entry.id}/${value.id}`;
      if (this.state[kind].some((row) => row.key === key))
        throw new Error(`Duplicate control: ${key}`);
      const row = { ...value, key, owner: entry.id, api };
      if (kind === "pages")
        this.publish({ pages: [...this.state.pages, row as RuntimePage] });
      else this.publish({ menu: [...this.state.menu, row as RuntimeMenu] });
      let removed = false;
      return {
        update: (patch) => {
          if (!active() || removed) return;
          if (kind === "pages")
            this.publish({
              pages: this.state.pages.map((p) =>
                p.key === key
                  ? ({
                      ...p,
                      ...patch,
                      id: value.id,
                      key,
                      owner: entry.id,
                      api,
                    } as RuntimePage)
                  : p,
              ),
            });
          else
            this.publish({
              menu: this.state.menu.map((p) =>
                p.key === key
                  ? ({
                      ...p,
                      ...patch,
                      id: value.id,
                      key,
                      owner: entry.id,
                    } as RuntimeMenu)
                  : p,
              ),
            });
        },
        remove: () => {
          if (!active() || removed) return;
          removed = true;
          this.publish(
            kind === "pages"
              ? { pages: this.state.pages.filter((p) => p.key !== key) }
              : { menu: this.state.menu.filter((p) => p.key !== key) },
          );
        },
      };
    };
    const api: ExtensionAPI = {
      api: 1,
      id: entry.id,
      signal: controller.signal,
      React,
      assets: { text: assetText },
      net: extensionNetwork(entry.id, entry.sha256, controller.signal),
      events: {
        on: (type, listener) =>
          this.events.on(entry.id, controller.signal, type, listener),
      },
      settings: extensionSettings(
        entry.id,
        controller.signal,
        (field) => {
          if (!knownSectionLabel(field.section))
            throw new Error("Unknown settings section");
          if (
            this.fields.rows.filter((row) => row.owner === entry.id).length >=
            128
          )
            throw new Error("Too many extension fields");
          return this.fields.register(entry.id, api, field, (value) => {
            validateField(value);
            if (!knownSectionLabel(value.section))
              throw new Error("Unknown settings section");
          });
        },
        () => this.fields.rows.filter((row) => row.owner === entry.id),
      ),
      stream: {
        registerIncoming: ({ id, transform }) => {
          if (!active()) throw new Error("Extension disposed");
          return this.incoming.register(
            entry.id,
            id,
            controller.signal,
            transform,
          );
        },
      },
      ui: {
        registerToolRenderer: (renderer) =>
          this.renderers.register(entry.id, api, renderer, validateRenderer),
        registerPage: (page) => register("pages", page),
        registerMenuItem: (item) => register("menu", item),
        openPage: (id) => {
          if (!active()) return;
          const page = this.state.pages.find(
            (p) => p.key === `${entry.id}/${id}`,
          );
          if (page) this.open(page);
        },
      },
    };
    const timer = window.setTimeout(() => {
      if (active()) {
        this.failed.set(entry.id, entry.sha256);
        this.dispose(entry.id);
        this.publish({
          errors: { ...this.state.errors, [entry.id]: "Activation timed out" },
        });
      }
    }, 10000);
    try {
      const bytes = await assetBytes(entry.ui.entry);
      if (!globalThis.crypto?.subtle)
        throw new Error("JavaScript extensions require HTTPS or localhost");
      const hash = await crypto.subtle.digest("SHA-256", bytes);
      const hex = [...new Uint8Array(hash)]
        .map((b) => b.toString(16).padStart(2, "0"))
        .join("");
      if (hex !== entry.entry_sha256)
        throw new Error("JavaScript digest mismatch");
      for (const path of entry.ui.styles ?? []) {
        const css = await assetText(path);
        if (!active()) return;
        const style = document.createElement("style");
        style.dataset.ext = entry.id;
        style.textContent = css;
        document.head.append(style);
        life.dispose.push(() => style.remove());
      }
      if (!active()) return;
      const url = URL.createObjectURL(
        new Blob([bytes], { type: "text/javascript" }),
      );
      life.dispose.push(() => URL.revokeObjectURL(url));
      // The module's top-level code has the origin's authority even if
      // activate throws. Environment switches must reload from this point.
      this.evaluated = true;
      requireEnvironmentReload();
      this.evaluatedDigests.set(entry.id, entry.sha256);
      const mod = await this.loadModule(url);
      if (!active()) return;
      if (typeof mod.activate !== "function")
        throw new Error("Module must export activate(coddy)");
      const cleanup = await mod.activate(api);
      if (!active()) {
        if (typeof cleanup === "function") cleanup();
        return;
      }
      if (typeof cleanup === "function")
        life.dispose.push(cleanup as () => void);
    } catch (error) {
      if (!active()) return;
      this.failed.set(entry.id, entry.sha256);
      this.dispose(entry.id);
      this.publish({
        errors: { ...this.state.errors, [entry.id]: String(error) },
      });
    } finally {
      window.clearTimeout(timer);
    }
  }
}
