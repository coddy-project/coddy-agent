import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useConfirm } from "../components/useConfirm";
import { useT } from "../i18n/I18nProvider";
import { isClientDraftSessionId } from "./draftSessions";
import {
  defaultSortOrder,
  type SessionArchiveFilter,
  type SessionSortKey,
  type SessionSortOrder,
} from "./sessionQuery";
import {
  allRowsSelected,
  formatRowTimestamp,
  formatRowTimestampFull,
  formatTokenCount,
  rowTotalTokens,
  selectionWithout,
  someRowsSelected,
  toggleAllRows,
  toggleSelected,
  workspaceBasename,
  type SessionManagerRow,
} from "./sessionManagerRows";

const PAGE_SIZE = 50;
const SEARCH_DEBOUNCE_MS = 250;

/**
 * The trash can. With a check inside its body it is the toolbar action that
 * removes the ticked rows; plain, it is the per-row delete. What the toolbar
 * button means in words lives in its tooltip, not on its face.
 */
function IconTrash(props: { mark?: "check" }) {
  return (
    <svg
      width="18"
      height="18"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.8"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
    >
      <path d="M3 6h18" />
      <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6" />
      <path d="M8 6V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
      {props.mark === "check" ? (
        <path d="M9 14.5l2 2 4-4" />
      ) : (
        <>
          <path d="M10 11v6" />
          <path d="M14 11v6" />
        </>
      )}
    </svg>
  );
}

/**
 * The archive box. It marks the action that empties the archive, which is a
 * different scope from the ticked rows and so needs a face of its own.
 */
function IconArchive() {
  return (
    <svg
      width="18"
      height="18"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.8"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
    >
      <path d="M3 7h18v3H3z" />
      <path d="M5 10v9a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2v-9" />
      <path d="M10 14h4" />
    </svg>
  );
}

/** Which way a sorted column points, drawn next to its name. */
function SortCaret(props: { order: SessionSortOrder }) {
  return (
    <span className="sessions-manager-sort-caret" aria-hidden>
      {props.order === "asc" ? "\u25b2" : "\u25bc"}
    </span>
  );
}

type ListResponse = {
  sessions?: SessionManagerRow[];
  nextCursor?: string | null;
  hasMore?: boolean;
};

type BulkDeleteResponse = {
  deleted?: string[];
  failed?: { id?: string; error?: string }[];
};

/**
 * SessionsManager is the Settings tab that treats the stored history as a
 * table: what every session cost, which backend answered it, and bulk removal
 * by tick, by "everything" or by "everything but the conversation that is
 * open". The History drawer stays the place to *open* a session; this is the
 * place to prune one.
 */
