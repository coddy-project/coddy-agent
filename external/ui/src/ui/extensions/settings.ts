import type {
  AnySettingsField,
  SettingsSection,
  SettingValue,
  ContributionHandle,
} from "./contributions";
import {
  noteSettingsConfigReloaded,
  noteSettingsConfigSaved,
} from "../settings/settingsConfigStore";

export type ExtensionSettingsAPI = {
  registerField: (
    field: AnySettingsField,
  ) => ContributionHandle<AnySettingsField>;
  /** A section of the extension's own. A field of the same extension may name
   * it in `section`; fields of other extensions may not. */
  registerSection: (
    section: SettingsSection,
  ) => ContributionHandle<SettingsSection>;
  get: (id: string) => Promise<SettingValue | undefined>;
  set: (id: string, value: SettingValue | null) => Promise<void>;
};
export function object(value: unknown): Record<string, unknown> {
  return value && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
}
export function ownerValues(doc: Record<string, unknown>, owner: string) {
  return object(object(doc.extension_values)[owner]);
}
export function setOwnerValues(
  doc: Record<string, unknown>,
  owner: string,
  values: Record<string, unknown>,
) {
  return {
    ...doc,
    extension_values: { ...object(doc.extension_values), [owner]: values },
  };
}

export function extensionSettings(
  owner: string,
  signal: AbortSignal,
  registerField: ExtensionSettingsAPI["registerField"],
  registerSection: ExtensionSettingsAPI["registerSection"],
  fields: () => AnySettingsField[],
): ExtensionSettingsAPI {
  let pending = Promise.resolve();
  const active = () => signal.throwIfAborted();
  const read = async () => {
    active();
    const res = await fetch("/coddy/config", { signal });
    if (!res.ok) throw new Error(`Extension settings: HTTP ${res.status}`);
    const doc = (await res.json()) as Record<string, unknown>;
    active();
    return doc;
  };
  const field = (id: string) => fields().find((field) => field.id === id);
  return {
    registerField,
    registerSection,
    async get(id) {
      const doc = await read();
      const values = ownerValues(doc, owner);
      return (Object.hasOwn(values, id) ? values[id] : field(id)?.default) as
        | SettingValue
        | undefined;
    },
    set(id, value) {
      const work = pending.then(async () => {
        active();
        if (!/^[a-z][a-z0-9-]{0,63}$/.test(id))
          throw new Error("Invalid settings key");
        const definition = field(id);
        if (
          value !== null &&
          (!["string", "number", "boolean"].includes(typeof value) ||
            (typeof value === "number" && !Number.isFinite(value)) ||
            (definition &&
              definition.type !== "component" &&
              (typeof value !== definition.type ||
                (definition.enum && !definition.enum.includes(String(value))))))
        )
          throw new Error("Invalid extension setting value");
        const doc = await read();
        const values = { ...ownerValues(doc, owner) };
        if (value === null) delete values[id];
        else values[id] = value;
        active();
        const next = setOwnerValues(doc, owner, values);
        const response = await fetch("/coddy/config", {
          method: "PUT",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify(next),
          signal,
        });
        if (!response.ok)
          throw new Error(`Extension settings: HTTP ${response.status}`);
        active();
        noteSettingsConfigSaved(next);
        noteSettingsConfigReloaded();
      });
      pending = work.catch(() => {});
      return work;
    },
  };
}
