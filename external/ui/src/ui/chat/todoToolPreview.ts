export type TodoPlanEntry = {
  content: string;
  status: string;
};

export type TodoToolPreview = {
  variant: "item" | "plan";
  header: string;
  meta: string[];
  entries: TodoPlanEntry[];
};

type TodoToolPreviewInput = {
  toolName: string;
  argsText?: string | undefined;
  planSnapshot?: readonly TodoPlanEntry[] | undefined;
};

const todoStatuses = new Set([
  "pending",
  "in_progress",
  "completed",
  "failed",
  "cancelled",
]);

function objectArgs(text: string | undefined): Record<string, unknown> | null {
  if (!text?.trim()) return null;
  try {
    const value = JSON.parse(text) as unknown;
    return value && typeof value === "object" && !Array.isArray(value)
      ? (value as Record<string, unknown>)
      : null;
  } catch {
    return null;
  }
}

/** Validates untyped HTTP/SSE data before it enters the transcript state. */
export function normalizeTodoPlanSnapshot(
  value: unknown,
): TodoPlanEntry[] | undefined {
  if (!Array.isArray(value)) return undefined;
  const entries: TodoPlanEntry[] = [];
  for (const item of value) {
    if (!item || typeof item !== "object" || Array.isArray(item)) {
      return undefined;
    }
    const row = item as Record<string, unknown>;
    const content = typeof row.content === "string" ? row.content.trim() : "";
    const status = typeof row.status === "string" ? row.status.trim() : "";
    if (!content || !todoStatuses.has(status)) return undefined;
    entries.push({ content, status });
  }
  return entries;
}

export function isTodoPreviewTool(toolName: string): boolean {
  const name = toolName.trim().toLowerCase();
  return (
    name === "coddy_todo_item_update" ||
    name === "coddy_todo_plan_replace"
  );
}

/** Builds a stable timeline preview from the plan snapshot saved with a todo tool call. */
export function buildTodoToolPreview(
  input: TodoToolPreviewInput,
): TodoToolPreview | null {
  const toolName = input.toolName.trim().toLowerCase();
  const entries = normalizeTodoPlanSnapshot(input.planSnapshot);
  if (!entries || entries.length === 0) return null;

  if (toolName === "coddy_todo_item_update") {
    const index = objectArgs(input.argsText)?.index;
    if (
      typeof index !== "number" ||
      !Number.isInteger(index) ||
      index < 0 ||
      index >= entries.length
    ) {
      return null;
    }
    return {
      variant: "item",
      header: "Updated item",
      meta: [`${index + 1} of ${entries.length}`],
      entries: [entries[index]!],
    };
  }

  if (toolName === "coddy_todo_plan_replace") {
    const completed = entries.filter(
      (entry) => entry.status === "completed",
    ).length;
    return {
      variant: "plan",
      header: "Todo plan",
      meta: [`${completed} completed`, `${entries.length} items`],
      entries,
    };
  }

  return null;
}