export function SessionsManager(props: {
  /** The conversation currently on screen, kept by "delete all except this one". */
  activeSessionId?: string;
  /** Reports the ids the server actually removed, so the shell can drop them. */
  onSessionsDeleted?: (ids: string[]) => void;
}) {
  const { t, tp } = useT();
  const confirm = useConfirm();
  const { onSessionsDeleted } = props;
  const [rows, setRows] = useState<SessionManagerRow[]>([]);
  const [selected, setSelected] = useState<ReadonlySet<string>>(new Set());
  const [searchDraft, setSearchDraft] = useState("");
  const [query, setQuery] = useState("");
  const [cursor, setCursor] = useState<string | null>(null);
  const [hasMore, setHasMore] = useState(false);
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [archiveFilter, setArchiveFilter] =
    useState<SessionArchiveFilter>("exclude");
  const [tagFilter, setTagFilter] = useState("");
  const [sortKey, setSortKey] = useState<SessionSortKey>("updated");
  const [sortOrder, setSortOrder] = useState<SessionSortOrder>("desc");
  // Every fetch carries a ticket; an answer that arrives after the search moved
  // on is dropped instead of overwriting the newer listing.
  const ticketRef = useRef(0);

  const activeId = String(props.activeSessionId ?? "").trim();
  // A client-only draft has no bundle on the server, so it is never a row here
  // and cannot be the session an "except" list protects.
  const activeStoredId =
    activeId && !isClientDraftSessionId(activeId) ? activeId : "";

  // The conversation open behind the panel is protected: it cannot be ticked,
  // the header checkbox passes over it, and its row has no trash. Everything
  // else is fair game, so ticking the header and pressing delete is how the
  // whole visible history goes - without ever taking the chat out from under
  // the operator.
  const selectableRows = useMemo(
    () => rows.filter((row) => row.id !== activeStoredId),
    [rows, activeStoredId],
  );

  const load = useCallback(
    async (reset: boolean, at: string | null) => {
      const ticket = ++ticketRef.current;
      setLoading(true);
      const ps = new URLSearchParams();
      ps.set("limit", String(PAGE_SIZE));
      ps.set("include_stats", "true");
      ps.set("include_activity", "true");
      ps.set("archived", archiveFilter);
      // Order and direction go to the server, not to the rendered page: paging
      // is an offset into the sorted listing, so sorting a page client side
      // would reorder 50 rows out of a history of hundreds.
      ps.set("sort", sortKey);
      ps.set("order", sortOrder);
      if (query) {
        ps.set("q", query);
      }
      if (tagFilter) {
        ps.set("tags", tagFilter);
      }
      if (!reset && at) {
        ps.set("cursor", at);
      }
      try {
        const res = await fetch(`/coddy/sessions?${ps.toString()}`);
        if (!res.ok) {
          throw new Error(String(res.status));
        }
        const data = (await res.json()) as ListResponse;
        if (ticket !== ticketRef.current) {
          return;
        }
        const next = data.sessions ?? [];
        setRows((prev) => {
          if (reset) {
            return next;
          }
          const seen = new Set(prev.map((r) => r.id));
          return [...prev, ...next.filter((r) => !seen.has(r.id))];
        });
        setCursor(data.nextCursor ?? null);
        setHasMore(!!data.hasMore);
        setError(null);
      } catch (e) {
        if (ticket !== ticketRef.current) {
          return;
        }
        setError(
          t("sessions.manage.loadFailed", {
            error: e instanceof Error ? e.message : String(e),
          }),
        );
      } finally {
        if (ticket === ticketRef.current) {
          setLoading(false);
        }
      }
    },
    [query, archiveFilter, tagFilter, sortKey, sortOrder, t],
  );

  // The refresh after a delete must use the search the field shows now, not the
  // one captured when the confirmation dialog opened: a debounce that fires
  // while the dialog is up would otherwise be overwritten by the older query.
  const loadRef = useRef(load);
  loadRef.current = load;

  useEffect(() => {
    void load(true, null);
  }, [load]);

  // The selection never holds a row the table is not showing. A search that
  // narrows the list drops the ticks that went with the hidden rows, so
  // "delete selected" always means exactly the ticked rows on screen - a
  // destructive action must never reach something invisible, and a tick must
  // never reappear later because it survived out of sight.
  useEffect(() => {
    setSelected((prev) => {
      if (prev.size === 0) {
        return prev;
      }
      const visible = new Set(selectableRows.map((row) => row.id));
      const next = new Set<string>();
      for (const id of prev) {
        if (visible.has(id)) {
          next.add(id);
        }
      }
      return next.size === prev.size ? prev : next;
    });
  }, [selectableRows]);

  // Debounce typing into the search box the way the History drawer does.
  useEffect(() => {
    const handle = window.setTimeout(
      () => setQuery(searchDraft.trim()),
      SEARCH_DEBOUNCE_MS,
    );
    return () => window.clearTimeout(handle);
  }, [searchDraft]);

  const selectedIds = selectableRows
    .filter((row) => selected.has(row.id))
    .map((row) => row.id);

  const runDelete = useCallback(
    async (ids: readonly string[]) => {
      setBusy(true);
      setError(null);
      // Reported after the refresh below: re-reading the listing clears the
      // error slot, so a message written before it would never be seen.
      let failure: string | null = null;
      try {
        const res = await fetch("/coddy/sessions/bulk-delete", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ ids: [...ids] }),
        });
        const data = (await res.json().catch(() => ({}))) as BulkDeleteResponse;
        if (!res.ok) {
          throw new Error(String(res.status));
        }
        const deleted = data.deleted ?? [];
        const failed = data.failed ?? [];
        setSelected((prev) => selectionWithout(prev, deleted));
        if (deleted.length > 0) {
          onSessionsDeleted?.(deleted);
        }
        if (failed.length > 0) {
          // Partial success is the normal answer when one session is mid-turn:
          // say which ones stayed instead of pretending the batch worked.
          failure = t("sessions.manage.partialFailure", {
            count: failed.length,
            reason: String(failed[0]?.error ?? ""),
          });
        }
      } catch (e) {
        failure = t("sessions.manage.deleteFailed", {
          error: e instanceof Error ? e.message : String(e),
        });
      } finally {
        setBusy(false);
        await loadRef.current(true, null);
        if (failure) {
          setError(failure);
        }
      }
    },
    [onSessionsDeleted, t],
  );

  const confirmAndDelete = useCallback(
    async (ids: readonly string[], title: string, message: string) => {
      if (ids.length === 0) {
        return;
      }
      const ok = await confirm({
        title,
        message,
        confirmLabel: t("common.delete"),
        variant: "danger",
      });
      if (!ok) {
        return;
      }
      await runDelete(ids);
    },
    [confirm, runDelete, t],
  );

  // Emptying the archive is a scope the server resolves, not a list of ticks:
  // the table holds one page and the archive may be larger than it, so the
  // request names the scope and the confirmation says as much.
  //
  // The protection of the open conversation travels with it. History can put
  // the conversation you are in into the archive, and this table promises that
  // nothing here deletes it - a promise a server-side scope would otherwise
  // walk straight past.
  const emptyArchive = useCallback(async () => {
    const ok = await confirm({
      title: t("sessions.manage.confirm.archived.title"),
      message: t("sessions.manage.confirm.archived.message"),
      confirmLabel: t("common.delete"),
      variant: "danger",
    });
    if (!ok) {
      return;
    }
    setBusy(true);
    setError(null);
    let failure: string | null = null;
    try {
      const res = await fetch("/coddy/sessions/bulk-delete", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(
          activeStoredId
            ? { scope: "archived", except: [activeStoredId] }
            : { scope: "archived" },
        ),
      });
      const data = (await res.json().catch(() => ({}))) as BulkDeleteResponse;
      if (!res.ok) {
        throw new Error(String(res.status));
      }
      const deleted = data.deleted ?? [];
      setSelected((prev) => selectionWithout(prev, deleted));
      if (deleted.length > 0) {
        onSessionsDeleted?.(deleted);
      }
      const failed = data.failed ?? [];
      if (failed.length > 0) {
        failure = t("sessions.manage.partialFailure", {
          count: failed.length,
          reason: String(failed[0]?.error ?? ""),
        });
      }
    } catch (e) {
      failure = t("sessions.manage.deleteFailed", {
        error: e instanceof Error ? e.message : String(e),
      });
    } finally {
      setBusy(false);
      await loadRef.current(true, null);
      if (failure) {
        setError(failure);
      }
    }
  }, [activeStoredId, confirm, onSessionsDeleted, t]);

  // Clicking the column that is already sorted flips it; a different column
  // starts from the direction that reads naturally for its kind of value.
  //
  // The two updates are queued side by side, never one from inside the other's
  // updater: React double-invokes updaters to surface impure ones, and a
  // setState nested in another would run twice - flipping the direction and
  // flipping it straight back.
  const sortBy = useCallback(
    (key: SessionSortKey) => {
      setSortOrder((prev) =>
        sortKey === key
          ? prev === "asc"
            ? "desc"
            : "asc"
          : defaultSortOrder(key),
      );
      setSortKey(key);
    },
    [sortKey],
  );

  const deleteSelectedLabel = t("sessions.manage.deleteSelected", {
    count: selectedIds.length,
  });
  const headerChecked = allRowsSelected(selectableRows, selected);
  const headerPartial = someRowsSelected(selectableRows, selected);

  /** One sortable column head: a button that carries the caret when active. */
  const sortableHead = (
    key: SessionSortKey,
    label: string,
    className?: string,
  ) => (
    <th
      className={className}
      scope="col"
      aria-sort={
        sortKey === key
          ? sortOrder === "asc"
            ? "ascending"
            : "descending"
          : "none"
      }
    >
      <button
        type="button"
        className="sessions-manager-sort"
        data-testid={`sessions-manager-sort-${key}`}
        title={t("sessions.manage.sortBy", { column: label })}
        onClick={() => sortBy(key)}
      >
        <span>{label}</span>
        {sortKey === key ? <SortCaret order={sortOrder} /> : null}
      </button>
    </th>
  );

  return (
    <div className="sessions-manager" data-testid="sessions-manager">
      <p className="settings-field-desc">{t("sessions.manage.lead")}</p>

      <div className="sessions-manager-toolbar">
        <input
          type="search"
          className="settings-input sessions-manager-search"
          placeholder={t("sessions.searchPlaceholder")}
          aria-label={t("sessions.searchAriaLabel")}
          data-testid="sessions-manager-search"
          value={searchDraft}
          onChange={(ev) => setSearchDraft(ev.target.value)}
        />
        {/* One action, and its words are its tooltip and its accessible name:
            the table has a tick per row and a tick for the whole page, so the
            scope of a delete is what the operator can see ticked rather than a
            button label they have to read carefully. */}
        <label className="sessions-manager-archive">
          <span className="sr-only">{t("sessions.manage.archive.label")}</span>
          <select
            className="settings-input sessions-manager-archive-select"
            data-testid="sessions-manager-archive-filter"
            aria-label={t("sessions.manage.archive.label")}
            value={archiveFilter}
            onChange={(ev) =>
              setArchiveFilter(ev.target.value as SessionArchiveFilter)
            }
          >
            <option value="exclude">
              {t("sessions.manage.archive.exclude")}
            </option>
            <option value="only">{t("sessions.manage.archive.only")}</option>
            <option value="all">{t("sessions.manage.archive.all")}</option>
          </select>
        </label>
        <div className="sessions-manager-actions">
          {/* The second action is a different scope, not a second reach past
              the ticks: it empties the archive, which the operator filled one
              conversation at a time and can look at with the filter beside it. */}
          <button
            type="button"
            className="settings-btn settings-btn-danger settings-btn-icon sessions-manager-action"
            data-testid="sessions-manager-delete-archived"
            disabled={busy}
            title={t("sessions.manage.deleteArchived")}
            aria-label={t("sessions.manage.deleteArchived")}
            onClick={() => void emptyArchive()}
          >
            <IconArchive />
          </button>
          <button
            type="button"
            className="settings-btn settings-btn-danger settings-btn-icon sessions-manager-action"
            data-testid="sessions-manager-delete-selected"
            disabled={busy || selectedIds.length === 0}
            title={deleteSelectedLabel}
            aria-label={deleteSelectedLabel}
            onClick={() =>
              void confirmAndDelete(
                selectedIds,
                tp(
                  "sessions.manage.confirm.selected.title",
                  selectedIds.length,
                  { count: selectedIds.length },
                ),
                t("sessions.manage.confirm.selected.message"),
              )
            }
          >
            <IconTrash mark="check" />
            {selectedIds.length > 0 ? (
              <span
                className="sessions-manager-action-count"
                aria-hidden
                data-testid="sessions-manager-selected-count"
              >
                {selectedIds.length}
              </span>
            ) : null}
          </button>
        </div>
      </div>

      {tagFilter ? (
        <p
          className="settings-muted sessions-manager-tag-filter"
          data-testid="sessions-manager-tag-filter"
        >
          {t("sessions.manage.tagFilter", { tag: tagFilter })}{" "}
          <button
            type="button"
            className="sessions-manager-tag-clear"
            data-testid="sessions-manager-tag-filter-clear"
            onClick={() => setTagFilter("")}
          >
            {t("sessions.manage.tagFilterClear")}
          </button>
        </p>
      ) : null}

      {error ? (
        <p className="settings-error" data-testid="sessions-manager-error">
          {error}
        </p>
      ) : null}

      <div className="sessions-manager-tablewrap">
        <table className="sessions-manager-table">
          <thead>
            <tr>
              <th className="sessions-manager-col-pick" scope="col">
                <input
                  type="checkbox"
                  aria-label={t("sessions.manage.selectAll")}
                  data-testid="sessions-manager-select-all"
                  checked={headerChecked}
                  ref={(el) => {
                    if (el) {
                      el.indeterminate = headerPartial;
                    }
                  }}
                  onChange={(ev) =>
                    setSelected((prev) =>
                      toggleAllRows(selectableRows, prev, ev.target.checked),
                    )
                  }
                />
              </th>
              {sortableHead("title", t("sessions.manage.column.conversation"))}
              <th scope="col">{t("sessions.manage.column.model")}</th>
              {sortableHead(
                "messages",
                t("sessions.manage.column.messages"),
                "sessions-manager-col-num",
              )}
              {sortableHead(
                "tokens",
                t("sessions.manage.column.tokens"),
                "sessions-manager-col-num",
              )}
              {sortableHead("created", t("sessions.manage.column.created"))}
              {sortableHead("updated", t("sessions.manage.column.updated"))}
            </tr>
          </thead>
          <tbody>
            {rows.map((row) => {
              const usage = row.tokenUsage ?? {};
              const total = rowTotalTokens(row);
              const protectedRow = row.id === activeStoredId;
              return (
                <tr
                  key={row.id}
                  className={protectedRow ? "is-active-session" : undefined}
                  data-testid={`sessions-manager-row-${row.id}`}
                >
                  <td className="sessions-manager-col-pick">
                    <input
                      type="checkbox"
                      aria-label={
                        protectedRow
                          ? t("sessions.manage.protectedRow")
                          : t("sessions.manage.selectRow", {
                              title: row.title || t("sessions.newChatFallback"),
                            })
                      }
                      {...(protectedRow
                        ? { title: t("sessions.manage.protectedRow") }
                        : {})}
                      data-testid={`sessions-manager-pick-${row.id}`}
                      disabled={protectedRow}
                      checked={selected.has(row.id)}
                      onChange={(ev) =>
                        setSelected((prev) =>
                          toggleSelected(prev, row.id, ev.target.checked),
                        )
                      }
                    />
                  </td>
                  <td className="sessions-manager-col-title">
                    <span className="sessions-manager-title-line">
                      {/* The archive is the state the row is in, so it leads the
                          title as a mark rather than standing among the tags,
                          which are labels the operator chose. */}
                      {row.archived ? (
                        <span
                          className="sessions-manager-archived-mark"
                          data-testid={`sessions-manager-archived-${row.id}`}
                          aria-label={t("sessions.manage.archivedBadge")}
                          title={
                            row.archivedAt
                              ? t("sessions.manage.archivedOn", {
                                  date: formatRowTimestampFull(row.archivedAt),
                                })
                              : t("sessions.manage.archivedBadge")
                          }
                        >
                          <IconArchive />
                        </span>
                      ) : null}
                      <span title={row.title || row.id}>
                        {row.title || t("sessions.newChatFallback")}
                      </span>
                    </span>
                    {/* The workspace, and the "open" mark of the protected
                        row, are a second line rather than columns of their
                        own: both are context for the title, and the table has
                        a drawer to fit into. */}
                    <span className="sessions-manager-sub">
                      <span
                        className="sessions-manager-cwd"
                        title={row.cwd || ""}
                      >
                        {workspaceBasename(row.cwd) || "—"}
                      </span>
                      {protectedRow ? (
                        <span
                          className="sessions-manager-badge"
                          title={t("sessions.manage.protectedRow")}
                        >
                          {t("sessions.manage.openBadge")}
                        </span>
                      ) : null}
                      {/* A tag is a filter you can reach: clicking one narrows
                          the table to the conversations filed under it. */}
                      {(row.tags ?? []).map((tag) => (
                        <button
                          key={tag}
                          type="button"
                          className="sessions-manager-tag"
                          data-testid={`sessions-manager-tag-${tag}`}
                          title={t("sessions.manage.filterByTag", { tag })}
                          onClick={() => setTagFilter(tag)}
                        >
                          {tag}
                        </button>
                      ))}
                    </span>
                  </td>
                  <td className="sessions-manager-col-model">
                    <span
                      title={row.model || t("sessions.manage.modelDefault")}
                    >
                      {row.model || t("sessions.manage.modelDefault")}
                    </span>
                  </td>
                  <td className="sessions-manager-col-num">
                    {row.messageCount ?? 0}
                  </td>
                  <td
                    className="sessions-manager-col-num"
                    title={t("sessions.manage.tokensBreakdown", {
                      input: usage.inputTokens ?? 0,
                      output: usage.outputTokens ?? 0,
                      total,
                    })}
                  >
                    {formatTokenCount(total)}
                  </td>
                  <td title={formatRowTimestampFull(row.createdAt)}>
                    {formatRowTimestamp(row.createdAt)}
                  </td>
                  <td title={formatRowTimestampFull(row.updatedAt)}>
                    {formatRowTimestamp(row.updatedAt)}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
        {rows.length === 0 && !loading ? (
          <p
            className="settings-muted sessions-manager-empty"
            data-testid="sessions-manager-empty"
          >
            {query || tagFilter
              ? t("sessions.manage.noMatches")
              : archiveFilter === "only"
                ? t("sessions.manage.noArchivedMatches")
                : t("sessions.empty")}
          </p>
        ) : null}
      </div>

      <div className="sessions-manager-footer">
        <span className="settings-muted" data-testid="sessions-manager-summary">
          {tp("sessions.manage.shown", rows.length, { count: rows.length })}
          {selectedIds.length > 0
            ? ` ${t("sessions.manage.selectedSuffix", { count: selectedIds.length })}`
            : ""}
        </span>
        {hasMore ? (
          <button
            type="button"
            className="settings-btn"
            data-testid="sessions-manager-load-more"
            disabled={loading || busy}
            onClick={() => void load(false, cursor)}
          >
            {t("sessions.manage.loadMore")}
          </button>
        ) : null}
      </div>
    </div>
  );
}
