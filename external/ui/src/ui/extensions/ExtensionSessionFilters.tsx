import { useEffect, useRef, useSyncExternalStore } from "react";
import { useExtensions } from "./ExtensionsProvider";
import type { SessionFilterInfo, SessionFilter } from "./sessionFilters";
import type { Owned, ToolView } from "./contributions";
import type { SessionRow } from "../sessions/types";

const emptySubscribe = () => () => {};
const emptyFilters: Owned<SessionFilter>[] = [];

/** The registered session filters, live: a late registration re-renders. */
export function useExtensionSessionFilters(): Owned<SessionFilter>[] {
  const runtime = useExtensions()?.runtime;
  return useSyncExternalStore(
    runtime ? runtime.subscribe : emptySubscribe,
    runtime ? () => runtime.snapshot().sessionFilters : () => emptyFilters,
    () => emptyFilters,
  );
}

/** The row metadata an extension filter sees, frozen like every SDK payload. */
export function sessionFilterInfo(row: SessionRow): SessionFilterInfo {
  return Object.freeze({
    id: row.id,
    title: row.title ?? "",
    cwd: row.cwd ?? "",
    repoRoot: row.repoRoot ?? "",
    archived: row.archived === true,
    pinned: row.pinned === true,
    origin: row.origin ?? "",
    tags: Object.freeze([...(row.tags ?? [])]),
    updatedAt: row.updatedAt ?? "",
  });
}

/** Keeps the rows every registered extension filter passes. */
export function applyExtensionSessionFilters(
  rows: SessionRow[],
  filters: readonly Owned<SessionFilter>[],
): SessionRow[] {
  const predicates = filters.filter((f) => typeof f.filter === "function");
  if (!predicates.length) return rows;
  return rows.filter((row) => {
    const info = sessionFilterInfo(row);
    return predicates.every((f) => {
      try {
        return f.filter!(info) !== false;
      } catch {
        // A filter that throws loses nothing but its own say: the row stays.
        return true;
      }
    });
  });
}

/** The mounted chip controls of every registered session filter. */
export function ExtensionSessionFilterBar() {
  const context = useExtensions();
  return context ? <FilterBar runtime={context.runtime} /> : null;
}

function FilterBar({
  runtime,
}: {
  runtime: NonNullable<ReturnType<typeof useExtensions>>["runtime"];
}) {
  const { sessionFilters } = useSyncExternalStore(
    runtime.subscribe,
    runtime.snapshot,
  );
  const withMount = sessionFilters.filter((f) => typeof f.mount === "function");
  if (!withMount.length) return null;
  return (
    <div className="extension-session-filters">
      {withMount.map((filter) => (
        <MountedChip
          key={filter.key}
          filter={filter}
          fail={(error) =>
            runtime.failMessageRenderer(filter.key, filter.owner, error)
          }
        />
      ))}
    </div>
  );
}

function MountedChip({
  filter,
  fail,
}: {
  filter: Owned<SessionFilter>;
  fail: (error: unknown) => void;
}) {
  const element = useRef<HTMLSpanElement>(null);
  const cleanup = useRef<() => void>(() => {});
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
      const result = filter.mount!(el, filter.api);
      if (typeof result === "function") cleanup.current = result;
      else if (result && typeof result === "object" && !("then" in result))
        cleanup.current = () => (result as ToolView).dispose?.();
      else if (result !== undefined) {
        Promise.resolve(result).catch(() => {});
        throw new Error("Session filter mount must be synchronous");
      }
    } catch (error) {
      fail(error);
    }
    return dispose;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [filter.key, filter.mount, filter.api]);
  return (
    <span
      ref={element}
      className="extension-session-filter"
      data-extension-renderer={filter.key}
    />
  );
}
