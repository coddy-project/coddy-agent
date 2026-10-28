import type { ExtensionAPI } from "./runtime";

export type SettingValue = string | number | boolean;
export type SettingsField = {
  id: string;
  section: string;
  title: string;
  description?: string;
  type: "string" | "number" | "boolean";
  default?: SettingValue;
  enum?: string[];
};
export type ExtensionToolCall = Readonly<{
  sessionId: string;
  toolCallId: string;
  name: string;
  server: string;
  tool: string;
  status: string;
  argsText: string;
  resultText: string;
  resultWasTruncated: boolean;
  permissionWaiting: boolean;
  durationMs: number | null;
  startedAtMs: number | null;
}>;
export type ToolView = {
  update?: (call: ExtensionToolCall) => void;
  dispose?: () => void;
};
export type ToolRenderer = {
  id: string;
  server?: string;
  tool?: string;
  mount: (
    element: HTMLElement,
    call: ExtensionToolCall,
    api: ExtensionAPI,
  ) => void | (() => void) | ToolView;
};
export type Owned<T> = T & { key: string; owner: string; api: ExtensionAPI };
export type ContributionHandle<T> = {
  update: (patch: Partial<Omit<T, "id">>) => void;
  remove: () => void;
};

export class Contributions<T extends { id: string }> {
  rows: Owned<T>[] = [];
  constructor(private notify: () => void) {}
  register(
    owner: string,
    api: ExtensionAPI,
    value: T,
    valid: (value: T) => void,
  ): ContributionHandle<T> {
    const check = () => {
      if (api.signal.aborted) throw new Error("Extension disposed");
    };
    check();
    valid(value);
    if (!/^[a-z][a-z0-9-]{0,63}$/.test(value.id))
      throw new Error("Invalid contribution id");
    const key = `${owner}/${value.id}`;
    if (this.rows.some((row) => row.key === key))
      throw new Error(`Duplicate contribution: ${key}`);
    this.rows = [...this.rows, { ...value, key, owner, api }];
    this.notify();
    const remove = () => {
      this.rows = this.rows.filter((row) => row.key !== key);
      api.signal.removeEventListener("abort", remove);
      this.notify();
    };
    api.signal.addEventListener("abort", remove, { once: true });
    return {
      remove,
      update: (patch) => {
        check();
        const row = this.rows.find((row) => row.key === key);
        if (!row) return;
        const next = { ...row, ...patch, id: row.id, key, owner, api };
        valid(next);
        this.rows = this.rows.map((row) => (row.key === key ? next : row));
        this.notify();
      },
    };
  }
  remove(key: string) {
    this.rows = this.rows.filter((row) => row.key !== key);
    this.notify();
  }
}

export function validateField(field: SettingsField) {
  if (
    !field.title?.trim() ||
    field.title.length > 120 ||
    !field.section ||
    !["string", "number", "boolean"].includes(field.type)
  )
    throw new Error("A settings field needs a section, title and scalar type");
  if (
    field.default !== undefined &&
    (typeof field.default !== field.type ||
      (typeof field.default === "number" && !Number.isFinite(field.default)))
  )
    throw new Error("Invalid settings default");
  if (
    field.enum &&
    (field.type !== "string" ||
      !field.enum.length ||
      field.enum.some((value) => typeof value !== "string") ||
      (field.default !== undefined &&
        !field.enum.includes(String(field.default))))
  )
    throw new Error("Invalid field choices");
}
export function validateRenderer(renderer: ToolRenderer) {
  if (
    typeof renderer.mount !== "function" ||
    (renderer.server !== undefined && !renderer.server) ||
    (renderer.tool !== undefined && !renderer.tool)
  )
    throw new Error("Invalid tool renderer");
}
export function selectRenderer(
  rows: Owned<ToolRenderer>[],
  server: string,
  tool: string,
) {
  return rows
    .filter(
      (row) =>
        (!row.server || row.server === "*" || row.server === server) &&
        (!row.tool || row.tool === "*" || row.tool === tool),
    )
    .sort((a, b) => {
      const score = (row: ToolRenderer) =>
        Number(!!row.server && row.server !== "*") +
        Number(!!row.tool && row.tool !== "*");
      return (
        score(b) - score(a) ||
        (a.owner < b.owner ? -1 : a.owner > b.owner ? 1 : 0)
      );
    })[0];
}
