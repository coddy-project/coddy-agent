import {
  createContext,
  useContext,
  useEffect,
  useRef,
  useSyncExternalStore,
  type ReactNode,
} from "react";

import type {
  Owned,
  SettingsBlock,
  SettingsBlockContext,
} from "../extensions/contributions";
import { useExtensions } from "../extensions/ExtensionsProvider";
import { translate } from "../i18n/i18n";
import {
  alterSettingsRows,
  SettingsRowAlterContext,
  type SettingsRowAlter,
  type SettingsRowDescriptor,
} from "../extensions/settingsRowAlter";
import { LegendWithHint } from "./FieldHint";
import {
  SETTINGS_TEXTS,
  landmarkAnchor,
  type DrawnText,
} from "./settingsSections";
import { SETTINGS_LANDMARKS, type SettingsLandmark } from "./settingsLandmarks";

export type { SettingsBlock, SettingsBlockContext };

type PlainObject = Record<string, unknown>;

const SECRET_ROW_FIELDS = new Set([
  "api_key",
  "api_key_command",
  "token",
  "secret",
  "password",
  "env",
]);

/** A copy of a row that cannot disclose a credential to match or mount. */
function sanitizedValue(value: unknown): unknown {
  if (Array.isArray(value)) {
    return Object.freeze(value.map(sanitizedValue));
  }
  if (value !== null && typeof value === "object") {
    const copy: PlainObject = {};
    for (const [key, child] of Object.entries(value)) {
      if (!SECRET_ROW_FIELDS.has(key)) copy[key] = sanitizedValue(child);
    }
    return Object.freeze(copy);
  }
  return value;
}

export function sanitizedSettingsRow(row: unknown): PlainObject | undefined {
  if (row === null || typeof row !== "object" || Array.isArray(row)) {
    return undefined;
  }
  return sanitizedValue(row) as PlainObject;
}

function text(value: unknown): string {
  return value === undefined || value === null ? "" : String(value);
}

function rowFor(
  section: string,
  doc: PlainObject,
  rowName: string | null,
): PlainObject | undefined {
  if (rowName === null) return undefined;
  const rows = doc[section];
  if (!Array.isArray(rows)) return undefined;
  const labelField =
    section === "models" ? "model" : section === "toolsets" ? "name" : "name";
  const row = rows.find(
    (candidate) =>
      candidate !== null &&
      typeof candidate === "object" &&
      !Array.isArray(candidate) &&
      text((candidate as PlainObject)[labelField]).trim() === rowName,
  );
  return sanitizedSettingsRow(row);
}

export function settingsBlockContext(
  section: string,
  doc: PlainObject,
  rowName: string | null = null,
): SettingsBlockContext {
  const row = rowFor(section, doc, rowName);
  return Object.freeze({
    section,
    rowName,
    ...(row === undefined ? {} : { row }),
  });
}

export type SpecialRowSource =
  | { type: "landmark"; landmark: SettingsLandmark }
  | { type: "extension"; block: Owned<SettingsBlock> };

export type SpecialRowPlanItem = {
  descriptor: SettingsRowDescriptor;
  source: SpecialRowSource;
  context: SettingsBlockContext;
};

