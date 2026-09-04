import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import type { Dispatch, SetStateAction } from "react";
import { createPortal } from "react-dom";
import type { TokenUsage } from "./types";
import { WorkspaceChips } from "./WorkspaceChips";
import { useT } from "../i18n/I18nProvider";
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
  atRangeDraftAtCaret,
  highlightedRange,
  replaceAtRangeSuffix,
  type AtRangeDraft,
} from "../skills/draftAtRange";
import {
  draftExtendsFailedSlashPrefix,
  slashMenuDraftAtCaret,
} from "../skills/draftSlash";
import { filterCommandRows } from "../skills/commandRows";
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

function fmtBytes(
  n: number,
  t: (key: string, params?: Record<string, string | number>) => string,
): string {
  if (n < 1024) return t("composer.bytesB", { n });
  if (n < 1024 * 1024) {
    return t("composer.bytesKB", { n: (n / 1024).toFixed(1) });
  }
  return t("composer.bytesMB", { n: (n / (1024 * 1024)).toFixed(1) });
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
function displayLlmId(id: string, fallback: string = "Model"): string {
  const m = id || "";
  const i = m.lastIndexOf("/");
  if (i >= 0 && i < m.length - 1) {
    return m.slice(i + 1);
  }
  return m || fallback;
}

/** File extension for an image MIME type ("image/svg+xml" -> "svg"). */
function imageExtFromMime(mime: string): string {
  const wellKnown: Record<string, string> = {
    png: "png",
    jpeg: "jpg",
    gif: "gif",
    webp: "webp",
    "svg+xml": "svg",
    bmp: "bmp",
  };
  const sub = (mime.split("/")[1] || "").toLowerCase();
  return wellKnown[sub] || sub.replace(/[^a-z0-9]/g, "") || "png";
}

/** Image files carried by a clipboard **`DataTransfer`** (`kind === "file"` + `image/*` MIME). */
function clipboardImageFiles(data: DataTransfer | null | undefined): File[] {
  if (!data || !data.items) return [];
  const out: File[] = [];
  for (const item of Array.from(data.items)) {
    if (item.kind !== "file" || !item.type.startsWith("image/")) continue;
    const f = item.getAsFile();
    if (f) out.push(f);
  }
  return out;
}

/**
 * Browsers name every clipboard image "image.png"; give pasted files
 * deterministic per-composer names so chips and session history stay unambiguous.
 */
function renamePastedImages(
  files: File[],
  seqRef: { current: number },
): File[] {
  return files.map((f) => {
    seqRef.current += 1;
    const name = `pasted-${seqRef.current}.${imageExtFromMime(f.type)}`;
    return new File([f], name, {
      type: f.type || "image/png",
      lastModified: f.lastModified,
    });
  });
}

/** Local preview URL for an attached image; revoked when the file changes or the chip unmounts. */
function useImageObjectUrl(file: File): string | null {
  const url = useMemo(() => {
    if (!file.type.startsWith("image/")) return null;
    if (
      typeof URL === "undefined" ||
      typeof URL.createObjectURL !== "function"
    ) {
      return null;
    }
    return URL.createObjectURL(file);
  }, [file]);
  useEffect(() => {
    if (!url) return;
    return () => {
      if (
        typeof URL !== "undefined" &&
        typeof URL.revokeObjectURL === "function"
      ) {
        URL.revokeObjectURL(url);
      }
    };
  }, [url]);
  return url;
}

/** Live attachment chip; image files render a thumbnail instead of the generic icon. */
function AttachedFileChip({
  file,
  disabled,
  onRemove,
}: {
  file: File;
  disabled: boolean;
  onRemove: () => void;
}) {
  const { t } = useT();
  const { svg, label } = fileTypeIcon(file.type, file.name);
  const thumbUrl = useImageObjectUrl(file);
  const tip = t("composer.attachmentTooltip", {
    fileName: file.name,
    label,
    size: fmtBytes(file.size, t),
  });
  return (
    <span
      className={[
        "composer-attachment-chip",
        thumbUrl ? "composer-attachment-chip--image" : "",
        disabled ? "composer-attachment-chip--disabled" : "",
      ]
        .filter(Boolean)
        .join(" ")}
      title={tip}
      aria-disabled={disabled ? "true" : undefined}
      data-testid="composer-attachment-chip"
    >
      <span className="composer-attachment-chip-icon" aria-hidden="true">
        {thumbUrl ? (
          <img
            className="composer-attachment-thumb"
            src={thumbUrl}
            alt=""
            data-testid="composer-attachment-thumb"
          />
        ) : (
          svg
        )}
      </span>
      <span className="composer-attachment-chip-name">{file.name}</span>
      <button
        type="button"
        className="composer-attachment-chip-remove"
        aria-label={t("composer.removeAttachment", { fileName: file.name })}
        onClick={onRemove}
      >
        ×
      </button>
    </span>
  );
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

// Outline treatment per session mode (styles.css .composer-tab.mode-*); anything
// unknown falls back to the agent look.
const MODE_TAB_CLASS: Record<string, string> = {
  agent: "mode-agent",
  plan: "mode-plan",
  ask: "mode-ask",
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
  /** Optional shared attachment state for composer layout transitions. */
  attachedFiles?: File[];
  onAttachedFilesChange?: Dispatch<SetStateAction<File[]>>;
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
  const { t } = useT();
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
  const [localAttachedFiles, setLocalAttachedFiles] = useState<File[]>([]);
  const attachedFiles = props.attachedFiles ?? localAttachedFiles;
  const setAttachedFiles = props.onAttachedFilesChange ?? setLocalAttachedFiles;
  /** Transient reason a paste/drop was rejected (non-multimodal model); auto-clears. */
  const [attachHint, setAttachHint] = useState<string | null>(null);
  /** Files are being dragged over the composer card (drop-target affordance). */
  const [dragOverCard, setDragOverCard] = useState(false);
  const attachHintTimerRef = useRef<number | null>(null);
  /** Names pasted images deterministically ("pasted-1.png"); clipboard files are all "image.png". */
  const pastedSeqRef = useRef(0);
  const attachmentSendingEnabled = props.llmModelMultimodal === true;
  const sendableAttachedFiles = attachmentSendingEnabled ? attachedFiles : [];
  /** Attachment-only send is valid only while the selected model accepts it. */
  const idleSendDisabled =
    props.value.trim() === "" && sendableAttachedFiles.length === 0;
  const showAttachHint = useCallback(() => {
    setAttachHint(t("composer.attachUnsupportedModel"));
    if (attachHintTimerRef.current !== null) {
      window.clearTimeout(attachHintTimerRef.current);
    }
    attachHintTimerRef.current = window.setTimeout(() => {
      setAttachHint(null);
      attachHintTimerRef.current = null;
    }, 4000);
  }, [t]);
  useEffect(
    () => () => {
      if (attachHintTimerRef.current !== null) {
        window.clearTimeout(attachHintTimerRef.current);
      }
    },
    [],
  );
  const [composerScrollTop, setComposerScrollTop] = useState(0);
  /** True while the prompt-improvement request is in flight. */
  const [enhancing, setEnhancing] = useState(false);
  /** Request failure shown without changing the user's draft. */
  const [enhanceErr, setEnhanceErr] = useState<string | null>(null);
  /** Draft saved just before improving it so Ctrl+Z can restore it once. */
  const preEnhanceRef = useRef<string | null>(null);
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
  /**
   * Line-range picker for `@path:N-M`. The colon closes the file picker on its own,
   * and this panel takes its place: it previews the file so the typed digits show
   * what they select, and on desktop the rows are clickable. The composer text
   * stays the only input - the panel writes the suffix back into it.
   */
  const [atRangeDraft, setAtRangeDraft] = useState<AtRangeDraft>({
    open: false,
  });
  const [atRangeFile, setAtRangeFile] = useState<{
    pathRel: string;
    lines: string[];
    totalLines: number;
    truncated: boolean;
  } | null>(null);
  /** Path whose fetch already settled (loaded or failed), so typing digits refetches nothing. */
  const atRangeLoadedPathRef = useRef<string | null>(null);
  const atRangeFetchGenRef = useRef(0);
  /** Row the pointer went down on, while a drag selection is in flight. */
  const atRangeDragAnchorRef = useRef<number | null>(null);
  /** Mention the user dismissed with Escape; suppressed until the draft moves on. */
  const [atRangeSuppressed, setAtRangeSuppressed] = useState<{
    atIdx: number;
    path: string;
  } | null>(null);
  const atRangeListRef = useRef<HTMLDivElement>(null);
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

  // The range panel only counts as open once its file loaded, so a colon typed in
  // prose ("см. 10:30-11:00") never flashes an empty panel.
  const atRangeOpen =
    atRangeDraft.open &&
    atRangeFile != null &&
    atRangeFile.pathRel === atRangeDraft.path;
  const atRangeHighlight = highlightedRange(atRangeDraft);
  const pickerOpen = slashOpen || atOpen || atRangeOpen;
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

    // Remember the dismissed mention so the next digit does not reopen the panel.
    if (atRangeDraft.open) {
      setAtRangeSuppressed({
        atIdx: atRangeDraft.atIdx,
        path: atRangeDraft.path,
      });
    }
    closeAtRangePicker();
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

  // Built-in deterministic commands (/compact, /plugin) are static per config, so
  // fetch them once the first time the slash menu opens and cache the result.
  const fetchCommandsOnce = useCallback(async () => {
    if (commandsFetchedRef.current) {
      return;
    }
    commandsFetchedRef.current = true;
    try {
      const res = await fetch("/coddy/commands");
      if (!res.ok) {
        return;
      }
      const body = (await res.json()) as { items?: SlashRow[] };
      const rows = body.items || [];
      commandItemsRef.current = rows;
      setCommandItems(rows);
    } catch {
      // Built-in commands are optional; ignore fetch errors.
    }
  }, []);

  // Load the built-in commands once on mount so they are ready before the user
  // narrows the slash prefix (avoids racing the skills-zero auto-close).
  useEffect(() => {
    void fetchCommandsOnce();
  }, [fetchCommandsOnce]);

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

  /** Clears the range panel; the composer text is left exactly as typed. */
  const closeAtRangePicker = useCallback(() => {
    atRangeFetchGenRef.current++;
    atRangeDragAnchorRef.current = null;
    atRangeLoadedPathRef.current = null;
    setAtRangeDraft({ open: false });
    setAtRangeFile(null);
  }, []);

  /**
   * Loads the mentioned file once per path. A path that does not resolve simply
   * leaves the panel closed - `@user:1-2` in prose must not pop an empty panel -
   * and is remembered so the next digit does not refetch it.
   */
  const loadAtRangeFile = useCallback(
    async (pathRel: string) => {
      if (atRangeLoadedPathRef.current === pathRel) {
        return;
      }
      atRangeLoadedPathRef.current = pathRel;
      const gen = ++atRangeFetchGenRef.current;
      try {
        const sp = new URLSearchParams({ path_rel: pathRel });
        const headers: Record<string, string> = {};
        const sid = (props.sessionId || "").trim();
        if (sid) {
          headers["X-Coddy-Session-ID"] = sid;
        }
        const res = await fetch(`/coddy/workspace/file?${sp.toString()}`, {
          headers,
        });
        if (!res.ok) {
          throw new Error(`HTTP ${res.status}`);
        }
        const body = (await res.json()) as {
          lines?: string[];
          total_lines?: number;
          truncated?: boolean;
        };
        if (gen !== atRangeFetchGenRef.current) {
          return;
        }
        setAtRangeFile({
          pathRel,
          lines: body.lines || [],
          totalLines: body.total_lines ?? (body.lines || []).length,
          truncated: body.truncated === true,
        });
      } catch {
        if (gen === atRangeFetchGenRef.current) {
          setAtRangeFile(null);
        }
      }
    },
    [props.sessionId],
  );

  const enhancePrompt = useCallback(async () => {
    if (enhancing || props.generating) {
      return;
    }
    const draft = props.value.trim();
    if (!draft) {
      return;
    }
    preEnhanceRef.current = props.value;
    setEnhancing(true);
    setEnhanceErr(null);
    try {
      const headers: Record<string, string> = {
        "Content-Type": "application/json",
      };
      const sessionID = (props.sessionId || "").trim();
      if (sessionID) {
        headers["X-Coddy-Session-ID"] = sessionID;
      }
      const response = await fetch("/coddy/enhance-prompt", {
        method: "POST",
        headers,
        body: JSON.stringify({ text: draft }),
      });
      if (!response.ok) {
        setEnhanceErr(
          response.status === 503
            ? t("composer.enhanceNoModel")
            : t("composer.enhanceFailed"),
        );
        preEnhanceRef.current = null;
        return;
      }
      const body = (await response.json()) as { text?: string };
      const enhanced = (body.text || "").trim();
      if (enhanced) {
        props.onChange(enhanced);
      } else {
        preEnhanceRef.current = null;
      }
    } catch {
      preEnhanceRef.current = null;
      setEnhanceErr(t("composer.enhanceFailed"));
    } finally {
      setEnhancing(false);
      requestAnimationFrame(() => taRef.current?.focus());
    }
  }, [
    enhancing,
    props.generating,
    props.value,
    props.sessionId,
    props.onChange,
    t,
  ]);

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
    [fetchAtPage, atNoMatch, props.sessionId, t],
  );

  const updatePickerMenus = useCallback(
    (value: string, caret: number) => {
      let deferAtDraft = false;
      if (deferAtDraftPickerTicksRef.current > 0) {
        deferAtDraftPickerTicksRef.current -= 1;
        deferAtDraft = true;
      }
      // A ":" after a mention closes the file picker (":" is no MENU_PATH_CHAR);
      // the range panel takes over from there.
      const rd = atRangeDraftAtCaret(value, caret);
      if (rd.open) {
        setAtRangeDraft(rd);
        if (
          atRangeSuppressed == null ||
          atRangeSuppressed.atIdx !== rd.atIdx ||
          atRangeSuppressed.path !== rd.path
        ) {
          void loadAtRangeFile(rd.path);
        }
        bumpSlashFetchGen();
        setSlashOpen(false);
        setSlashReplace(null);
        setSlashNoMatch(null);
        setSlashLoading(false);
        bumpAtFetchGen();
        setAtOpen(false);
        setAtReplace(null);
        setAtNoMatch(null);
        setAtLoading(false);
        return;
      }
      if (atRangeDraft.open) {
        closeAtRangePicker();
        setAtRangeSuppressed(null);
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
    [
      updateAtMenu,
      updateSlashMenu,
      loadAtRangeFile,
      closeAtRangePicker,
      atRangeDraft.open,
      atRangeSuppressed,
    ],
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

  /**
   * Writes a line range picked in the panel back into the composer text, then
   * re-derives the draft so the next drag step measures against the new suffix.
   */
  const applyAtRangeSelection = useCallback(
    (startLine: number, endLine: number) => {
      if (!atRangeDraft.open) {
        return;
      }
      const next = replaceAtRangeSuffix(
        props.value,
        atRangeDraft,
        startLine,
        endLine,
      );
      props.onChange(next.text);
      setAtRangeDraft(atRangeDraftAtCaret(next.text, next.caret));
      requestAnimationFrame(() => {
        const el = taRef.current;
        if (!el) {
          return;
        }
        el.focus();
        el.setSelectionRange(next.caret, next.caret);
      });
    },
    [atRangeDraft, props.value, props.onChange],
  );

  // A drag may end anywhere, so the anchor is cleared from the window, not the row.
  useEffect(() => {
    if (!atRangeOpen) {
      return;
    }
    const onUp = () => {
      atRangeDragAnchorRef.current = null;
    };
    window.addEventListener("mouseup", onUp);
    return () => window.removeEventListener("mouseup", onUp);
  }, [atRangeOpen]);

  const atRangeHighlightStart = atRangeHighlight?.start ?? 0;
  const atRangeHighlightEnd = atRangeHighlight?.end ?? 0;
  // Keep the typed range in view: digits can point far outside the shown window.
  // Start first, then end, so a range that fits shows from its top while a longer
  // one settles on the edge the user is still typing.
  useEffect(() => {
    if (!atRangeOpen || atRangeHighlightStart < 1) {
      return;
    }
    const host = atRangeListRef.current;
    if (!host) {
      return;
    }
    for (const line of [atRangeHighlightStart, atRangeHighlightEnd]) {
      const row = host.querySelector(`[data-line="${line}"]`);
      if (
        row instanceof HTMLElement &&
        typeof row.scrollIntoView === "function"
      ) {
        row.scrollIntoView({ block: "nearest" });
      }
    }
  }, [atRangeOpen, atRangeHighlightStart, atRangeHighlightEnd]);

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
        {displayLlmId(mid, t("composer.model"))}
      </button>
    );
  }

  const reasoningLevels = props.llmReasoningLevels ?? [];
  const showReasoning =
    reasoningLevels.length > 0 && !!props.onLlmReasoningChange;
  const reasoningVal = (props.llmReasoning || "").trim();
  const reasoningLabel = reasoningVal
    ? reasoningVal.slice(0, 1).toUpperCase() + reasoningVal.slice(1)
    : t("composer.reasoning");

  function displayMode(id: string): string {
    const m = id || "agent";
    if (m === "plan") {
      return t("composer.modePlan");
    }
    if (m === "agent") {
      return t("composer.modeAgent");
    }
    if (m === "ask") {
      return t("composer.modeAsk");
    }
    const i = m.lastIndexOf("/");
    if (i >= 0 && i < m.length - 1) {
      return m.slice(i + 1);
    }
    return m;
  }
  const modeLabel = displayMode(props.mode || "agent");
  const llmLabel = llmVal
    ? displayLlmId(llmVal, t("composer.model"))
    : t("composer.model");
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
    ? [
        t("composer.contextTipIdle"),
        t("composer.contextTipMaxContext", { count: fmtInt(maxCtx) }),
      ].join("\n")
    : [
        t("composer.contextTipUsed", {
          percent: typeof pct === "number" ? pct.toFixed(1) : "0.0",
        }),
        usage
          ? [
              t("composer.contextTipInput", {
                count: fmtInt(usage.inputTokens),
              }),
              t("composer.contextTipOutput", {
                count: fmtInt(usage.outputTokens),
              }),
              t("composer.contextTipTotal", {
                count: fmtInt(usage.totalTokens),
              }),
            ].join("\n")
          : "",
        t("composer.contextTipMaxContext", { count: fmtInt(maxCtx) }),
      ]
        .filter(Boolean)
        .join("\n");

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

  const slashMenuChrome = (
    <>
      <div className="slash-menu-surface" aria-hidden />
      <div
        className="slash-menu-scroll"
        style={{ maxHeight: pickerFloatRect?.maxH }}
      >
        {showSkillsSection ? (
          <>
            <div className="slash-menu-title">{t("composer.skillsTitle")}</div>
            {slashLoading && slashItems.length === 0 ? (
              <div className="slash-muted">{t("composer.loading")}</div>
            ) : null}
            {slashErr ? <div className="slash-err">{slashErr}</div> : null}
            {!slashLoading && slashItems.length === 0 && !slashErr ? (
              <div className="slash-muted">
                {t("composer.noMatchingSkills")}
              </div>
            ) : null}
            <ul className="slash-rows">
              {slashItems.map((row, idx) => (
                <li key={row.name}>
                  <button
                    type="button"
                    role="option"
                    aria-selected={idx === slashActiveIdx}
                    className={`slash-row-btn${idx === slashActiveIdx ? " is-active" : ""}`}
                    data-testid={`slash-command-row-${row.name}`}
                    onMouseEnter={() => setSlashActive(idx)}
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
                          <span className="slash-row-desc">
                            {row.description}
                          </span>
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
                {slashLoading ? t("composer.loading") : t("composer.more")}
              </button>
            ) : null}
          </>
        ) : null}
        {commandMatches.length > 0 ? (
          <>
            <div className="slash-menu-title">
              {t("composer.commandsTitle")}
            </div>
            <ul className="slash-rows">
              {commandMatches.map((row, idx) => {
                const gidx = slashItems.length + idx;
                return (
                  <li key={`cmd-${row.name}`}>
                    <button
                      type="button"
                      role="option"
                      aria-selected={gidx === slashActiveIdx}
                      className={`slash-row-btn${gidx === slashActiveIdx ? " is-active" : ""}`}
                      data-testid={`command-row-${row.name}`}
                      onMouseEnter={() => setSlashActive(gidx)}
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
                            <span className="slash-row-desc">
                              {row.description}
                            </span>
                          </>
                        ) : null}
                      </span>
                    </button>
                  </li>
                );
              })}
            </ul>
          </>
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
        <div className="slash-menu-title">
          {t("composer.workspaceFilesTitle")}
        </div>
        {atPrefix.trim() === "" && atItems.length === 0 ? (
          <div className="slash-muted">{t("composer.typeAfterAt")}</div>
        ) : null}
        {atLoading && atItems.length === 0 && atPrefix.trim() !== "" ? (
          <div className="slash-muted">{t("composer.loading")}</div>
        ) : null}
        {atErr ? <div className="slash-err">{atErr}</div> : null}
        {!atLoading &&
        atItems.length === 0 &&
        !atErr &&
        atPrefix.trim() !== "" ? (
          <div className="slash-muted">{t("composer.noFiles")}</div>
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
            {atLoading ? t("composer.loading") : t("composer.more")}
          </button>
        ) : null}
        {atItems.length > 0 ? (
          <div className="slash-muted at-range-menu-hint">
            {t("composer.atRangeMenuHint")}
          </div>
        ) : null}
      </div>
    </>
  );

  const atRangeChrome = (
    <>
      <div className="slash-menu-surface" aria-hidden />
      <div
        className="slash-menu-scroll"
        style={{ maxHeight: pickerFloatRect?.maxH }}
      >
        <div className="slash-menu-title">
          {t("composer.atRangeTitle")}
          <span className="at-range-title-path">{atRangeFile?.pathRel}</span>
          {atRangeHighlight ? (
            <span
              className="at-range-title-range"
              data-testid="at-range-current"
            >
              {atRangeHighlight.start}-{atRangeHighlight.end}
            </span>
          ) : null}
        </div>
        <div className="slash-muted at-range-hint">
          {isMobileShell
            ? t("composer.atRangeHintMobile")
            : t("composer.atRangeHint")}
        </div>
        <div
          className="at-range-lines"
          data-testid="at-range-lines"
          ref={atRangeListRef}
        >
          {(atRangeFile?.lines ?? []).map((line, idx) => {
            const no = idx + 1;
            const selected =
              atRangeHighlight != null &&
              no >= atRangeHighlight.start &&
              no <= atRangeHighlight.end;
            const cls = `at-range-line${selected ? " at-range-line--sel" : ""}`;
            const body = (
              <>
                <span className="at-range-line-no">{no}</span>
                <span className="at-range-line-code">
                  {line === "" ? " " : line}
                </span>
              </>
            );
            // Mobile shells have no mouse to select with: rows are display only
            // and the range is typed as digits.
            return isMobileShell ? (
              <div key={no} className={cls} data-line={no}>
                {body}
              </div>
            ) : (
              <button
                key={no}
                type="button"
                className={cls}
                data-line={no}
                data-testid={`at-range-line-${no}`}
                aria-pressed={selected}
                onMouseDown={(e) => {
                  // Never take focus from the textarea - it holds the draft.
                  e.preventDefault();
                  atRangeDragAnchorRef.current = no;
                  applyAtRangeSelection(no, no);
                }}
                onMouseEnter={() => {
                  const anchor = atRangeDragAnchorRef.current;
                  if (anchor != null) {
                    applyAtRangeSelection(anchor, no);
                  }
                }}
              >
                {body}
              </button>
            );
          })}
        </div>
        {atRangeFile?.truncated ? (
          <div className="slash-muted">
            {t("composer.atRangeTruncated", {
              shown: atRangeFile.lines.length,
              total: atRangeFile.totalLines,
            })}
          </div>
        ) : null}
      </div>
    </>
  );

  const pickerChrome = atRangeOpen
    ? atRangeChrome
    : atOpen
      ? atMenuChrome
      : slashMenuChrome;
  const pickerTestId = atRangeOpen
    ? "at-range-picker"
    : atOpen
      ? "workspace-files-menu"
      : "slash-command-menu";
  const pickerAriaLabel = atRangeOpen
    ? t("composer.atRangeAriaLabel")
    : atOpen
      ? t("composer.workspaceFilesAriaLabel")
      : t("composer.slashCommandsAriaLabel");
  const pickerRole = atRangeOpen ? "group" : "listbox";

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
          {t("composer.messageLabel")}
        </label>
        <div
          className={`composer-card${dragOverCard ? " composer-card--dragover" : ""}`}
          ref={composerCardRef}
          onDragOver={(ev) => {
            const dt = ev.dataTransfer;
            if (!dt || !Array.from(dt.types || []).includes("Files")) {
              return;
            }
            ev.preventDefault();
            setDragOverCard(true);
          }}
          onDragLeave={(ev) => {
            const to = ev.relatedTarget;
            if (to instanceof Node && ev.currentTarget.contains(to)) {
              return;
            }
            setDragOverCard(false);
          }}
          onDrop={(ev) => {
            const files = ev.dataTransfer
              ? Array.from(ev.dataTransfer.files || [])
              : [];
            if (files.length === 0) {
              return;
            }
            ev.preventDefault();
            setDragOverCard(false);
            if (!props.llmModelMultimodal) {
              showAttachHint();
              return;
            }
            setAttachedFiles((prev) => [...prev, ...files]);
          }}
        >
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
            <button
              type="button"
              className="composer-enhance-btn"
              aria-label={t("composer.enhance")}
              title={t("composer.enhance")}
              data-testid="composer-enhance-btn"
              disabled={enhancing || props.generating || idleSendDisabled}
              onClick={() => void enhancePrompt()}
            >
              <svg
                className={
                  enhancing
                    ? "composer-enhance-icon is-spinning"
                    : "composer-enhance-icon"
                }
                viewBox="0 0 16 16"
                fill="currentColor"
                width="12"
                height="12"
                aria-hidden="true"
              >
                <path d="M9.5 1l.7 1.8L12 3.5l-1.8.7L9.5 6l-.7-1.8L7 3.5l1.8-.7L9.5 1zM3.2 5.6l.5 1.2 1.2.5-1.2.5-.5 1.2-.5-1.2L1.5 7.3l1.2-.5.5-1.2zM8.9 6.6a1 1 0 011.5 0l.9.9a1 1 0 010 1.5l-5.3 5.3a1 1 0 01-1.5 0l-.9-.9a1 1 0 010-1.5l5.3-5.3zm.8 1.5l-4.6 4.6.5.5 4.6-4.6-.5-.5z" />
              </svg>
            </button>
          </div>
          {(props.editingFiles && props.editingFiles.length > 0) ||
          attachedFiles.length > 0 ? (
            <div
              className="composer-attachments"
              aria-label={t("composer.attachedFilesAriaLabel")}
            >
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
              {attachedFiles.map((f, idx) => (
                <AttachedFileChip
                  key={idx}
                  file={f}
                  disabled={!attachmentSendingEnabled}
                  onRemove={() =>
                    setAttachedFiles((prev) => prev.filter((_, i) => i !== idx))
                  }
                />
              ))}
            </div>
          ) : null}
          {attachHint ? (
            <div
              className="composer-attach-hint"
              role="status"
              data-testid="composer-attach-hint"
            >
              {attachHint}
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
                    ? t("composer.placeholderEmpty")
                    : t("composer.placeholderFollowUp")
                }
                autoComplete="off"
                value={props.value}
                onChange={(ev) => {
                  const v = ev.target.value;
                  const caret = ev.target.selectionStart ?? v.length;
                  setCaretPos(caret);
                  preEnhanceRef.current = null;
                  setEnhanceErr(null);
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
                onPaste={(ev) => {
                  const images = clipboardImageFiles(ev.clipboardData);
                  if (images.length === 0) {
                    return; // plain-text paste: keep the browser default
                  }
                  ev.preventDefault();
                  if (!props.llmModelMultimodal) {
                    showAttachHint();
                    return;
                  }
                  setAttachedFiles((prev) => [
                    ...prev,
                    ...renamePastedImages(images, pastedSeqRef),
                  ]);
                }}
                onKeyDown={(ev) => {
                  if (
                    ev.key === "z" &&
                    (ev.metaKey || ev.ctrlKey) &&
                    !ev.shiftKey &&
                    preEnhanceRef.current !== null
                  ) {
                    ev.preventDefault();
                    const restored = preEnhanceRef.current;
                    preEnhanceRef.current = null;
                    props.onChange(restored);
                    return;
                  }
                  if (ev.key === "Escape" && contextPopoverOpen) {
                    ev.preventDefault();
                    closeContextPopover();
                    return;
                  }
                  if (
                    ev.key === "Escape" &&
                    (slashOpen || atOpen || atRangeOpen)
                  ) {
                    ev.preventDefault();
                    dismissSlashAtPickers();
                    return;
                  }
                  if (
                    (ev.key === "ArrowDown" || ev.key === "ArrowUp") &&
                    slashOpen &&
                    !atOpen &&
                    slashRows.length > 0 &&
                    !props.generating
                  ) {
                    ev.preventDefault();
                    const len = slashRows.length;
                    setSlashActive((i) => {
                      const cur = Math.min(Math.max(i, 0), len - 1);
                      return ev.key === "ArrowDown"
                        ? (cur + 1) % len
                        : (cur - 1 + len) % len;
                    });
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
                    (slashItems.length > 0 || commandMatches.length > 0) &&
                    !props.generating
                  ) {
                    ev.preventDefault();
                    const pick = slashRows[slashActiveIdx]?.name;
                    if (pick) {
                      applySlashChoice(pick);
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
                    (slashItems.length > 0 || commandMatches.length > 0) &&
                    !props.generating
                  ) {
                    ev.preventDefault();
                    const pick = slashRows[slashActiveIdx]?.name;
                    if (pick) {
                      applySlashChoice(pick);
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
                    if (!txt && sendableAttachedFiles.length === 0) {
                      return;
                    }
                    if (sendableAttachedFiles.length > 0) {
                      const files = [...sendableAttachedFiles];
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

          {enhanceErr ? (
            <div
              className="composer-enhance-err"
              role="status"
              data-testid="composer-enhance-err"
            >
              {enhanceErr}
            </div>
          ) : null}

          <div className="composer-bar">
            <div
              className="composer-tabs"
              aria-label={t("composer.composerOptions")}
            >
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
                    aria-label={t("composer.attachFile")}
                    title={t("composer.attachFile")}
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
                  className={`composer-tab mode-btn ${MODE_TAB_CLASS[props.mode || "agent"] ?? "mode-agent"}`}
                  aria-label={t("composer.mode")}
                  title={t("composer.mode")}
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
                    aria-label={t("composer.model")}
                    title={t("composer.modelTitle")}
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
                    aria-label={t("composer.reasoningLevel")}
                    title={t("composer.reasoningLevelTitle")}
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
                aria-label={t("composer.contextUsage")}
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
                aria-label={
                  props.generating
                    ? t("composer.stopGeneration")
                    : t("composer.send")
                }
                disabled={!props.generating && idleSendDisabled}
                onClick={() => {
                  if (props.generating) {
                    props.onStop?.();
                    return;
                  }
                  const txt = props.value.trim();
                  if (!txt && sendableAttachedFiles.length === 0) {
                    return;
                  }
                  if (sendableAttachedFiles.length > 0) {
                    const files = [...sendableAttachedFiles];
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
                        aria-label={t("composer.filterModels")}
                        placeholder={t("composer.filterModelsPlaceholder")}
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
                          {t("composer.noModelsMatch", {
                            query: llmQuery.trim(),
                          })}
                        </div>
                      ) : llmGrouped ? (
                        llmGroups.map((g) => (
                          <div
                            key={g.vendor || "_"}
                            className="mode-menu-group"
                          >
                            <div className="mode-menu-group-label">
                              {g.vendor || t("composer.vendorOther")}
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
                  aria-label={t("composer.closePicker")}
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
                  data-testid={pickerTestId}
                  role={pickerRole}
                  aria-label={pickerAriaLabel}
                  style={
                    !props.isEmpty && sheetBottomPx != null
                      ? {
                          bottom: sheetBottomPx,
                          ["--context-sheet-bottom" as string]: `${sheetBottomPx}px`,
                        }
                      : undefined
                  }
                >
                  {pickerChrome}
                </div>
              </>
            ) : pickerFloatRect ? (
              <div
                className="slash-menu slash-menu--portal"
                data-testid={pickerTestId}
                role={pickerRole}
                aria-label={pickerAriaLabel}
                style={{
                  left: pickerFloatRect.left,
                  width: pickerFloatRect.width,
                  bottom: pickerFloatRect.bottom,
                }}
              >
                {pickerChrome}
              </div>
            ) : null,
            document.body,
          )
        : null}
    </>
  );
}
