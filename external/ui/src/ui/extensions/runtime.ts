import * as React from "react";
import { requireEnvironmentReload } from "../env/remoteEnv";
import { extensionNetwork, type ExtensionNetwork } from "./network";
import {
  Contributions,
  validateField,
  validateRenderer,
  validateSection,
  type AnySettingsField,
  type SettingsSection,
  type ToolRenderer,
  type Owned,
  type ContributionHandle,
} from "./contributions";
import { ExtensionEvents, type ExtensionEventAPI } from "./events";
import { extensionSettings, type ExtensionSettingsAPI } from "./settings";
import { ExtensionThemes, type ExtensionThemeAPI } from "./theme";
import { knownSectionLabel } from "../settings/settingsSections";
import {
  IncomingStream,
  type IncomingRegistration,
  type IncomingHandle,
} from "./incomingStream";
import {
  FrameObserver,
  type FrameHandle,
  type FrameListener,
} from "./frameObserver";
import {
  validateMessageRenderer,
  type MessageRenderer,
} from "./messageRenderers";
import {
  validatePermissionPreview,
  type PermissionPreview,
} from "./permissionPreviews";
import { validatePanel, type Panel, type RuntimePanel } from "./panels";
import { AnchorRegistry, isExtensionAnchorName } from "./anchors";
import { validateSessionFilter, type SessionFilter } from "./sessionFilters";
import { ExtensionI18n, type ExtensionI18nAPI } from "./extensionI18n";
import { extensionHost, type ExtensionHostAPI } from "./hostChannel";
import { engineInfo } from "./compat";
import { readAppliedUiLocale } from "../i18n/uiLocale";

