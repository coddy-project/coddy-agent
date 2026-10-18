/**
 * The Settings search: the field, its results, and the small store the drawer
 * owns so that typing never re-renders the tab under the results. The field is
 * a combobox over a listbox like the documentation search (docs/DocsView.tsx);
 * the results take the content column, since the field sits in a 200px rail.
 */
import {
  Fragment,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
  type KeyboardEvent,
  type RefObject,
} from "react";
import { useT } from "../i18n/I18nProvider";
import {
  serverSnapshotTouchOnly,
  snapshotTouchOnly,
  subscribeTouchOnly,
} from "../shellBreakpoint";
import {
  descriptionLine,
  markWords,
  searchSettings,
  searchWords,
  type SettingsSearchEntry,
  type SettingsSearchHit,
} from "./settingsSearch";

export const STATUS_DEBOUNCE_MS = 500;

export type SettingsSearchState = {
  query: string;
  /** Whether the results are asked for; they show when the query has a word too. */
  open: boolean;
  active: number;
  /** The result last opened, highlighted when the results come back. */
  chosenId: string | null;
};

type Found = { hits: SettingsSearchHit[]; more: number };

export type SettingsSearchStore = {
  get: () => SettingsSearchState;
  subscribe: (listener: () => void) => () => void;
  type: (query: string) => void;
  show: (active?: number) => void;
  hide: () => void;
  clear: () => void;
  move: (active: number) => void;
  choose: (id: string) => void;
  /**
   * The answer to the current query over an index, kept for the same index and
   * words: the field and the results ask in one render and get the same object,
   * which is also what tells them the hits changed.
   */
  answer: (index: readonly SettingsSearchEntry[]) => Found;
};

export function createSettingsSearch(): SettingsSearchStore {
  let state: SettingsSearchState = {
    query: "",
    open: false,
    active: 0,
    chosenId: null,
  };
  const listeners = new Set<() => void>();
  let last: {
    index: readonly SettingsSearchEntry[];
    words: string;
    found: Found;
  } | null = null;
  const set = (patch: Partial<SettingsSearchState>) => {
    const next = { ...state, ...patch };
    if (
      next.query === state.query &&
      next.open === state.open &&
      next.active === state.active &&
      next.chosenId === state.chosenId
    ) {
      return;
    }
    state = next;
    listeners.forEach((listener) => listener());
  };
  return {
    get: () => state,
    subscribe: (listener) => {
      listeners.add(listener);
      return () => {
        listeners.delete(listener);
      };
    },
    type: (query) => set({ query, open: true }),
    show: (active) =>
      set(active === undefined ? { open: true } : { open: true, active }),
    hide: () => set({ open: false }),
    clear: () => set({ query: "", open: false, active: 0, chosenId: null }),
    move: (active) => set({ active }),
    choose: (id) => set({ chosenId: id, open: false }),
    answer: (index) => {
      const words = searchWords(state.query).join(" ");
      if (last === null || last.index !== index || last.words !== words) {
        last = { index, words, found: searchSettings(index, state.query) };
      }
      return last.found;
    },
  };
}

export function useSettingsSearchState(
  search: SettingsSearchStore,
): SettingsSearchState {
  return useSyncExternalStore(search.subscribe, search.get, search.get);
}

function resultsShowing(state: SettingsSearchState): boolean {
  return state.open && state.query.trim() !== "";
}

/** Settings reads only this while it renders: not the query as it is typed. */
export function useResultsShowing(search: SettingsSearchStore): boolean {
  const snapshot = () => resultsShowing(search.get());
  return useSyncExternalStore(search.subscribe, snapshot, snapshot);
}

function firstActive(
  hits: readonly SettingsSearchHit[],
  chosenId: string | null,
) {
  const at =
    chosenId === null ? -1 : hits.findIndex((h) => h.entry.id === chosenId);
  return at >= 0 ? at : 0;
}

function optionId(resultsId: string, at: number): string {
  return `${resultsId}-option-${at}`;
}

