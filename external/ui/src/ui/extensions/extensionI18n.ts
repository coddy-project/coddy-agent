/**
 * Extension dictionaries. Keys are forced under `ext.<id>.`; a lookup falls
 * back from the active locale to `en` and then to the key itself, so a
 * missing translation degrades to readable text instead of a blank.
 */
export type ExtensionMessages = Readonly<Record<string, string>>;
export type ExtensionI18nAPI = {
  /** Active host locale tag (`en`, `ru`, ...). */
  locale: () => string;
  /** Looks up `ext.<id>.<key>` in the registered dictionaries. */
  t: (key: string, vars?: Readonly<Record<string, string | number>>) => string;
  /** Registers one locale's strings; keys without the `ext.<id>.` prefix are
   * prefixed. Re-registering a locale replaces the previous dictionary. */
  registerMessages: (
    locale: string,
    dict: Readonly<Record<string, string>>,
  ) => { remove: () => void };
  onChange: (listener: (locale: string) => void) => { remove: () => void };
};

type Dicts = Map<string, ExtensionMessages>;

const LOCALE_RE = /^[a-z]{2}(-[a-z0-9]{2,8})*$/i;

export class ExtensionI18n {
  private dicts = new Map<string, Dicts>();
  private listeners = new Map<
    string,
    Map<(locale: string) => void, AbortSignal>
  >();
  constructor(
    private readLocale: () => string,
    private onError: (owner: string, message: string) => void,
  ) {}

  /** Host locale changes call this; every owner's listeners hear the new one. */
  notifyLocale(locale: string) {
    for (const [owner, map] of [...this.listeners]) {
      for (const [listener, signal] of [...map]) {
        if (signal.aborted) {
          map.delete(listener);
          continue;
        }
        try {
          listener(locale);
        } catch (error) {
          map.delete(listener);
          this.onError(owner, `Locale listener: ${String(error)}`);
        }
      }
      if (!map.size) this.listeners.delete(owner);
    }
  }

  api(owner: string, signal: AbortSignal): ExtensionI18nAPI {
    const prefix = `ext.${owner}.`;
    const dictsOf = (): Dicts => {
      let dicts = this.dicts.get(owner);
      if (!dicts) this.dicts.set(owner, (dicts = new Map()));
      return dicts;
    };
    const t: ExtensionI18nAPI["t"] = (key, vars) => {
      const full = key.startsWith(prefix) ? key : prefix + key;
      const dicts = this.dicts.get(owner);
      const locale = this.readLocale();
      // Active locale, then English, then the key itself: a missing
      // translation degrades to readable text, never to a blank.
      const text =
        dicts?.get(locale)?.[full] ?? dicts?.get("en")?.[full] ?? full;
      if (!vars) return text;
      return text.replace(/\{(\w+)\}/g, (whole, name: string) =>
        name in vars ? String(vars[name]) : whole,
      );
    };
    return {
      locale: () => this.readLocale(),
      t,
      registerMessages: (locale, dict) => {
        if (signal.aborted) throw new Error("Extension disposed");
        if (!LOCALE_RE.test(locale) || !dict || typeof dict !== "object")
          throw new Error("Invalid locale or dictionary");
        const normalized: Record<string, string> = {};
        for (const [key, value] of Object.entries(dict)) {
          if (typeof value !== "string")
            throw new Error("Dictionary values must be strings");
          normalized[key.startsWith(prefix) ? key : prefix + key] = value;
        }
        const dicts = dictsOf();
        dicts.set(locale, Object.freeze(normalized));
        let removed = false;
        const remove = () => {
          if (removed || signal.aborted) return;
          removed = true;
          if (dicts.get(locale) === normalized) dicts.delete(locale);
          signal.removeEventListener("abort", remove);
        };
        signal.addEventListener("abort", remove, { once: true });
        return { remove };
      },
      onChange: (listener) => {
        if (signal.aborted) throw new Error("Extension disposed");
        if (typeof listener !== "function")
          throw new Error("A listener is required");
        const key = owner;
        const remove = () => {
          const map = this.listeners.get(key);
          if (!map?.has(listener)) return;
          map.delete(listener);
          if (!map.size) this.listeners.delete(key);
          signal.removeEventListener("abort", remove);
        };
        let map = this.listeners.get(key);
        if (!map) this.listeners.set(key, (map = new Map()));
        map.set(listener, signal);
        signal.addEventListener("abort", remove, { once: true });
        return { remove };
      },
    };
  }
}