export type ExtensionEntry = {
  api: number;
  id: string;
  name: string;
  version: string;
  description?: string;
  sha256: string;
  entry_sha256: string;
  enabled: boolean;
  /** Manifest permissions this package declares. Enablement remains the gate;
   * the declaration is the vocabulary a later phase enforces. */
  permissions?: readonly string[];
  ui: { entry: string; styles?: string[]; compat_styles?: string[] };
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
  theme: ExtensionThemeAPI;
  settings: ExtensionSettingsAPI;
  i18n: ExtensionI18nAPI;
  host: ExtensionHostAPI;
  stream: {
    registerIncoming: (registration: IncomingRegistration) => IncomingHandle;
    /** Observes every frame of the live stream, including kinds the host does
     * not parse. Read-only: a listener cannot rewrite or stop a frame. */
    onFrame: (registration: {
      id: string;
      listener: FrameListener;
    }) => FrameHandle;
  };
  ui: {
    registerPage: (page: ExtensionPage) => Handle<ExtensionPage>;
    registerMenuItem: (item: MenuItem) => Handle<MenuItem>;
    openPage: (id: string) => void;
    registerToolRenderer: (
      renderer: ToolRenderer,
    ) => ContributionHandle<ToolRenderer>;
    /** Draws transcript rows: matched host item types and `ext_frame` rows of
     * live frames the host has no row for. */
    registerMessageRenderer: (
      renderer: MessageRenderer,
    ) => ContributionHandle<MessageRenderer>;
    /** Decorates the host's permission card; the prompt stays host-controlled. */
    registerPermissionPreview: (
      preview: PermissionPreview,
    ) => ContributionHandle<PermissionPreview>;
    registerPanel: (panel: Panel) => Handle<Panel>;
    openPanel: (id: string) => void;
    registerSessionFilter: (
      filter: SessionFilter,
    ) => ContributionHandle<SessionFilter>;
    /** A named anchor box of the host surface, or null while its surface is
     * closed. `onAnchor` covers anchors that mount later. */
    anchor: (name: string) => HTMLElement | null;
    onAnchor: (
      name: string,
      listener: (element: HTMLElement | null) => void,
    ) => { remove: () => void };
    /** Compatibility stylesheet layered over the host's on a legacy engine
     * (Chromium 110 and below, the JCEF panels of older IDEs). */
    addCompatStyles: (css: string) => { remove: () => void };
    engine: () => ReturnType<typeof engineInfo>;
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
  panels: RuntimePanel[];
  fields: Owned<AnySettingsField>[];
  sections: Owned<SettingsSection>[];
  renderers: Owned<ToolRenderer>[];
  messageRenderers: Owned<MessageRenderer>[];
  permissionPreviews: Owned<PermissionPreview>[];
  sessionFilters: Owned<SessionFilter>[];
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
    panels: [],
    fields: [],
    sections: [],
    renderers: [],
    messageRenderers: [],
    permissionPreviews: [],
    sessionFilters: [],
    errors: {},
  };
  readonly fields = new Contributions<AnySettingsField>(() =>
    this.publish({ fields: this.fields.rows }),
  );
  readonly sections = new Contributions<SettingsSection>(() =>
    this.publish({ sections: this.sections.rows }),
  );
  readonly renderers = new Contributions<ToolRenderer>(() =>
    this.publish({ renderers: this.renderers.rows }),
  );
  readonly messageRenderers = new Contributions<MessageRenderer>(() =>
    this.publish({ messageRenderers: this.messageRenderers.rows }),
  );
  readonly permissionPreviews = new Contributions<PermissionPreview>(() =>
    this.publish({ permissionPreviews: this.permissionPreviews.rows }),
  );
  readonly sessionFilters = new Contributions<SessionFilter>(() =>
    this.publish({ sessionFilters: this.sessionFilters.rows }),
  );
  readonly events = new ExtensionEvents((owner, message) =>
    this.reportError(owner, message),
  );
  readonly themes = new ExtensionThemes((owner, message) =>
    this.reportError(owner, message),
  );
  readonly frames = new FrameObserver((owner, message) =>
    this.reportError(owner, message),
  );
  readonly anchors = new AnchorRegistry();
  readonly i18n = new ExtensionI18n(
    () => readAppliedUiLocale(),
    (owner, message) => this.reportError(owner, message),
  );
  reportError(owner: string, message: string) {
    this.publish({ errors: { ...this.state.errors, [owner]: message } });
  }
  failRenderer(key: string, owner: string, error: unknown) {
    this.renderers.remove(key);
    this.reportError(owner, String(error));
  }
  failMessageRenderer(key: string, owner: string, error: unknown) {
    this.messageRenderers.remove(key);
    this.reportError(owner, String(error));
  }
  failPermissionPreview(key: string, owner: string, error: unknown) {
    this.permissionPreviews.remove(key);
    this.reportError(owner, String(error));
  }
  private evaluated = false;
  private evaluatedDigests = new Map<string, string>();
  private failed = new Map<string, string>();
  private compatStyles = new Map<string, HTMLStyleElement[]>();
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
    private openPanel: (panel: RuntimePanel) => void = () => {},
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
    for (const style of this.compatStyles.get(id) ?? []) style.remove();
    this.compatStyles.delete(id);
    this.publish({
      pages: this.state.pages.filter((p) => p.owner !== id),
      menu: this.state.menu.filter((p) => p.owner !== id),
      panels: this.state.panels.filter((p) => p.owner !== id),
    });
  }
  stop() {
    for (const id of this.lifetimes.keys()) this.dispose(id);
  }
  /** Reconciles what is installed with what is running. retryFailed asks again
   * for the ones a previous attempt failed, which only an explicit user action
   * does - the list poll leaves a failed digest alone instead of hammering it. */
  async reconcile(entries: ExtensionEntry[], retryFailed = false) {
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
    if (retryFailed) {
      // A retry re-reads what never ran, but never what did: a module's
      // top-level code has the origin's authority and its side effects happen
      // once, so undoing them takes a reload rather than a second import.
      for (const id of [...this.failed.keys()]) {
        if (!this.evaluatedDigests.has(id)) this.failed.delete(id);
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
    // A new attempt replaces the report of the previous one as it starts, so
    // one that succeeds leaves no stale failure on the card. Nothing else has
    // reported for this owner yet: the extension has no registration alive.
    if (this.state.errors[entry.id]) {
      const errors = { ...this.state.errors };
      delete errors[entry.id];
      this.publish({ errors });
    }
    const controller = new AbortController();
    const life = {
      digest: entry.sha256,
      controller,
      dispose: [] as (() => void)[],
    };
    this.lifetimes.set(entry.id, life);
    const active = () =>
      !controller.signal.aborted && this.lifetimes.get(entry.id) === life;
    // A module import or activate call that never settles must not hold
    // reconcile's Promise.all - and the stream-readiness gate behind it - open
    // forever: the activation timeout aborts the lifetime, so these awaits
    // follow it instead of the misbehaving module.
    const aborting = () =>
      new Promise<never>((_, reject) => {
        const fail = () =>
          reject(new DOMException("Extension disposed", "AbortError"));
        if (controller.signal.aborted) fail();
        else controller.signal.addEventListener("abort", fail, { once: true });
      });
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
    const register = <T extends ExtensionPage | MenuItem | Panel>(
      kind: "pages" | "menu" | "panels",
      value: T,
      valid: (row: T) => void,
    ): Handle<T> => {
      if (!active()) throw new Error("Extension disposed");
      valid(value);
      const key = `${entry.id}/${value.id}`;
      if (this.state[kind].some((row) => row.key === key))
        throw new Error(`Duplicate control: ${key}`);
      const row = { ...value, key, owner: entry.id, api };
      if (kind === "pages")
        this.publish({ pages: [...this.state.pages, row as RuntimePage] });
      else if (kind === "menu")
        this.publish({ menu: [...this.state.menu, row as RuntimeMenu] });
      else
        this.publish({ panels: [...this.state.panels, row as RuntimePanel] });
      let removed = false;
      return {
        update: (patch) => {
          if (!active() || removed) return;
          const current = this.state[kind].find((r) => r.key === key) as
            | (T & { key: string; owner: string; api: ExtensionAPI })
            | undefined;
          if (!current) return;
          const next = {
            ...current,
            ...patch,
            id: value.id,
            key,
            owner: entry.id,
            api,
          };
          valid(next as unknown as T);
          this.publish({
            [kind]: this.state[kind].map((r) => (r.key === key ? next : r)),
          } as Partial<RuntimeSnapshot>);
        },
        remove: () => {
          if (!active() || removed) return;
          removed = true;
          this.publish({
            [kind]: this.state[kind].filter((r) => r.key !== key),
          } as Partial<RuntimeSnapshot>);
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
      theme: this.themes.api(entry.id, controller.signal),
      settings: extensionSettings(
        entry.id,
        controller.signal,
        (field) => {
          if (
            !knownSectionLabel(field.section) &&
            !this.sections.rows.some(
              (row) => row.owner === entry.id && row.id === field.section,
            )
          )
            throw new Error("Unknown settings section");
          if (
            this.fields.rows.filter((row) => row.owner === entry.id).length >=
            128
          )
            throw new Error("Too many extension fields");
          return this.fields.register(entry.id, api, field, (value) => {
            validateField(value);
            if (
              !knownSectionLabel(value.section) &&
              !this.sections.rows.some(
                (row) => row.owner === entry.id && row.id === value.section,
              )
            )
              throw new Error("Unknown settings section");
          });
        },
        (section) =>
          this.sections.register(entry.id, api, section, validateSection),
        () => this.fields.rows.filter((row) => row.owner === entry.id),
      ),
      i18n: this.i18n.api(entry.id, controller.signal),
      host: extensionHost(
        entry.id,
        entry.sha256,
        controller.signal,
        (type, data) => this.events.emitCustom(entry.id, type, data),
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
        onFrame: ({ id, listener }) => {
          if (!active()) throw new Error("Extension disposed");
          return this.frames.register(
            entry.id,
            id,
            controller.signal,
            listener,
          );
        },
      },
      ui: {
        registerToolRenderer: (renderer) =>
          this.renderers.register(entry.id, api, renderer, validateRenderer),
        registerMessageRenderer: (renderer) => {
          if (!active()) throw new Error("Extension disposed");
          return this.messageRenderers.register(
            entry.id,
            api,
            renderer,
            validateMessageRenderer,
          );
        },
        registerPermissionPreview: (preview) => {
          if (!active()) throw new Error("Extension disposed");
          return this.permissionPreviews.register(
            entry.id,
            api,
            preview,
            validatePermissionPreview,
          );
        },
        registerSessionFilter: (filter) => {
          if (!active()) throw new Error("Extension disposed");
          return this.sessionFilters.register(
            entry.id,
            api,
            filter,
            validateSessionFilter,
          );
        },
        registerPage: (page) =>
          register("pages", page, (row) => {
            if (
              !/^[a-z][a-z0-9-]{0,63}$/.test(row.id) ||
              !row.title?.trim() ||
              typeof row.mount !== "function"
            )
              throw new Error("A page needs an id, title and mount function");
          }),
        registerMenuItem: (item) =>
          register("menu", item, (row) => {
            if (
              !/^[a-z][a-z0-9-]{0,63}$/.test(row.id) ||
              !row.title?.trim() ||
              typeof row.onSelect !== "function"
            )
              throw new Error("A menu item needs an id, title and onSelect");
          }),
        registerPanel: (panel) =>
          register("panels", panel, (row) =>
            validatePanel(row as unknown as Panel),
          ),
        openPage: (id) => {
          if (!active()) return;
          const page = this.state.pages.find(
            (p) => p.key === `${entry.id}/${id}`,
          );
          if (page) this.open(page);
        },
        openPanel: (id) => {
          if (!active()) return;
          const panel = this.state.panels.find(
            (p) => p.key === `${entry.id}/${id}`,
          );
          if (panel) this.openPanel(panel);
        },
        anchor: (name) => {
          if (!active()) return null;
          return isExtensionAnchorName(name) ? this.anchors.get(name) : null;
        },
        onAnchor: (name, listener) => {
          if (!active()) throw new Error("Extension disposed");
          return this.anchors.on(name, controller.signal, listener);
        },
        addCompatStyles: (css) => {
          if (!active()) throw new Error("Extension disposed");
          if (typeof css !== "string" || !css.trim())
            throw new Error("Compat styles must be a stylesheet text");
          const remove = () => {
            const list = this.compatStyles.get(entry.id) ?? [];
            for (const style of list) {
              if (style.textContent === css) {
                style.remove();
                this.compatStyles.set(
                  entry.id,
                  list.filter((s) => s !== style),
                );
                break;
              }
            }
            controller.signal.removeEventListener("abort", remove);
          };
          if (engineInfo().tier === "legacy") {
            const style = document.createElement("style");
            style.dataset.extCompat = entry.id;
            style.textContent = css;
            document.head.append(style);
            this.compatStyles.set(entry.id, [
              ...(this.compatStyles.get(entry.id) ?? []),
              style,
            ]);
          }
          controller.signal.addEventListener("abort", remove, { once: true });
          return { remove };
        },
        engine: () => engineInfo(),
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
      // Compat styles are the overlay of a legacy engine only: on a modern
      // one the ordinary stylesheet already draws what they would correct.
      if (engineInfo().tier === "legacy") {
        for (const path of entry.ui.compat_styles ?? []) {
          const css = await assetText(path);
          if (!active()) return;
          const style = document.createElement("style");
          style.dataset.extCompat = entry.id;
          style.textContent = css;
          document.head.append(style);
          this.compatStyles.set(entry.id, [
            ...(this.compatStyles.get(entry.id) ?? []),
            style,
          ]);
        }
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
      const mod = await Promise.race([this.loadModule(url), aborting()]);
      if (!active()) return;
      if (typeof mod.activate !== "function")
        throw new Error("Module must export activate(coddy)");
      const cleanup = await Promise.race([
        Promise.resolve(mod.activate(api)),
        aborting(),
      ]);
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
