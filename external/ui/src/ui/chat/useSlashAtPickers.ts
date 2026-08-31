import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type Dispatch,
  type RefObject,
  type SetStateAction,
} from "react";
import { useT } from "../i18n/I18nProvider";
import {
  draftExtendsFailedAtPrefix,
  atMenuDraftAtCaret,
} from "../skills/draftAt";
import {
  draftExtendsFailedSlashPrefix,
  slashMenuDraftAtCaret,
} from "../skills/draftSlash";
import { filterCommandRows } from "../skills/commandRows";
import {
  pickerRowFromRecent,
  readWorkspaceAtRecents,
  recordWorkspaceAtRecent,
  WORKSPACE_AT_RECENTS_NO_SESSION_KEY,
} from "../skills/workspaceAtRecents";
import {
  fetchAtPage as fetchAtPageApi,
  fetchCommands,
  fetchSlashPage as fetchSlashPageApi,
  type SlashRow,
  type WorkspaceFileRow,
} from "./composerPickerApi";

/**
 * The composer's `/` skills+commands picker and `@` workspace-files picker:
 * caret-driven open/close, race-guarded page fetches, no-match suppression,
 * choice application (text splice + caret restore), and load-more paging.
 */
export function useSlashAtPickers({
  sessionId,
  value,
  onChange,
  taRef,
}: {
  sessionId: string;
  value: string;
  onChange: (v: string) => void;
  taRef: RefObject<HTMLTextAreaElement | null>;
}): {
  slashOpen: boolean;
  atOpen: boolean;
  pickerOpen: boolean;
  slashItems: SlashRow[];
  commandMatches: SlashRow[];
  slashRows: SlashRow[];
  slashActiveIdx: number;
  setSlashActive: Dispatch<SetStateAction<number>>;
  showSkillsSection: boolean;
  slashLoading: boolean;
  slashErr: string | null;
  slashHasMore: boolean;
  atItems: WorkspaceFileRow[];
  atPrefix: string;
  atLoading: boolean;
  atErr: string | null;
  atHasMore: boolean;
  slashNoMatch: { slashIdx: number; prefix: string } | null;
  atNoMatch: { atIdx: number; prefix: string } | null;
  updatePickerMenus: (value: string, caret: number) => void;
  dismissSlashAtPickers: () => void;
  applySlashChoice: (name: string) => void;
  applyAtChoice: (row: WorkspaceFileRow) => void;
  loadMoreSlash: () => void;
  loadMoreAt: () => void;
} {
  const { t } = useT();
  /** Bump when the slash draft changes or is dismissed so stale list responses are ignored. */
  const slashFetchGenRef = useRef(0);
  const [slashItems, setSlashItems] = useState<SlashRow[]>([]);
  /** Index of the keyboard-highlighted row across the flat skills+commands list. */
  const [slashActive, setSlashActive] = useState(0);
  /** Built-in deterministic commands (/compact, /plugin) shown as a separate group. */
  const [commandItems, setCommandItems] = useState<SlashRow[]>([]);
  const commandItemsRef = useRef<SlashRow[]>([]);
  const commandsFetchedRef = useRef(false);
  const [slashOpen, setSlashOpen] = useState(false);
  const [slashPrefix, setSlashPrefix] = useState("");
  const [slashLoading, setSlashLoading] = useState(false);
  const [slashErr, setSlashErr] = useState<string | null>(null);
  const [slashPage, setSlashPage] = useState(1);
  const [slashHasMore, setSlashHasMore] = useState(false);
  const [slashReplace, setSlashReplace] = useState<{
    from: number;
    to: number;
  } | null>(null);
  /** Server returned zero rows for failed `prefix`; hide picker/chip while the user extends that prefix at the same `/`. */
  const [slashNoMatch, setSlashNoMatch] = useState<{
    slashIdx: number;
    prefix: string;
  } | null>(null);
  const atFetchGenRef = useRef(0);
  /**
   * After a workspace row is chosen, `setSelectionRange` + textarea `select` fires
   * `updatePickerMenus` while the line still matches `atMenuDraftAtCaret`
   * (file picks append a trailing space, which MENU_PATH treats as inside the `@` token).
   * Skip reopening `@` on the next picker sync ticks (handles duplicate selection events).
   */
  const deferAtDraftPickerTicksRef = useRef(0);
  const [atItems, setAtItems] = useState<WorkspaceFileRow[]>([]);
  const [atOpen, setAtOpen] = useState(false);
  const [atPrefix, setAtPrefix] = useState("");
  const [atLoading, setAtLoading] = useState(false);
  const [atErr, setAtErr] = useState<string | null>(null);
  const [atPage, setAtPage] = useState(1);
  const [atHasMore, setAtHasMore] = useState(false);
  const [atReplace, setAtReplace] = useState<{
    from: number;
    to: number;
  } | null>(null);
  const [atNoMatch, setAtNoMatch] = useState<{
    atIdx: number;
    prefix: string;
  } | null>(null);

  const pickerOpen = slashOpen || atOpen;

  const bumpSlashFetchGen = () => {
    slashFetchGenRef.current++;
  };

  const bumpAtFetchGen = () => {
    atFetchGenRef.current++;
  };

  /** Close floating slash/workspace pickers without mutating textarea text (Escape or sheet backdrop). */
  function dismissSlashAtPickers() {
    setSlashOpen(false);
    setSlashReplace(null);
    setSlashNoMatch(null);
    bumpSlashFetchGen();
    setSlashLoading(false);
    setSlashErr(null);

    setAtOpen(false);
    setAtReplace(null);
    setAtNoMatch(null);
    bumpAtFetchGen();
    setAtLoading(false);
    setAtErr(null);
  }

  const fetchSlashPage = useCallback(
    (prefix: string, page: number) => fetchSlashPageApi(sessionId, prefix, page),
    [sessionId],
  );

  // Built-in deterministic commands (/compact, /plugin) are static per config, so
  // fetch them once the first time the slash menu opens and cache the result.
  const fetchCommandsOnce = useCallback(async () => {
    if (commandsFetchedRef.current) {
      return;
    }
    commandsFetchedRef.current = true;
    const rows = await fetchCommands();
    if (rows === null) {
      return;
    }
    commandItemsRef.current = rows;
    setCommandItems(rows);
  }, []);

  // Load the built-in commands once on mount so they are ready before the user
  // narrows the slash prefix (avoids racing the skills-zero auto-close).
  useEffect(() => {
    void fetchCommandsOnce();
  }, [fetchCommandsOnce]);

  const fetchAtPage = useCallback(
    (prefix: string, page: number) => fetchAtPageApi(sessionId, prefix, page),
    [sessionId],
  );

  const updateSlashMenu = useCallback(
    (value: string, caret: number) => {
      const draft = slashMenuDraftAtCaret(value, caret);
      if (!draft.open) {
        bumpSlashFetchGen();
        setSlashOpen(false);
        setSlashReplace(null);
        setSlashNoMatch(null);
        setSlashLoading(false);
        return;
      }
      if (slashNoMatch && draftExtendsFailedSlashPrefix(draft, slashNoMatch)) {
        bumpSlashFetchGen();
        setSlashOpen(false);
        setSlashReplace(null);
        setSlashLoading(false);
        return;
      }
      setSlashOpen(true);
      setSlashReplace({ from: draft.slashIdx, to: draft.caret });
      setSlashPrefix(draft.prefix);
      slashFetchGenRef.current += 1;
      const gen = slashFetchGenRef.current;
      void (async () => {
        const el = taRef.current;
        const now = el
          ? slashMenuDraftAtCaret(
              el.value,
              el.selectionStart ?? el.value.length,
            )
          : null;
        if (
          gen !== slashFetchGenRef.current ||
          !now ||
          !now.open ||
          now.slashIdx !== draft.slashIdx ||
          now.prefix !== draft.prefix
        ) {
          return;
        }
        setSlashLoading(true);
        setSlashErr(null);
        try {
          const body = await fetchSlashPage(now.prefix, 1);
          if (gen !== slashFetchGenRef.current) {
            return;
          }
          const el2 = taRef.current;
          const after = el2
            ? slashMenuDraftAtCaret(
                el2.value,
                el2.selectionStart ?? el2.value.length,
              )
            : null;
          if (
            !after ||
            !after.open ||
            after.slashIdx !== now.slashIdx ||
            after.prefix !== now.prefix
          ) {
            return;
          }
          const rows = body.items || [];
          setSlashItems(rows);
          setSlashPage(1);
          setSlashHasMore(!!body.has_more);
          if (rows.length === 0) {
            // No skills match — but keep the menu open if a built-in command does.
            const cmdMatches = filterCommandRows(
              commandItemsRef.current,
              after.prefix,
            );
            if (cmdMatches.length === 0) {
              setSlashNoMatch({
                slashIdx: after.slashIdx,
                prefix: after.prefix,
              });
              setSlashOpen(false);
              setSlashReplace(null);
            } else {
              setSlashNoMatch(null);
            }
          } else {
            setSlashNoMatch(null);
          }
        } catch (e) {
          if (gen !== slashFetchGenRef.current) {
            return;
          }
          setSlashErr(
            e instanceof Error ? e.message : t("composer.requestFailed"),
          );
          setSlashItems([]);
          setSlashHasMore(false);
          setSlashNoMatch(null);
        } finally {
          if (gen === slashFetchGenRef.current) {
            setSlashLoading(false);
          }
        }
      })();
    },
    [fetchSlashPage, slashNoMatch, t],
  );

  const updateAtMenu = useCallback(
    (value: string, caret: number) => {
      const draft = atMenuDraftAtCaret(value, caret);
      if (!draft.open) {
        bumpAtFetchGen();
        setAtOpen(false);
        setAtReplace(null);
        setAtNoMatch(null);
        setAtLoading(false);
        return;
      }
      if (atNoMatch && draftExtendsFailedAtPrefix(draft, atNoMatch)) {
        bumpAtFetchGen();
        setAtOpen(false);
        setAtReplace(null);
        setAtLoading(false);
        return;
      }
      setAtOpen(true);
      setAtReplace({ from: draft.atIdx, to: draft.caret });
      setAtPrefix(draft.prefix);

      if (draft.prefix.trim() === "") {
        bumpAtFetchGen();
        const wk =
          sessionId.trim() || WORKSPACE_AT_RECENTS_NO_SESSION_KEY;
        const recents = readWorkspaceAtRecents(wk).map(pickerRowFromRecent);
        setAtItems(recents);
        setAtPage(1);
        setAtHasMore(false);
        setAtNoMatch(null);
        setAtLoading(false);
        setAtErr(null);
        return;
      }

      atFetchGenRef.current += 1;
      const gen = atFetchGenRef.current;
      void (async () => {
        const el = taRef.current;
        const now = el
          ? atMenuDraftAtCaret(el.value, el.selectionStart ?? el.value.length)
          : null;
        if (
          gen !== atFetchGenRef.current ||
          !now ||
          !now.open ||
          now.atIdx !== draft.atIdx ||
          now.prefix !== draft.prefix
        ) {
          return;
        }
        setAtLoading(true);
        setAtErr(null);
        try {
          const body = await fetchAtPage(now.prefix.trimEnd(), 1);
          if (gen !== atFetchGenRef.current) {
            return;
          }
          const el2 = taRef.current;
          const after = el2
            ? atMenuDraftAtCaret(
                el2.value,
                el2.selectionStart ?? el2.value.length,
              )
            : null;
          if (
            !after ||
            !after.open ||
            after.atIdx !== now.atIdx ||
            after.prefix !== now.prefix
          ) {
            return;
          }
          const rows = body.items || [];
          setAtItems(rows);
          setAtPage(1);
          setAtHasMore(!!body.has_more);
          if (rows.length === 0) {
            setAtNoMatch({ atIdx: after.atIdx, prefix: after.prefix });
            setAtItems([]);
            setAtHasMore(false);
          } else {
            setAtNoMatch(null);
          }
        } catch (e) {
          if (gen !== atFetchGenRef.current) {
            return;
          }
          setAtErr(
            e instanceof Error ? e.message : t("composer.requestFailed"),
          );
          setAtItems([]);
          setAtHasMore(false);
          setAtNoMatch(null);
        } finally {
          if (gen === atFetchGenRef.current) {
            setAtLoading(false);
          }
        }
      })();
    },
    [fetchAtPage, atNoMatch, sessionId, t],
  );

  const updatePickerMenus = useCallback(
    (value: string, caret: number) => {
      let deferAtDraft = false;
      if (deferAtDraftPickerTicksRef.current > 0) {
        deferAtDraftPickerTicksRef.current -= 1;
        deferAtDraft = true;
      }
      const ad = atMenuDraftAtCaret(value, caret);
      if (ad.open && !deferAtDraft) {
        bumpSlashFetchGen();
        setSlashOpen(false);
        setSlashReplace(null);
        setSlashNoMatch(null);
        setSlashLoading(false);
        updateAtMenu(value, caret);
        return;
      }
      bumpAtFetchGen();
      setAtOpen(false);
      setAtReplace(null);
      setAtNoMatch(null);
      setAtLoading(false);
      updateSlashMenu(value, caret);
    },
    [updateAtMenu, updateSlashMenu],
  );

  const applySlashChoice = (name: string) => {
    if (!slashReplace) {
      return;
    }
    const { from, to } = slashReplace;
    const insert = `/${name} `;
    const next = value.slice(0, from) + insert + value.slice(to);
    onChange(next);
    setSlashOpen(false);
    setSlashReplace(null);
    setSlashNoMatch(null);
    bumpSlashFetchGen();
    setSlashLoading(false);
    setAtOpen(false);
    setAtReplace(null);
    setAtNoMatch(null);
    bumpAtFetchGen();
    requestAnimationFrame(() => {
      const el = taRef.current;
      if (!el) {
        return;
      }
      const pos = from + insert.length;
      el.focus();
      el.setSelectionRange(pos, pos);
    });
  };

  const applyAtChoice = (row: WorkspaceFileRow) => {
    if (!atReplace) {
      return;
    }
    deferAtDraftPickerTicksRef.current = 2;
    const { from, to } = atReplace;
    const insert =
      row.kind === "dir"
        ? `@${row.path_rel}`
        : `@${row.path_rel.replace(/\/$/, "")} `;
    const next = value.slice(0, from) + insert + value.slice(to);
    onChange(next);
    recordWorkspaceAtRecent(
      sessionId.trim() || WORKSPACE_AT_RECENTS_NO_SESSION_KEY,
      row,
    );
    setAtOpen(false);
    setAtReplace(null);
    setAtNoMatch(null);
    bumpAtFetchGen();
    setSlashOpen(false);
    setSlashReplace(null);
    setSlashNoMatch(null);
    bumpSlashFetchGen();
    setSlashLoading(false);
    requestAnimationFrame(() => {
      const el = taRef.current;
      if (!el) {
        return;
      }
      const pos = from + insert.length;
      el.focus();
      el.setSelectionRange(pos, pos);
    });
  };

  const loadMoreSlash = () => {
    if (!slashOpen || slashLoading || !slashHasMore) {
      return;
    }
    void (async () => {
      setSlashLoading(true);
      setSlashErr(null);
      try {
        const nextPage = slashPage + 1;
        const body = await fetchSlashPage(slashPrefix, nextPage);
        const more = body.items || [];
        setSlashItems((prev) => [...prev, ...more]);
        if (more.length > 0) {
          setSlashNoMatch(null);
        }
        setSlashPage(nextPage);
        setSlashHasMore(!!body.has_more);
      } catch (e) {
        setSlashErr(
          e instanceof Error ? e.message : t("composer.requestFailed"),
        );
      } finally {
        setSlashLoading(false);
      }
    })();
  };

  const loadMoreAt = () => {
    if (!atOpen || atLoading || !atHasMore || atPrefix.trim() === "") {
      return;
    }
    void (async () => {
      setAtLoading(true);
      setAtErr(null);
      try {
        const nextPage = atPage + 1;
        const body = await fetchAtPage(atPrefix.trimEnd(), nextPage);
        const more = body.items || [];
        setAtItems((prev) => [...prev, ...more]);
        if (more.length > 0) {
          setAtNoMatch(null);
        }
        setAtPage(nextPage);
        setAtHasMore(!!body.has_more);
      } catch (e) {
        setAtErr(e instanceof Error ? e.message : t("composer.requestFailed"));
      } finally {
        setAtLoading(false);
      }
    })();
  };

  const commandMatches = useMemo(
    () => (slashOpen ? filterCommandRows(commandItems, slashPrefix) : []),
    [slashOpen, commandItems, slashPrefix],
  );
  // Flat, render-ordered list of selectable rows (skills first, then commands),
  // used for arrow-key navigation. The highlighted index is clamped to it.
  const slashRows = useMemo(
    () => [...slashItems, ...commandMatches],
    [slashItems, commandMatches],
  );
  const slashActiveIdx = slashRows.length
    ? Math.min(Math.max(slashActive, 0), slashRows.length - 1)
    : 0;
  // Reset the highlight to the first row whenever the query changes or the menu
  // (re)opens, so "first row selected by default" always holds.
  useEffect(() => {
    setSlashActive(0);
  }, [slashPrefix, slashOpen]);
  // Hide the Skills group when only built-in commands match, so a lone command
  // does not sit under an empty "Skills" header.
  const showSkillsSection =
    slashLoading ||
    !!slashErr ||
    slashItems.length > 0 ||
    commandMatches.length === 0;

  return {
    slashOpen,
    atOpen,
    pickerOpen,
    slashItems,
    commandMatches,
    slashRows,
    slashActiveIdx,
    setSlashActive,
    showSkillsSection,
    slashLoading,
    slashErr,
    slashHasMore,
    atItems,
    atPrefix,
    atLoading,
    atErr,
    atHasMore,
    slashNoMatch,
    atNoMatch,
    updatePickerMenus,
    dismissSlashAtPickers,
    applySlashChoice,
    applyAtChoice,
    loadMoreSlash,
    loadMoreAt,
  };
}