export function SettingsSearchField(props: {
  search: SettingsSearchStore;
  index: readonly SettingsSearchEntry[];
  inputRef: RefObject<HTMLInputElement | null>;
  resultsId: string;
  disabled: boolean;
  onOpen: (entry: SettingsSearchEntry) => void;
}) {
  const { search, index, inputRef, resultsId, disabled, onOpen } = props;
  const { t } = useT();
  // The "/" badge shows while the cursor is elsewhere and the field is empty,
  // on every width (a narrow desktop window has a keyboard), never on touch.
  const touchOnly = useSyncExternalStore(
    subscribeTouchOnly,
    snapshotTouchOnly,
    serverSnapshotTouchOnly,
  );
  const [focused, setFocused] = useState(false);
  const state = useSettingsSearchState(search);
  const { hits } = search.answer(index);
  const showing = resultsShowing(state);
  const active = Math.min(state.active, hits.length - 1);

  // The chosen result is highlighted if it is still among the hits, else the first.
  useLayoutEffect(() => {
    search.move(firstActive(hits, search.get().chosenId));
  }, [search, hits]);

  const bringBack = () => {
    if (!resultsShowing(search.get())) {
      search.show(firstActive(hits, search.get().chosenId));
    }
  };

  const onKeyDown = (e: KeyboardEvent<HTMLInputElement>) => {
    // Enter and Escape belong to the input method while it composes.
    if (e.nativeEvent.isComposing || e.keyCode === 229) {
      return;
    }
    if (e.key === "Escape") {
      if (search.get().query !== "") {
        e.preventDefault();
        search.clear();
      }
      return;
    }
    if (!showing || hits.length === 0) {
      return;
    }
    if (e.key === "ArrowDown" || e.key === "ArrowUp") {
      e.preventDefault();
      const next =
        e.key === "ArrowDown"
          ? (active + 1) % hits.length
          : (active - 1 + hits.length) % hits.length;
      search.move(next);
      document
        .getElementById(optionId(resultsId, next))
        ?.scrollIntoView?.({ block: "nearest" });
    } else if (e.key === "Enter") {
      e.preventDefault();
      const hit = hits[active];
      if (hit !== undefined) {
        onOpen(hit.entry);
      }
    }
  };

  return (
    <div className="settings-search-box">
      <input
        ref={inputRef}
        className="settings-search-input"
        data-testid="settings-search"
        type="search"
        role="combobox"
        autoComplete="off"
        aria-autocomplete="list"
        aria-controls={resultsId}
        aria-expanded={showing && hits.length > 0}
        aria-activedescendant={
          showing && active >= 0 ? optionId(resultsId, active) : undefined
        }
        placeholder={t("settings.search.placeholder")}
        aria-label={t("settings.search.placeholder")}
        value={state.query}
        disabled={disabled}
        onChange={(e) =>
          // An emptied field is a cleared search, as Escape leaves it.
          e.target.value === "" ? search.clear() : search.type(e.target.value)
        }
        onFocus={() => {
          setFocused(true);
          bringBack();
        }}
        onBlur={() => setFocused(false)}
        onClick={bringBack}
        onKeyDown={onKeyDown}
      />
      {!touchOnly && !focused && state.query === "" ? (
        <kbd className="settings-search-kbd" aria-hidden="true">
          /
        </kbd>
      ) : null}
    </div>
  );
}

function Marked(props: { text: string; words: readonly string[] }) {
  return (
    <>
      {markWords(props.text, props.words).map((part, i) =>
        part.hit ? (
          <mark key={i}>{part.text}</mark>
        ) : (
          <Fragment key={i}>{part.text}</Fragment>
        ),
      )}
    </>
  );
}

function describing(
  entry: SettingsSearchEntry,
  words: readonly string[],
): string | null {
  const holding = entry.descriptions.find((d) =>
    markWords(d, words).some((part) => part.hit),
  );
  return holding ?? entry.descriptions[0] ?? null;
}