/** The descriptors and source mapping shared by drawing and search. */
export function buildSpecialRowPlan(input: {
  section: string;
  doc: PlainObject;
  rowName?: string | null;
  landmarks?: readonly SettingsLandmark[];
  blocks?: readonly Owned<SettingsBlock>[];
  alters?: readonly SettingsRowAlter[];
  includeExtensions?: boolean;
  onMatchError?:
    | ((block: Owned<SettingsBlock>, error: unknown) => void)
    | undefined;
}): SpecialRowPlanItem[] {
  const section = input.section;
  const rowName = input.rowName ?? null;
  const context = settingsBlockContext(section, input.doc, rowName);
  const raw: {
    descriptor: SettingsRowDescriptor;
    source: SpecialRowSource;
  }[] = [];
  const baseIds = new Set<string>();

  for (const landmark of input.landmarks ?? []) {
    if (landmark.section !== section || baseIds.has(landmark.id)) continue;
    try {
      if (landmark.shown !== undefined && !landmark.shown()) continue;
      if (
        rowName === null
          ? landmark.row !== undefined
          : !landmark.row?.(context.row ?? {})
      ) {
        continue;
      }
    } catch {
      continue;
    }
    const drawn: DrawnText = SETTINGS_TEXTS[landmark.heading];
    raw.push({
      descriptor: {
        section,
        id: landmark.id,
        path: landmark.id,
        label: drawn.label === undefined ? landmark.id : translate(drawn.label),
        ...(drawn.desc === undefined
          ? {}
          : { description: translate(drawn.desc) }),
        kind: "block",
        type: "landmark",
      },
      source: { type: "landmark", landmark },
    });
    baseIds.add(landmark.id);
  }

  if (input.includeExtensions !== false) {
    for (const block of input.blocks ?? []) {
      if (block.section !== section) continue;
      const id = `ext:${block.owner}/${block.id}`;
      if (baseIds.has(id)) continue;
      try {
        // An unmatched block belongs to the section itself. A predicate opts
        // into row forms and can narrow the row by its public metadata.
        if (block.match ? !block.match(context) : rowName !== null) continue;
      } catch (error) {
        input.onMatchError?.(block, error);
        continue;
      }
      raw.push({
        descriptor: {
          section,
          id,
          path: id,
          label: block.title,
          ...(block.description === undefined
            ? {}
            : { description: block.description }),
          kind: "block",
          type: "extension",
        },
        source: { type: "extension", block },
      });
      baseIds.add(id);
    }
  }

  const sources = new Map(raw.map((row) => [row.descriptor.id, row.source]));
  const altered = alterSettingsRows(
    raw.map((row) => row.descriptor),
    input.alters ?? [],
    section,
  );
  const seen = new Set<string>();
  const out: SpecialRowPlanItem[] = [];
  for (const descriptor of altered) {
    if (descriptor.visible === false || seen.has(descriptor.id)) continue;
    const source = sources.get(descriptor.id);
    if (source === undefined) continue;
    seen.add(descriptor.id);
    out.push({ descriptor, source, context });
  }
  return out;
}

const emptySubscribe = () => () => {};
const emptyBlocks: Owned<SettingsBlock>[] = [];

type StructuralBlockRuntime = {
  settingsBlocks?: { rows?: Owned<SettingsBlock>[] };
  reportError?: (owner: string, message: string) => void;
};

function blockRows(
  runtime: StructuralBlockRuntime | undefined,
): Owned<SettingsBlock>[] {
  const rows = runtime?.settingsBlocks?.rows;
  return Array.isArray(rows) ? rows : emptyBlocks;
}

/** Reads the contribution structurally while older hosts have no such slot. */
export function useSettingsBlocks(): Owned<SettingsBlock>[] {
  const context = useExtensions();
  const runtime = context?.runtime as unknown as
    | StructuralBlockRuntime
    | undefined;
  const subscribe = context?.runtime.subscribe ?? emptySubscribe;
  return useSyncExternalStore(
    runtime ? subscribe : emptySubscribe,
    () => blockRows(runtime),
    () => emptyBlocks,
  );
}

export function useSpecialRowPlan(input: {
  section: string;
  doc: PlainObject;
  rowName?: string | null;
  landmarks?: readonly SettingsLandmark[];
  blocks?: readonly Owned<SettingsBlock>[];
  includeExtensions?: boolean;
}): SpecialRowPlanItem[] {
  const registered = useSettingsBlocks();
  const { alters } = useContext(SettingsRowAlterContext);
  const runtime = useExtensions()?.runtime as unknown as
    | StructuralBlockRuntime
    | undefined;
  return buildSpecialRowPlan({
    ...input,
    blocks: input.blocks ?? registered,
    alters,
    onMatchError: (block, error) =>
      runtime?.reportError?.(block.owner, String(error)),
  });
}

