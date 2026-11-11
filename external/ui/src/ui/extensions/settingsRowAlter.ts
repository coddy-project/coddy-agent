import { createContext, useSyncExternalStore } from "react";

import type { JsonSchema } from "../settings/SchemaForm";
import type { Owned } from "./contributions";
import { useExtensions } from "./ExtensionsProvider";

/** One schema-driven Settings field as an alter pass sees it. */
export type SettingsRowDescriptor = Readonly<{
  /** The Settings tab, not the configuration key when those differ. */
  section: string;
  /** Stable id of the row: its dotted path inside the section's form. */
  id: string;
  /** The same dotted path, named explicitly for passes that prefer it. */
  path: string;
  label: string;
  /** A block draws a fieldset of its own; a field draws a scalar control. */
  kind: "field" | "block";
  /** The JSON Schema type ("string", "object", "array", ...). */
  type: string;
  description?: string | undefined;
  /** False hides the row; omitting the descriptor hides it too. */
  visible?: boolean | undefined;
}>;

export type SettingsRowAlterContext = Readonly<{ section: string }>;
export type SettingsRowAlterFn = (
  rows: readonly SettingsRowDescriptor[],
  context: SettingsRowAlterContext,
) => readonly SettingsRowDescriptor[];
export type SettingsRowAlter = {
  id: string;
  run: SettingsRowAlterFn;
};

/** The texts a custom Settings control draws in the field's ordinary box. */
export type SettingsRowTextResolver = (
  domain: string,
  path: string,
  parent: Record<string, unknown> | undefined,
  schema: JsonSchema,
) =>
  | false
  | null
  | {
      primary: string;
      primaryDesc?: string | undefined;
      second?: string | undefined;
      secondDesc?: string | undefined;
    };

/** Supplied by Settings so SchemaForm and its search index run the same passes. */
export const SettingsRowAlterContext = createContext<{
  alters: readonly SettingsRowAlter[];
  texts?: SettingsRowTextResolver | undefined;
}>({ alters: [] });

function frozenRows(
  rows: readonly SettingsRowDescriptor[],
): readonly SettingsRowDescriptor[] {
  return Object.freeze(rows.map((row) => Object.freeze({ ...row })));
}

/**
 * Runs row passes in registration order. Every pass sees frozen descriptors;
 * omission or `visible: false` hides, `label` retitles and array order reorders.
 * A throw discards only that pass's answer, leaving earlier passes intact.
 */
export function alterSettingsRows(
  rows: readonly SettingsRowDescriptor[],
  alters: readonly SettingsRowAlter[],
  section: string,
): readonly SettingsRowDescriptor[] {
  let current = frozenRows(rows);
  const context = Object.freeze({ section });
  for (const pass of alters) {
    try {
      const next = pass.run(current, context);
      if (Array.isArray(next)) current = frozenRows(next);
    } catch {
      // A broken pass keeps the list produced by the passes before it.
    }
  }
  return current;
}

/** The Settings tab id owning the schema form's translation domain. */
export function settingsSectionForDomain(domain: string): string {
  if (domain.startsWith("system.")) return "system";
  if (domain === "sessions") return "sessions_manager";
  return domain;
}

const emptySubscribe = () => () => {};
const emptyAlters: Owned<SettingsRowAlter>[] = [];

/** The registered row alter passes, live in registration order. */
export function useSettingsRowAlters(): Owned<SettingsRowAlter>[] {
  const runtime = useExtensions()?.runtime;
  return useSyncExternalStore(
    runtime ? runtime.subscribe : emptySubscribe,
    runtime ? () => runtime.settingsRowAlters.rows : () => emptyAlters,
    () => emptyAlters,
  );
}
