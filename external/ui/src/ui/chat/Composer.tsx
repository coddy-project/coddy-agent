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
import { segmentComposerMirrorSpans } from "../skills/composerMirrorSegments";
import {
  subscribeShellStack,
  snapshotShellStack,
  serverSnapshotShellStack,
} from "../shellBreakpoint";
import { contextUsagePercent } from "./contextUsage";
import { fileTypeIcon } from "../messages/fileTypeIcon";
import { AttachedFileChip } from "./AttachedFileChip";
import {
  clipboardImageFiles,
  renamePastedImages,
} from "./composerAttachments";
import {
  clamp01,
  displayLlmId,
  displayModeLabel,
  fmtInt,
} from "./composerLabels";
import { ComposerModeMenus } from "./ComposerModeMenus";
import { SlashAtPickerMenus } from "./SlashAtPickerMenus";
import { useComposerSheetLayout } from "./useComposerSheetLayout";
import { useEnhancePrompt } from "./useEnhancePrompt";
import { useSlashAtPickers } from "./useSlashAtPickers";

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
  const {
    enhancing,
    enhanceErr,
    clearEnhanceState,
    enhancePrompt,
    restorePreEnhanceDraft,
  } = useEnhancePrompt({
    value: props.value,
    onChange: props.onChange,
    sessionId: props.sessionId || "",
    generating: props.generating === true,
    taRef,
  });
  const picker = useSlashAtPickers({
    sessionId: props.sessionId || "",
    value: props.value,
    onChange: props.onChange,
    taRef,
  });
  const {
    slashOpen,
    atOpen,
    pickerOpen,
    slashItems,
    commandMatches,
    slashRows,
    slashActiveIdx,
    setSlashActive,
    atItems,
    slashNoMatch,
    atNoMatch,
    updatePickerMenus,
    dismissSlashAtPickers,
    applySlashChoice,
    applyAtChoice,
  } = picker;
  const [caretPos, setCaretPos] = useState(0);

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

  const sheetOverlayOpen = pickerOpen || contextPopoverOpen;

  const { pickerUseSheet, sheetBottomPx, pickerFloatRect } =
    useComposerSheetLayout({
      isEmpty: props.isEmpty,
      pickerOpen,
      sheetOverlayOpen,
      composerCardRef,
      composerFieldWrapRef,
    });

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

  const llmList = props.llmModels ?? [];
  const showLlm = llmList.length > 0;
  const llmVal = (props.llmModel || "").trim();

  const reasoningLevels = props.llmReasoningLevels ?? [];
  const showReasoning =
    reasoningLevels.length > 0 && !!props.onLlmReasoningChange;
  const reasoningVal = (props.llmReasoning || "").trim();
  const reasoningLabel = reasoningVal
    ? reasoningVal.slice(0, 1).toUpperCase() + reasoningVal.slice(1)
    : t("composer.reasoning");

  const modeLabel = displayModeLabel(props.mode || "agent", t);
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
  }

  function toggleMenu(
    type: "mode" | "llm" | "reasoning",
    trigger: HTMLElement,
  ) {
    if (menuOpen === type) {
      closeMenu();
    } else {
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
                  clearEnhanceState();
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
                    !ev.shiftKey
                  ) {
                    const restored = restorePreEnhanceDraft();
                    if (restored !== null) {
                      ev.preventDefault();
                      props.onChange(restored);
                      return;
                    }
                  }
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
      {menuOpen ? (
        <ComposerModeMenus
          menuOpen={menuOpen}
          menuAnchorRect={menuAnchorRect}
          menuUseSheet={menuUseSheet}
          modeMenuDirClass={modeMenuDirClass}
          modes={props.modes}
          mode={props.mode}
          onModeChange={props.onModeChange}
          llmModels={llmList}
          llmModel={llmVal}
          onLlmModelChange={props.onLlmModelChange}
          reasoningLevels={reasoningLevels}
          reasoning={reasoningVal}
          onLlmReasoningChange={props.onLlmReasoningChange}
          onClose={closeMenu}
        />
      ) : null}
      <SlashAtPickerMenus
        picker={picker}
        pickerUseSheet={pickerUseSheet}
        pickerFloatRect={pickerFloatRect}
        sheetBottomPx={sheetBottomPx}
        isEmpty={props.isEmpty}
      />
    </>
  );
}