const CurrentSpecialRow = createContext<SettingsRowDescriptor | null>(null);

/** A hand-drawn component reads its retitled heading without owning the plan. */
export function useSpecialRowLabel(id: string, fallback: string): string {
  const current = useContext(CurrentSpecialRow);
  return current?.id === id ? current.label : fallback;
}

export function SpecialRowsView(props: {
  plan: readonly SpecialRowPlanItem[];
  nodes: Readonly<Record<string, ReactNode>>;
}) {
  return (
    <>
      {props.plan.map((item) => {
        if (item.source.type === "extension") {
          return (
            <ExtensionBlockHost
              key={item.descriptor.id}
              item={item}
              block={item.source.block}
            />
          );
        }
        const node = props.nodes[item.descriptor.id];
        if (node === undefined || node === null || node === false) return null;
        return (
          <CurrentSpecialRow.Provider
            key={item.descriptor.id}
            value={item.descriptor}
          >
            {node}
          </CurrentSpecialRow.Provider>
        );
      })}
    </>
  );
}

/** Renders one before/after slot of hand-drawn host and extension blocks. */
export function SpecialRows(props: {
  section: string;
  doc: PlainObject;
  rowName?: string | null;
  nodes: Readonly<Record<string, ReactNode>>;
  landmarks?: readonly SettingsLandmark[];
  blocks?: readonly Owned<SettingsBlock>[];
  includeExtensions?: boolean;
}) {
  const landmarks =
    props.landmarks ??
    SETTINGS_LANDMARKS.filter(
      (landmark) =>
        landmark.section === props.section &&
        Object.hasOwn(props.nodes, landmark.id),
    );
  const plan = useSpecialRowPlan({
    section: props.section,
    doc: props.doc,
    rowName: props.rowName ?? null,
    landmarks,
    ...(props.blocks === undefined ? {} : { blocks: props.blocks }),
    ...(props.includeExtensions === undefined
      ? {}
      : { includeExtensions: props.includeExtensions }),
  });
  return <SpecialRowsView plan={plan} nodes={props.nodes} />;
}

/** The extension-owned body of a declarative Settings block. */
export function ExtensionBlockHost({
  item,
  block,
}: {
  item: SpecialRowPlanItem;
  block: Owned<SettingsBlock>;
}) {
  const element = useRef<HTMLDivElement>(null);
  const cleanup = useRef<() => void>(() => {});
  const runtime = useExtensions()?.runtime as unknown as
    | StructuralBlockRuntime
    | undefined;
  const fail = (error: unknown) =>
    runtime?.reportError?.(block.owner, String(error));
  const contextKey = JSON.stringify(item.context);
  useEffect(() => {
    const el = element.current;
    if (!el) return;
    const dispose = () => {
      try {
        Promise.resolve(cleanup.current()).catch(fail);
      } catch (error) {
        fail(error);
      }
      cleanup.current = () => {};
      el.replaceChildren();
    };
    try {
      const result = block.mount(el, block.api, item.context);
      if (typeof result === "function") cleanup.current = result;
      else if (result !== undefined && result !== null) {
        Promise.resolve(result).catch(() => {});
        throw new Error("Settings block mount must be synchronous");
      }
    } catch (error) {
      fail(error);
    }
    return dispose;
    // The row fingerprint remounts only when its public metadata really changed.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [block.key, block.mount, block.api, contextKey]);
  return (
    <fieldset
      className="settings-fieldset extension-settings-block"
      data-extension-settings-block={block.key}
      data-settings-anchor={landmarkAnchor(
        item.descriptor.section,
        item.descriptor.id,
      )}
    >
      <LegendWithHint
        label={item.descriptor.label}
        description={item.descriptor.description}
      />
      <div ref={element} className="extension-settings-block-view" />
    </fieldset>
  );
}
