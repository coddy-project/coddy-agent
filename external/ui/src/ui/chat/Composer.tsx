import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import { createPortal } from "react-dom";
import type { TokenUsage } from "./types";
import { WorkspaceChips } from "./WorkspaceChips";
import { EnvironmentChip } from "./EnvironmentChip";
import type { WorkspaceContext } from "./workspaceContext";
import {
  ContextBreakdownPopover,
  type ContextBreakdown,
} from "./ContextBreakdownPopover";
import { ContextUsageRing } from "./ContextUsageRing";
import {
  draftExtendsFailedAtPrefix,
  atMenuDraftAtCaret,
} from "../skills/draftAt";
import {
  draftExtendsFailedSlashPrefix,
  slashMenuDraftAtCaret,
} from "../skills/draftSlash";
import { segmentComposerMirrorSpans } from "../skills/composerMirrorSegments";
import { workspacePickRowSubtitle } from "../skills/workspacePickRowSubtitle";
import {
  pickerRowFromRecent,
  readWorkspaceAtRecents,
  recordWorkspaceAtRecent,
  WORKSPACE_AT_RECENTS_NO_SESSION_KEY,
} from "../skills/workspaceAtRecents";
import {
  shellStackMaxWidthMediaQuery,
  subscribeShellStack,
  snapshotShellStack,
  serverSnapshotShellStack,
} from "../shellBreakpoint";
import { contextUsagePercent } from "./contextUsage";
import {
  filterLlmModels,
  groupLlmModelsByVendor,
  shouldGroupLlmModels,
  shouldShowLlmFilter,
} from "./llmModelMenu";
import { fileTypeIcon } from "../messages/fileTypeIcon";

function fmtBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / (1024 * 1024)).toFixed(1)} MB`;
}

function clamp01(x: number): number {
  if (!Number.isFinite(x)) return 0;
  if (x < 0) return 0;
  if (x > 1) return 1;
  return x;
}

function fmtInt(n: number | undefined): string {
  if (typeof n !== "number" || !Number.isFinite(n)) return "0";
  return Math.max(0, Math.trunc(n)).toString();
}

/** Short label for **`models[].model`** ids (Coddy profile IDs use displayMode elsewhere). */
function displayLlmId(id: string): string {
  const m = id || "";
  const i = m.lastIndexOf("/");
  if (i >= 0 && i < m.length - 1) {
    return m.slice(i + 1);
  }
  return m || "Model";
}

type SlashRow = { name: string; description: string };

type WorkspaceFileRow = { name: string; path_rel: string; kind: string };

/** Floating slash menu anchored to **`composer-field-wrap`** (viewport-relative). */
type PickerFloatRect = {
  left: number;
  width: number;
  bottom: number;
  maxH: number;
};

export function Composer(props: {
  value: string;
  isEmpty: boolean;
  /** Empty-state composer refocuses when this increments (e.g. each New Chat). */
  focusEpoch?: number;
  /** When set, slash command requests send X-Coddy-Session-ID for cwd-scoped skills. */
  sessionId?: string;
  mode: string;
  modes: string[];
  /** Configured backends (`owned_by` != **`coddy`**). Omitted when empty. */
  llmModels?: string[];
  /** Selected **`models[].model`** id (`metadata.model` on profile requests). */
  llmModel?: string;
  onLlmModelChange?: (modelId: string) => void;
  /** Whether the currently selected model accepts image/file inputs. */
  llmModelMultimodal?: boolean;
  /** Reasoning levels offered by the current model; empty/omitted hides the selector. */
  llmReasoningLevels?: string[];
  /** Selected reasoning level (`metadata.reasoning`). */
  llmReasoning?: string;
  onLlmReasoningChange?: (level: string) => void;
  /** Files carried over from the message being edited — shown as read-only chips. */
  editingFiles?: { name: string; mimeType: string }[];
  /** Pristine home (no session). Ring stays empty; tooltip does not imply usage. */
  contextIdle?: boolean;
  tokenUsage?: TokenUsage | null;
  contextPct?: number;
  maxContextTokens?: number;
  contextBreakdown?: ContextBreakdown | null;
  /** Fired when the user opens the context breakdown popover (refresh stats). */
  onContextRingOpen?: () => void;
  /** Known skill names from the catalog — chips confirmed `/name` tokens in the mirror overlay. */
  knownSkillNames?: Set<string>;
  onModeChange: (mode: string) => void;
  onChange: (v: string) => void;
  /** files is non-empty only when the user attached files via the file picker. */
  onSend: (text: string, files?: File[]) => void;
  generating?: boolean;
  onStop?: () => void;
  /** Workspace context chips (folder / branch / worktree) above the field. */
  workspaceCtx?: WorkspaceContext | null;
  worktreePref?: boolean;
  /** The workspace is chosen once: locked as soon as the conversation starts. */
  workspaceLocked?: boolean;
  onWorkspacePickFolder?: (path: string) => void;
  onWorkspacePickBranch?: (branch: string, worktree: boolean) => void;
  onWorktreeToggle?: () => void;
}) {
  const idleSendDisabled = props.value.trim() === "";
  const isMobileShell = useSyncExternalStore(
    subscribeShellStack,
    snapshotShellStack,
    serverSnapshotShellStack,
  );
  const [menuOpen, setMenuOpen] = useState<"mode" | "llm" | "reasoning" | null>(
    null,
  );
  /** Screen rect of the open trigger, so the portaled menu (frosted glass over chat) can anchor to it. */
  const [menuAnchorRect, setMenuAnchorRect] = useState<DOMRect | null>(null);
  /** Live query for the model menu filter (only meaningful while `menuOpen === "llm"`). */
  const [llmQuery, setLlmQuery] = useState("");
  const llmFilterRef = useRef<HTMLInputElement | null>(null);
  const [contextPopoverOpen, setContextPopoverOpen] = useState(false);
  /** After closing the breakdown, hide hover tooltip until pointer leaves the ring. */
  const [contextTipSuppressed, setContextTipSuppressed] = useState(false);

  const taRef = useRef<HTMLTextAreaElement | null>(null);
  const fileInputRef = useRef<HTMLInputElement | null>(null);
  const composerFieldWrapRef = useRef<HTMLDivElement | null>(null);
  const composerCardRef = useRef<HTMLDivElement | null>(null);
  const contextHostRef = useRef<HTMLDivElement | null>(null);
  const mirrorInnerRef = useRef<HTMLDivElement | null>(null);
  const [attachedFiles, setAttachedFiles] = useState<File[]>([]);
  const [composerScrollTop, setComposerScrollTop] = useState(0);
  /** Bump when the slash draft changes or is dismissed so stale list responses are ignored. */
  const slashFetchGenRef = useRef(0);
  const [slashItems, setSlashItems] = useState<SlashRow[]>([]);
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
  const [pickerFloatRect, setPickerFloatRect] =
    useState<PickerFloatRect | null>(null);
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
  const [caretPos, setCaretPos] = useState(0);
  /** Stacked-shell viewports (`max-width`) use a bottom sheet so the picker is not clipped off-screen. */
  const [pickerUseSheet, setPickerUseSheet] = useState(() => {
    if (typeof window === "undefined") {
      return false;
    }
    return window.matchMedia(shellStackMaxWidthMediaQuery).matches;
  });
  const [sheetBottomPx, setSheetBottomPx] = useState<number | null>(null);

  const focusEpoch = props.focusEpoch ?? 0;
  /** Tracks session id for docked composer so switching chats in History refocuses input. */
  const sessionFocusRef = useRef<string | null>(null);

  useLayoutEffect(() => {
    if (!props.isEmpty) {
      return;
    }
    const el = taRef.current;
    if (!el) {
      return;
    }
    el.focus();
  }, [props.isEmpty, focusEpoch, props.sessionId]);

  useLayoutEffect(() => {
    if (props.isEmpty) {
      sessionFocusRef.current = null;
      return;
    }
    const sid = (props.sessionId || "").trim();
    if (!sid) {
      return;
    }
    const prev = sessionFocusRef.current;
    if (prev === sid) {
      return;
    }
    sessionFocusRef.current = sid;
    const el = taRef.current;
    if (!el) {
      return;
    }
    el.focus();
  }, [props.isEmpty, props.sessionId]);

  const pickerOpen = slashOpen || atOpen;
  const sheetOverlayOpen = pickerOpen || contextPopoverOpen;

  const measureSheetBottom = useCallback(() => {
    if (typeof window === "undefined") {
      return;
    }
    const useSheet = window.matchMedia(shellStackMaxWidthMediaQuery).matches;
    if (!useSheet) {
      setSheetBottomPx(null);
      return;
    }
    if (props.isEmpty) {
      setSheetBottomPx(0);
      return;
    }
    const el =
      composerCardRef.current ??
      document.querySelector<HTMLElement>(
        ".composer-wrap-docked .composer-card",
      );
    if (!el) {
      setSheetBottomPx(null);
      return;
    }
    const r = el.getBoundingClientRect();
    setSheetBottomPx(Math.max(0, Math.round(window.innerHeight - r.top + 8)));
  }, [props.isEmpty]);

  useEffect(() => {
    if (typeof window === "undefined") {
      return;
    }
    const mq = window.matchMedia(shellStackMaxWidthMediaQuery);
    const sync = () => setPickerUseSheet(mq.matches);
    sync();
    mq.addEventListener("change", sync);
    return () => mq.removeEventListener("change", sync);
  }, []);

  useLayoutEffect(() => {
    if (!sheetOverlayOpen) {
      setSheetBottomPx(null);
      return;
    }
    if (typeof window !== "undefined") {
      setPickerUseSheet(
        window.matchMedia(shellStackMaxWidthMediaQuery).matches,
      );
    }
    measureSheetBottom();
    window.addEventListener("resize", measureSheetBottom);
    window.addEventListener("scroll", measureSheetBottom, { passive: true });
    const card =
      composerCardRef.current ??
      document.querySelector<HTMLElement>(
        ".composer-wrap-docked .composer-card",
      );
    const ro =
      typeof ResizeObserver !== "undefined" && card
        ? new ResizeObserver(() => measureSheetBottom())
        : null;
    if (card) {
      ro?.observe(card);
    }
    return () => {
      window.removeEventListener("resize", measureSheetBottom);
      window.removeEventListener("scroll", measureSheetBottom);
      ro?.disconnect();
    };
  }, [sheetOverlayOpen, measureSheetBottom]);

  const closeContextPopover = useCallback(() => {
    setContextPopoverOpen(false);
    setContextTipSuppressed(true);
    contextHostRef.current?.blur();
  }, []);

  useEffect(() => {
    if (pickerOpen && contextPopoverOpen) {
      closeContextPopover();
    }
  }, [pickerOpen, contextPopoverOpen, closeContextPopover]);
  const measurePickerFloat = useCallback(() => {
    if (!pickerOpen) {
      setPickerFloatRect(null);
      return;
    }
    if (pickerUseSheet) {
      setPickerFloatRect(null);
      return;
    }
    const el = composerFieldWrapRef.current;
    if (!el) {
      setPickerFloatRect(null);
      return;
    }
    const r = el.getBoundingClientRect();
    if (r.width < 8) {
      setPickerFloatRect(null);
      return;
    }
    const maxH = Math.min(260, Math.round(window.innerHeight * 0.42));
    setPickerFloatRect({
      left: r.left,
      width: r.width,
      bottom: window.innerHeight - r.top + 8,
      maxH,
    });
  }, [pickerOpen, pickerUseSheet]);

  useLayoutEffect(() => {
    if (!pickerOpen) {
      setPickerFloatRect(null);
      return;
    }
    if (pickerUseSheet) {
      setPickerFloatRect(null);
      return;
    }
    measurePickerFloat();
    const el = composerFieldWrapRef.current;
    let ro: ResizeObserver | null = null;
    if (typeof ResizeObserver !== "undefined" && el) {
      ro = new ResizeObserver(() => measurePickerFloat());
      ro.observe(el);
    }
    window.addEventListener("resize", measurePickerFloat);
    const onMsgs = () => measurePickerFloat();
    const shellMobile =
      typeof document !== "undefined" &&
      window.matchMedia(shellStackMaxWidthMediaQuery).matches;
    if (shellMobile) {
      window.addEventListener("scroll", onMsgs, { passive: true });
    } else {
      const msgEl =
        typeof document !== "undefined"
          ? document.getElementById("messages")
          : null;
      msgEl?.addEventListener("scroll", onMsgs, { passive: true });
    }
    return () => {
      ro?.disconnect();
      window.removeEventListener("resize", measurePickerFloat);
      if (shellMobile) {
        window.removeEventListener("scroll", onMsgs);
      } else {
        const msgEl =
          typeof document !== "undefined"
            ? document.getElementById("messages")
            : null;
        msgEl?.removeEventListener("scroll", onMsgs);
      }
    };
  }, [pickerOpen, pickerUseSheet, measurePickerFloat, props.isEmpty]);

  useEffect(() => {
    if (typeof window === "undefined") {
      return;
    }
    const mq = window.matchMedia(shellStackMaxWidthMediaQuery);
    const sync = () => setPickerUseSheet(mq.matches);
    mq.addEventListener("change", sync);
    return () => mq.removeEventListener("change", sync);
  }, []);

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
    async (prefix: string, page: number) => {
      const sp = new URLSearchParams({
        page: String(page),
        page_size: "30",
      });
      if (prefix) {
        sp.set("prefix", prefix);
      }
      const headers: Record<string, string> = {};
      const sid = (props.sessionId || "").trim();
      if (sid) {
        headers["X-Coddy-Session-ID"] = sid;
      }
      const res = await fetch(`/coddy/slash-commands?${sp.toString()}`, {
        headers,
      });
      if (!res.ok) {
        throw new Error(`HTTP ${res.status}`);
      }
      return (await res.json()) as {
        items: SlashRow[];
        has_more: boolean;
        page: number;
      };
    },
    [props.sessionId],
  );

  const fetchAtPage = useCallback(
    async (prefix: string, page: number) => {
      const sp = new URLSearchParams({
        page: String(page),
        page_size: "10",
        prefix,
        dirs: "true",
      });
      const headers: Record<string, string> = {};
      const sid = (props.sessionId || "").trim();
      if (sid) {
        headers["X-Coddy-Session-ID"] = sid;
      }
      const res = await fetch(`/coddy/workspace/files?${sp.toString()}`, {
        headers,
      });
      if (!res.ok) {
        throw new Error(`HTTP ${res.status}`);
      }
      return (await res.json()) as {
        items: WorkspaceFileRow[];
        has_more: boolean;
      };
    },
    [props.sessionId],
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
            setSlashNoMatch({ slashIdx: after.slashIdx, prefix: after.prefix });
            setSlashOpen(false);
            setSlashReplace(null);
          } else {
            setSlashNoMatch(null);
          }
        } catch (e) {
          if (gen !== slashFetchGenRef.current) {
            return;
          }
          setSlashErr(e instanceof Error ? e.message : "request failed");
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
    [fetchSlashPage, slashNoMatch],
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
          (props.sessionId || "").trim() || WORKSPACE_AT_RECENTS_NO_SESSION_KEY;
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
          setAtErr(e instanceof Error ? e.message : "request failed");
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
    [fetchAtPage, atNoMatch, props.sessionId],
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

  const maskComposerText = props.value.length > 0;
  const composerSegments = useMemo(
    () =>
      segmentComposerMirrorSpans(
        props.value,
        caretPos,
        slashNoMatch,
        atNoMatch,
        props.knownSkillNames,
      ),
    [props.value, caretPos, slashNoMatch, atNoMatch, props.knownSkillNames],
  );

  useLayoutEffect(() => {
    const el = taRef.current;
    if (!el) {
      return;
    }
    setCaretPos(el.selectionStart ?? el.value.length);
  }, [props.value]);

  const adjustMirrorToTextarea = useCallback(() => {
    const ta = taRef.current;
    const inner = mirrorInnerRef.current;
    if (!ta || !inner) {
      return;
    }
    const sw = Math.max(0, ta.offsetWidth - ta.clientWidth);
    inner.style.paddingRight = `${16 + sw}px`;
    inner.style.minHeight = `${Math.max(ta.clientHeight, ta.scrollHeight)}px`;
    setComposerScrollTop(ta.scrollTop);
  }, []);

  useLayoutEffect(() => {
    if (!maskComposerText) {
      setComposerScrollTop(0);
      return;
    }
    adjustMirrorToTextarea();
  }, [props.value, maskComposerText, props.isEmpty, adjustMirrorToTextarea]);

  useEffect(() => {
    if (!maskComposerText) {
      return;
    }
    const ta = taRef.current;
    if (!ta) {
      return;
    }
    const ro = new ResizeObserver(() => adjustMirrorToTextarea());
    ro.observe(ta);
    return () => ro.disconnect();
  }, [maskComposerText, adjustMirrorToTextarea]);

  function syncComposerScroll() {
    const ta = taRef.current;
    if (!ta || !maskComposerText) {
      return;
    }
    setComposerScrollTop(ta.scrollTop);
  }

  const applySlashChoice = (name: string) => {
    if (!slashReplace) {
      return;
    }
    const { from, to } = slashReplace;
    const insert = `/${name} `;
    const next = props.value.slice(0, from) + insert + props.value.slice(to);
    props.onChange(next);
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
    const next = props.value.slice(0, from) + insert + props.value.slice(to);
    props.onChange(next);
    recordWorkspaceAtRecent(
      (props.sessionId || "").trim() || WORKSPACE_AT_RECENTS_NO_SESSION_KEY,
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
        setSlashErr(e instanceof Error ? e.message : "request failed");
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
        setAtErr(e instanceof Error ? e.message : "request failed");
      } finally {
        setAtLoading(false);
      }
    })();
  };

  const llmList = props.llmModels ?? [];
  const showLlm = llmList.length > 0;
  const llmVal = (props.llmModel || "").trim();
  // Filter input appears once the backend list is long; vendor grouping kicks
  // in whenever more than one vendor is configured. See llmModelMenu.ts.
  const llmShowFilter = shouldShowLlmFilter(llmList.length);
  const llmFiltered = useMemo(
    () => filterLlmModels(llmList, llmQuery),
    [llmList, llmQuery],
  );
  const llmGrouped = shouldGroupLlmModels(llmList);
  const llmGroups = useMemo(
    () => groupLlmModelsByVendor(llmFiltered),
    [llmFiltered],
  );
  function renderLlmItem(mid: string) {
    return (
      <button
        key={mid}
        type="button"
        role="menuitem"
        title={mid}
        className={`mode-item ${mid === llmVal ? "is-selected" : ""}`}
        onClick={() => {
          props.onLlmModelChange?.(mid);
          closeMenu();
        }}
      >
        {displayLlmId(mid)}
      </button>
    );
  }

  const reasoningLevels = props.llmReasoningLevels ?? [];
  const showReasoning =
    reasoningLevels.length > 0 && !!props.onLlmReasoningChange;
  const reasoningVal = (props.llmReasoning || "").trim();
  const reasoningLabel = reasoningVal
    ? reasoningVal.slice(0, 1).toUpperCase() + reasoningVal.slice(1)
    : "Reasoning";

  function displayMode(id: string): string {
    const m = id || "agent";
    if (m === "plan" || m === "agent") {
      return m.slice(0, 1).toUpperCase() + m.slice(1);
    }
    const i = m.lastIndexOf("/");
    if (i >= 0 && i < m.length - 1) {
      return m.slice(i + 1);
    }
    return m;
  }
  const modeLabel = displayMode(props.mode || "agent");
  const llmLabel = llmVal ? displayLlmId(llmVal) : "Model";
  const contextIdle = props.contextIdle === true;
  const maxCtx =
    typeof props.maxContextTokens === "number" && props.maxContextTokens > 0
      ? props.maxContextTokens
      : 128000;
  const pctRaw =
    props.contextBreakdown != null
      ? contextUsagePercent(maxCtx, props.contextBreakdown)
      : typeof props.contextPct === "number"
        ? props.contextPct
        : null;
  const pct = contextIdle ? null : pctRaw;
  const pct01 = contextIdle
    ? 0
    : clamp01(typeof pct === "number" ? pct / 100 : 0);
  const usage = contextIdle ? null : props.tokenUsage || null;
  const modeMenuDirClass = props.isEmpty ? "opens-down" : "opens-up";
  // On narrow/mobile shells the mode/model/reasoning menus render as a
  // full-width bottom sheet (same family as the slash/at picker sheet) instead
  // of a cramped anchored dropdown.
  const menuUseSheet = isMobileShell;

  function closeMenu() {
    setMenuOpen(null);
    setMenuAnchorRect(null);
    setLlmQuery("");
  }

  function toggleMenu(
    type: "mode" | "llm" | "reasoning",
    trigger: HTMLElement,
  ) {
    if (menuOpen === type) {
      closeMenu();
    } else {
      setLlmQuery("");
      setMenuAnchorRect(trigger.getBoundingClientRect());
      setMenuOpen(type);
    }
  }
  const tip = contextIdle
    ? ["No context usage yet", `Max context ${fmtInt(maxCtx)}`].join("\n")
    : [
        `${typeof pct === "number" ? pct.toFixed(1) : "0.0"}% context used`,
        usage
          ? `Input ${fmtInt(usage.inputTokens)}\nOutput ${fmtInt(usage.outputTokens)}\nTotal ${fmtInt(usage.totalTokens)}`
          : "",
        `Max context ${fmtInt(maxCtx)}`,
      ]
        .filter(Boolean)
        .join("\n");

  const slashMenuChrome = (
    <>
      <div className="slash-menu-surface" aria-hidden />
      <div
        className="slash-menu-scroll"
        style={{ maxHeight: pickerFloatRect?.maxH }}
      >
        <div className="slash-menu-title">Skills</div>
        {slashLoading && slashItems.length === 0 ? (
          <div className="slash-muted">Loading…</div>
        ) : null}
        {slashErr ? <div className="slash-err">{slashErr}</div> : null}
        {!slashLoading && slashItems.length === 0 && !slashErr ? (
          <div className="slash-muted">No commands</div>
        ) : null}
        <ul className="slash-rows">
          {slashItems.map((row) => (
            <li key={row.name}>
              <button
                type="button"
                role="option"
                className="slash-row-btn"
                data-testid={`slash-command-row-${row.name}`}
                onMouseDown={(e) => {
                  e.preventDefault();
                  applySlashChoice(row.name);
                }}
              >
                <span className="slash-row-line">
                  <span className="slash-row-name">/{row.name}</span>
                  {row.description ? (
                    <>
                      {" "}
                      <span className="slash-row-desc">{row.description}</span>
                    </>
                  ) : null}
                </span>
              </button>
            </li>
          ))}
        </ul>
        {slashHasMore ? (
          <button
            type="button"
            className="slash-load-more"
            disabled={slashLoading}
            data-testid="slash-command-more"
            onMouseDown={(e) => e.preventDefault()}
            onClick={() => loadMoreSlash()}
          >
            {slashLoading ? "Loading…" : "More"}
          </button>
        ) : null}
      </div>
    </>
  );

  const atMenuChrome = (
    <>
      <div className="slash-menu-surface" aria-hidden />
      <div
        className="slash-menu-scroll"
        style={{ maxHeight: pickerFloatRect?.maxH }}
      >
        <div className="slash-menu-title">Workspace files</div>
        {atPrefix.trim() === "" && atItems.length === 0 ? (
          <div className="slash-muted">Type after @ to search</div>
        ) : null}
        {atLoading && atItems.length === 0 && atPrefix.trim() !== "" ? (
          <div className="slash-muted">Loading…</div>
        ) : null}
        {atErr ? <div className="slash-err">{atErr}</div> : null}
        {!atLoading &&
        atItems.length === 0 &&
        !atErr &&
        atPrefix.trim() !== "" ? (
          <div className="slash-muted">No files</div>
        ) : null}
        <ul className="slash-rows">
          {atItems.map((row) => (
            <li key={`${row.kind}:${row.path_rel}`}>
              <button
                type="button"
                role="option"
                className="slash-row-btn"
                data-testid={`workspace-file-row-${row.path_rel.replace(/[^a-zA-Z0-9_-]+/g, "_")}`}
                onMouseDown={(e) => {
                  e.preventDefault();
                  applyAtChoice(row);
                }}
              >
                <span className="slash-row-name">@{row.path_rel}</span>
                <span className="slash-row-desc">
                  {workspacePickRowSubtitle(row)}
                </span>
              </button>
            </li>
          ))}
        </ul>
        {atHasMore ? (
          <button
            type="button"
            className="slash-load-more"
            disabled={atLoading}
            data-testid="workspace-files-more"
            onMouseDown={(e) => e.preventDefault()}
            onClick={() => loadMoreAt()}
          >
            {atLoading ? "Loading…" : "More"}
          </button>
        ) : null}
      </div>
    </>
  );

  return (
    <>
      <footer
        className={[
          "composer-wrap",
          props.isEmpty ? "" : "composer-wrap-docked",
          contextPopoverOpen && pickerUseSheet
            ? "composer-wrap-context-sheet"
            : "",
        ]
          .filter(Boolean)
          .join(" ")}
      >
        <label className="sr-only" htmlFor="composer">
          Message
        </label>
        <div className="composer-card" ref={composerCardRef}>
          <div className="composer-context-row">
            <EnvironmentChip />
            {props.workspaceCtx !== undefined && props.onWorkspacePickFolder ? (
              <WorkspaceChips
                context={props.workspaceCtx ?? null}
                worktreePref={props.worktreePref ?? false}
                onPickFolder={props.onWorkspacePickFolder}
                onPickBranch={props.onWorkspacePickBranch ?? (() => {})}
                onWorktreeToggle={props.onWorktreeToggle ?? (() => {})}
                opensUp={!props.isEmpty}
                locked={props.workspaceLocked ?? false}
              />
            ) : null}
          </div>
          {(props.editingFiles && props.editingFiles.length > 0) ||
          attachedFiles.length > 0 ? (
            <div className="composer-attachments" aria-label="Attached files">
              {(props.editingFiles || []).map((f, idx) => {
                const { svg } = fileTypeIcon(f.mimeType, f.name);
                return (
                  <span
                    key={`ef-${idx}`}
                    className="composer-attachment-chip composer-attachment-chip--locked"
                    title={f.name}
                  >
                    <span
                      className="composer-attachment-chip-icon"
                      aria-hidden="true"
                    >
                      {svg}
                    </span>
                    <span className="composer-attachment-chip-name">
                      {f.name}
                    </span>
                  </span>
                );
              })}
              {attachedFiles.map((f, idx) => {
                const { svg, label } = fileTypeIcon(f.type, f.name);
                const tip = `${f.name}\n${label} · ${fmtBytes(f.size)}`;
                return (
                  <span
                    key={idx}
                    className="composer-attachment-chip"
                    title={tip}
                  >
                    <span
                      className="composer-attachment-chip-icon"
                      aria-hidden="true"
                    >
                      {svg}
                    </span>
                    <span className="composer-attachment-chip-name">
                      {f.name}
                    </span>
                    <button
                      type="button"
                      className="composer-attachment-chip-remove"
                      aria-label={`Remove ${f.name}`}
                      onClick={() =>
                        setAttachedFiles((prev) =>
                          prev.filter((_, i) => i !== idx),
                        )
                      }
                    >
                      ×
                    </button>
                  </span>
                );
              })}
            </div>
          ) : null}
          <div className="composer-field-wrap" ref={composerFieldWrapRef}>
            <div className="composer-stack">
              {maskComposerText ? (
                <div className="composer-mirror" aria-hidden="true">
                  <div
                    ref={mirrorInnerRef}
                    className="composer-mirror-inner"
                    style={{ transform: `translateY(-${composerScrollTop}px)` }}
                  >
                    {composerSegments.map((seg, idx) =>
                      seg.type === "text" ? (
                        <span key={idx}>{seg.value}</span>
                      ) : seg.type === "slash" ? (
                        <span
                          key={idx}
                          className="composer-skill-chip-inline"
                          data-testid="composer-skill-chip"
                          data-skill-name={seg.name}
                        >
                          {seg.literal}
                        </span>
                      ) : (
                        <span
                          key={idx}
                          className="composer-at-chip-inline"
                          data-testid="composer-at-chip"
                          data-path-rel={seg.pathRel}
                        >
                          {seg.literal}
                        </span>
                      ),
                    )}
                  </div>
                </div>
              ) : null}
              <textarea
                ref={taRef}
                id="composer"
                className={maskComposerText ? "composer-ta-masked" : undefined}
                rows={props.isEmpty ? 5 : 2}
                placeholder={
                  props.isEmpty
                    ? "Plan, Build, / for skills, @ for files"
                    : "Add a follow-up"
                }
                autoComplete="off"
                value={props.value}
                onChange={(ev) => {
                  const v = ev.target.value;
                  const caret = ev.target.selectionStart ?? v.length;
                  setCaretPos(caret);
                  props.onChange(v);
                  updatePickerMenus(v, caret);
                }}
                onScroll={() => syncComposerScroll()}
                onKeyUp={(ev) => {
                  const el = taRef.current;
                  if (!el) {
                    return;
                  }
                  setCaretPos(el.selectionStart ?? el.value.length);
                  if (
                    ev.key === "ArrowLeft" ||
                    ev.key === "ArrowRight" ||
                    ev.key === "Home" ||
                    ev.key === "End"
                  ) {
                    updatePickerMenus(props.value, el.selectionStart);
                  }
                }}
                onSelect={() => {
                  const el = taRef.current;
                  if (el) {
                    setCaretPos(el.selectionStart ?? el.value.length);
                    updatePickerMenus(props.value, el.selectionStart);
                    syncComposerScroll();
                  }
                }}
                onClick={() => {
                  const el = taRef.current;
                  if (el) {
                    setCaretPos(el.selectionStart ?? el.value.length);
                    updatePickerMenus(props.value, el.selectionStart);
                    syncComposerScroll();
                  }
                }}
                onKeyDown={(ev) => {
                  if (ev.key === "Escape" && contextPopoverOpen) {
                    ev.preventDefault();
                    closeContextPopover();
                    return;
                  }
                  if (ev.key === "Escape" && (slashOpen || atOpen)) {
                    ev.preventDefault();
                    dismissSlashAtPickers();
                    return;
                  }
                  if (
                    ev.key === "Tab" &&
                    atOpen &&
                    atItems.length > 0 &&
                    !props.generating
                  ) {
                    ev.preventDefault();
                    const row0 = atItems[0];
                    if (row0) {
                      applyAtChoice(row0);
                    }
                    return;
                  }
                  if (
                    ev.key === "Tab" &&
                    slashOpen &&
                    slashItems.length > 0 &&
                    !props.generating
                  ) {
                    ev.preventDefault();
                    const row0 = slashItems[0];
                    if (row0) {
                      applySlashChoice(row0.name);
                    }
                    return;
                  }
                  if (
                    ev.key === "Enter" &&
                    !ev.shiftKey &&
                    atOpen &&
                    atItems.length > 0 &&
                    !props.generating
                  ) {
                    ev.preventDefault();
                    const row0 = atItems[0];
                    if (row0) {
                      applyAtChoice(row0);
                    }
                    return;
                  }
                  if (
                    ev.key === "Enter" &&
                    !ev.shiftKey &&
                    slashOpen &&
                    slashItems.length > 0 &&
                    !props.generating
                  ) {
                    ev.preventDefault();
                    const row0 = slashItems[0];
                    if (row0) {
                      applySlashChoice(row0.name);
                    }
                    return;
                  }
                  if (ev.key === "Enter") {
                    if (isMobileShell) {
                      // On mobile: Enter inserts a newline (browser default). Send is button-only.
                      return;
                    }
                    // Desktop: Shift+Enter = newline (browser default, not intercepted).
                    if (ev.shiftKey) {
                      return;
                    }
                    // Desktop: Enter or Ctrl+Enter = send.
                    ev.preventDefault();
                    if (props.generating) {
                      return;
                    }
                    const txt = props.value.trim();
                    if (!txt) {
                      return;
                    }
                    if (attachedFiles.length > 0) {
                      const files = [...attachedFiles];
                      setAttachedFiles([]);
                      props.onSend(txt, files);
                    } else {
                      props.onSend(txt);
                    }
                  }
                }}
              />
            </div>
          </div>

          <div className="composer-bar">
            <div className="composer-tabs" aria-label="Composer options">
              {props.llmModelMultimodal ? (
                <>
                  <input
                    ref={fileInputRef}
                    type="file"
                    multiple
                    className="sr-only"
                    aria-hidden="true"
                    tabIndex={-1}
                    data-testid="composer-file-input"
                    onChange={(ev) => {
                      const files = ev.target.files;
                      if (!files || files.length === 0) return;
                      setAttachedFiles((prev) => [
                        ...prev,
                        ...Array.from(files),
                      ]);
                      ev.target.value = "";
                    }}
                  />
                  <button
                    type="button"
                    className="composer-tab composer-attach-btn"
                    aria-label="Attach file"
                    title="Attach file"
                    data-testid="composer-attach-btn"
                    onClick={() => fileInputRef.current?.click()}
                  >
                    <svg
                      viewBox="0 0 16 16"
                      fill="none"
                      stroke="currentColor"
                      strokeWidth="1.5"
                      width="14"
                      height="14"
                      aria-hidden="true"
                    >
                      <path
                        d="M13.5 7.5l-6 6A4 4 0 012 8l7-7a2.5 2.5 0 013.5 3.5l-6 6A1 1 0 015 9l5-5"
                        strokeLinecap="round"
                        strokeLinejoin="round"
                      />
                    </svg>
                  </button>
                </>
              ) : null}
              <div className="mode">
                <button
                  type="button"
                  className={`composer-tab mode-btn ${props.mode === "plan" ? "mode-plan" : "mode-agent"}`}
                  aria-label="Mode"
                  title="Mode"
                  aria-haspopup="menu"
                  aria-expanded={menuOpen === "mode"}
                  onClick={(e) => toggleMenu("mode", e.currentTarget)}
                >
                  {modeLabel}
                </button>
              </div>

              {showLlm && props.onLlmModelChange ? (
                <div className="mode">
                  <button
                    type="button"
                    className="composer-tab mode-btn mode-llm"
                    aria-label="Model"
                    title="YAML backend (metadata.model)"
                    aria-haspopup="menu"
                    aria-expanded={menuOpen === "llm"}
                    onClick={(e) => toggleMenu("llm", e.currentTarget)}
                  >
                    {llmLabel}
                  </button>
                </div>
              ) : null}

              {showReasoning ? (
                <div className="mode">
                  <button
                    type="button"
                    className="composer-tab mode-btn mode-reasoning"
                    aria-label="Reasoning level"
                    title="Reasoning level (metadata.reasoning)"
                    aria-haspopup="menu"
                    aria-expanded={menuOpen === "reasoning"}
                    onClick={(e) => toggleMenu("reasoning", e.currentTarget)}
                  >
                    {reasoningLabel}
                  </button>
                </div>
              ) : null}
            </div>

            <div className="composer-bar-actions">
              <div
                className={[
                  "composer-context-tip-host",
                  contextTipSuppressed ? "composer-context-tip-suppressed" : "",
                ]
                  .filter(Boolean)
                  .join(" ")}
                ref={contextHostRef}
                tabIndex={0}
                aria-label="Context usage"
                aria-expanded={contextPopoverOpen}
                data-testid="composer-context-ring-host"
                onMouseLeave={() => setContextTipSuppressed(false)}
                onClick={() => {
                  if (contextPopoverOpen) {
                    closeContextPopover();
                  } else {
                    props.onContextRingOpen?.();
                    setContextPopoverOpen(true);
                  }
                }}
                onKeyDown={(ev) => {
                  if (ev.key === "Enter" || ev.key === " ") {
                    ev.preventDefault();
                    if (contextPopoverOpen) {
                      closeContextPopover();
                    } else {
                      props.onContextRingOpen?.();
                      setContextPopoverOpen(true);
                    }
                  }
                }}
              >
                <ContextUsageRing fill01={pct01} />
                {!contextPopoverOpen && !contextTipSuppressed ? (
                  <span
                    className="rail-tip composer-context-tip"
                    role="tooltip"
                  >
                    {tip}
                  </span>
                ) : null}
              </div>
              <button
                type="button"
                className={[
                  "composer-icon composer-run-icon",
                  props.generating
                    ? "composer-send-stop composer-run-icon--stop"
                    : "composer-send-play composer-run-icon--play",
                ].join(" ")}
                id="btn-send"
                aria-label={props.generating ? "Stop generation" : "Send"}
                disabled={!props.generating && idleSendDisabled}
                onClick={() => {
                  if (props.generating) {
                    props.onStop?.();
                    return;
                  }
                  const txt = props.value.trim();
                  if (!txt) {
                    return;
                  }
                  if (attachedFiles.length > 0) {
                    const files = [...attachedFiles];
                    setAttachedFiles([]);
                    props.onSend(txt, files);
                  } else {
                    props.onSend(txt);
                  }
                }}
              >
                {props.generating ? (
                  <span className="composer-send-glyph" aria-hidden="true">
                    <span className="composer-stop-square" />
                  </span>
                ) : (
                  <span className="composer-send-glyph" aria-hidden="true">
                    <svg
                      viewBox="0 0 12 12"
                      fill="currentColor"
                      width="17"
                      height="17"
                    >
                      <path d="M2 0L11 6L2 12Z" />
                    </svg>
                  </span>
                )}
              </button>
            </div>
          </div>
        </div>
      </footer>
      {contextPopoverOpen ? (
        <ContextBreakdownPopover
          open={contextPopoverOpen}
          onClose={closeContextPopover}
          useSheet={pickerUseSheet}
          composerDocked={!props.isEmpty}
          sheetBottomPx={pickerUseSheet ? sheetBottomPx : null}
          anchorRef={contextHostRef}
          contextIdle={contextIdle}
          contextPct={pct}
          maxContextTokens={maxCtx}
          breakdown={props.contextBreakdown}
        />
      ) : null}
      {menuOpen && (menuUseSheet || menuAnchorRect)
        ? createPortal(
            <>
              <button
                type="button"
                className={`mode-menu-backdrop ${menuUseSheet ? "mode-menu-backdrop--scrim" : ""}`}
                aria-hidden="true"
                tabIndex={-1}
                onMouseDown={(e) => {
                  e.preventDefault();
                  closeMenu();
                }}
              />
              <div
                className={`mode-menu ${menuUseSheet ? "mode-menu--sheet" : `mode-menu--portal ${modeMenuDirClass}`} ${menuOpen === "llm" ? "mode-menu--llm" : ""}`}
                role="menu"
                style={
                  menuUseSheet || !menuAnchorRect
                    ? undefined
                    : modeMenuDirClass === "opens-up"
                      ? {
                          left: menuAnchorRect.left,
                          bottom: window.innerHeight - menuAnchorRect.top + 8,
                        }
                      : {
                          left: menuAnchorRect.left,
                          top: menuAnchorRect.bottom + 8,
                        }
                }
              >
                {menuOpen === "mode"
                  ? props.modes.map((m) => (
                      <button
                        key={m}
                        type="button"
                        role="menuitem"
                        className={`mode-item ${m === props.mode ? "is-selected" : ""}`}
                        onClick={() => {
                          props.onModeChange(m);
                          closeMenu();
                        }}
                      >
                        {displayMode(m)}
                      </button>
                    ))
                  : null}
                {menuOpen === "llm" ? (
                  <>
                    {llmShowFilter ? (
                      <input
                        ref={llmFilterRef}
                        type="text"
                        className="mode-menu-filter"
                        data-testid="model-menu-filter"
                        aria-label="Filter models"
                        placeholder="Filter models…"
                        autoFocus
                        value={llmQuery}
                        onChange={(e) => setLlmQuery(e.target.value)}
                        onKeyDown={(e) => {
                          if (e.key === "Escape") {
                            e.preventDefault();
                            e.stopPropagation();
                            closeMenu();
                          } else if (e.key === "Enter") {
                            e.preventDefault();
                            e.stopPropagation();
                            const first = llmFiltered[0];
                            if (first) {
                              props.onLlmModelChange?.(first);
                              closeMenu();
                            }
                          }
                        }}
                      />
                    ) : null}
                    <div className="mode-menu-scroll">
                      {llmFiltered.length === 0 ? (
                        <div
                          className="mode-menu-empty"
                          data-testid="model-menu-empty"
                        >
                          No models match “{llmQuery.trim()}”
                        </div>
                      ) : llmGrouped ? (
                        llmGroups.map((g) => (
                          <div
                            key={g.vendor || "_"}
                            className="mode-menu-group"
                          >
                            <div className="mode-menu-group-label">
                              {g.vendor || "Other"}
                            </div>
                            {g.models.map((mid) => renderLlmItem(mid))}
                          </div>
                        ))
                      ) : (
                        llmFiltered.map((mid) => renderLlmItem(mid))
                      )}
                    </div>
                  </>
                ) : null}
                {menuOpen === "reasoning"
                  ? reasoningLevels.map((lv) => (
                      <button
                        key={lv}
                        type="button"
                        role="menuitem"
                        title={lv}
                        className={`mode-item ${lv === reasoningVal ? "is-selected" : ""}`}
                        onClick={() => {
                          props.onLlmReasoningChange?.(lv);
                          closeMenu();
                        }}
                      >
                        {lv.slice(0, 1).toUpperCase() + lv.slice(1)}
                      </button>
                    ))
                  : null}
              </div>
            </>,
            document.body,
          )
        : null}
      {pickerOpen
        ? createPortal(
            pickerUseSheet ? (
              <>
                <button
                  type="button"
                  className="slash-sheet-backdrop"
                  aria-label="Close picker"
                  tabIndex={-1}
                  onMouseDown={(e) => {
                    e.preventDefault();
                    dismissSlashAtPickers();
                  }}
                />
                <div
                  className={[
                    "slash-menu slash-menu--sheet",
                    !props.isEmpty ? "slash-menu--above-composer" : "",
                  ]
                    .filter(Boolean)
                    .join(" ")}
                  data-testid={
                    atOpen ? "workspace-files-menu" : "slash-command-menu"
                  }
                  role="listbox"
                  aria-label={atOpen ? "Workspace files" : "Slash commands"}
                  style={
                    !props.isEmpty && sheetBottomPx != null
                      ? {
                          bottom: sheetBottomPx,
                          ["--context-sheet-bottom" as string]: `${sheetBottomPx}px`,
                        }
                      : undefined
                  }
                >
                  {atOpen ? atMenuChrome : slashMenuChrome}
                </div>
              </>
            ) : pickerFloatRect ? (
              <div
                className="slash-menu slash-menu--portal"
                data-testid={
                  atOpen ? "workspace-files-menu" : "slash-command-menu"
                }
                role="listbox"
                aria-label={atOpen ? "Workspace files" : "Slash commands"}
                style={{
                  left: pickerFloatRect.left,
                  width: pickerFloatRect.width,
                  bottom: pickerFloatRect.bottom,
                }}
              >
                {atOpen ? atMenuChrome : slashMenuChrome}
              </div>
            ) : null,
            document.body,
          )
        : null}
    </>
  );
}