export function SettingsSearchResults(props: {
  id: string;
  search: SettingsSearchStore;
  index: readonly SettingsSearchEntry[];
  onOpen: (entry: SettingsSearchEntry) => void;
}) {
  const { id, search, index, onOpen } = props;
  const { t, tp } = useT();
  const state = useSettingsSearchState(search);
  const { hits, more } = search.answer(index);
  const words = useMemo(() => searchWords(state.query), [state.query]);
  const active = Math.min(state.active, hits.length - 1);

  // The highlighted result stays in view as the hits change and the arrows
  // move; a pointer moving it needs no scrolling.
  const byPointer = useRef(false);
  // A browser reports a move at the same spot when new results come in under a
  // resting pointer; only a move to another spot is the operator's, so the
  // first move after a change only records where the pointer is.
  const pointerAt = useRef<{ x: number; y: number } | null>(null);
  useLayoutEffect(() => {
    pointerAt.current = null;
  }, [hits]);
  useEffect(() => {
    if (byPointer.current) {
      byPointer.current = false;
      return;
    }
    if (active >= 0) {
      document
        .getElementById(optionId(id, active))
        ?.scrollIntoView?.({ block: "nearest" });
    }
  }, [id, active, hits]);

  const total = hits.length + more;
  const count = tp("settings.search.found", total);
  const [announced, setAnnounced] = useState("");
  useEffect(() => {
    const timer = window.setTimeout(
      () => setAnnounced(count),
      STATUS_DEBOUNCE_MS,
    );
    return () => window.clearTimeout(timer);
  }, [count]);

  return (
    <div
      className="settings-scroll settings-search-results"
      data-testid="settings-search-results"
    >
      <span className="sr-only" role="status">
        {announced}
      </span>
      {hits.length === 0 ? (
        <p
          className="settings-search-empty"
          data-testid="settings-search-empty"
        >
          {t("settings.search.empty")}
        </p>
      ) : (
        <ul
          className="settings-hits"
          id={id}
          role="listbox"
          aria-label={t("settings.search.results")}
          data-testid="settings-search-hits"
        >
          {hits.map((hit, i) => {
            const { entry } = hit;
            const description =
              hit.tier === 3 ? describing(entry, words) : null;
            const descId = `${optionId(id, i)}-desc`;
            const key = entry.keyPath;
            return (
              <li key={entry.id} role="presentation">
                <button
                  type="button"
                  id={optionId(id, i)}
                  role="option"
                  tabIndex={-1}
                  aria-selected={i === active}
                  aria-describedby={description !== null ? descId : undefined}
                  className={`settings-hit${i === active ? " is-selected" : ""}`}
                  data-settings-target={entry.anchor ?? `tab:${entry.section}`}
                  data-settings-row={entry.row ?? undefined}
                  onMouseMove={(e) => {
                    const last = pointerAt.current;
                    pointerAt.current = { x: e.clientX, y: e.clientY };
                    if (
                      last === null ||
                      (last.x === e.clientX && last.y === e.clientY)
                    ) {
                      return;
                    }
                    // Only a move runs the effect that takes the flag down.
                    if (i !== active) {
                      byPointer.current = true;
                      search.move(i);
                    }
                  }}
                  onClick={() => onOpen(entry)}
                >
                  <span className="settings-hit-title">
                    <Marked text={entry.names[0] ?? ""} words={words} />
                    {hit.secondMatched !== null ? (
                      <>
                        {" · "}
                        <Marked text={hit.secondMatched} words={words} />
                      </>
                    ) : null}
                  </span>
                  {entry.where.length > 0 || key !== null ? (
                    <span className="settings-hit-where">
                      {entry.where.length > 0 ? (
                        <Marked text={entry.where.join(" › ")} words={words} />
                      ) : null}
                      {key !== null ? (
                        <>
                          {entry.where.length > 0 ? " " : null}
                          <code className="settings-hit-key">
                            <Marked text={key} words={words} />
                          </code>
                        </>
                      ) : null}
                    </span>
                  ) : null}
                  {description !== null ? (
                    <span
                      id={descId}
                      className="settings-hit-desc"
                      aria-hidden="true"
                    >
                      <Marked
                        text={descriptionLine(description, words)}
                        words={words}
                      />
                    </span>
                  ) : null}
                </button>
              </li>
            );
          })}
        </ul>
      )}
      {more > 0 ? (
        <p className="settings-search-more" data-testid="settings-search-more">
          {tp("settings.search.more", more)}
        </p>
      ) : null}
    </div>
  );
}
