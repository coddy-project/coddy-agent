import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import type { PointerEvent as ReactPointerEvent } from "react";
import { useT } from "../i18n/I18nProvider";
import { useEscapeCloses } from "../components/useEscapeCloses";
import { useRightDockEscape } from "../components/useRightDock";
import {
  serverSnapshotTouchOnly,
  snapshotTouchOnly,
  subscribeTouchOnly,
} from "../shellBreakpoint";
import { IconExpand, IconRestore } from "../files/windowIcons";
import { composerAutoFocusAllowed } from "../chat/composerFocus";
import {
  patchFrame,
  removeFrame,
  rotateFrame,
  type CanvasBoard,
  type CanvasDevice,
  type CanvasFrame as Frame,
} from "./api";
import type { PickedElement } from "./bridgeProtocol";
import {
  boundsOf,
  fitCamera,
  pan,
  toScreen,
  zoomAt,
  zoomPercent,
  type Camera,
  type Rect,
} from "./camera";
import { CanvasFrame, sizeLabel } from "./CanvasFrame";
import { CanvasPromptBar } from "./CanvasPromptBar";
import { composeCanvasPrompt, pickLabel, type CanvasPick } from "./pickPrompt";

/** What the bar holds, per chat, for the life of the page: closing the window
 *  to read the transcript does not lose a half-written request. */
const drafts = new Map<string, { text: string; picks: CanvasPick[] }>();

/** Forgets every draft (tests only). */
export function resetCanvasDraftsForTests(): void {
  drafts.clear();
}

export interface CanvasViewProps {
  sessionId: string;
  board: CanvasBoard | null;
  reloadBoard: () => void;
  /** The frame the address or the agent's last show points at. */
  focusFrameId: string;
  /** Moves whenever the focus is asked again, for the same frame too. */
  focusSeq: number;
  /** The chat's turn is running. */
  busy: boolean;
  onSend: (text: string) => void;
  onStop: () => void;
  onClose: () => void;
}

interface Popover {
  frame: Frame;
  element: PickedElement;
  at: Rect;
}

let pickSeq = 0;

/**
 * The canvas of a chat: a window over the chat, framed like the Files
 * window, holding the board of live frames and a prompt bar. Frames stand at
 * their own size, scaled together by the board's camera; Interact hands the
 * pointer to the pages, Comment picks elements, which become chips of the bar
 * and, sent, a prompt of the chat.
 */
export function CanvasView(props: CanvasViewProps) {
  const { t, tp } = useT();
  const touchOnly = useSyncExternalStore(
    subscribeTouchOnly,
    snapshotTouchOnly,
    serverSnapshotTouchOnly,
  );
  const frames = useMemo(() => props.board?.frames ?? [], [props.board]);
  const boardRef = useRef<HTMLDivElement | null>(null);
  const [viewport, setViewport] = useState({ width: 0, height: 0 });
  const [camera, setCamera] = useState<Camera>({ x: 0, y: 0, scale: 1 });
  const [mode, setMode] = useState<"interact" | "comment">("interact");
  const [expanded, setExpanded] = useState(false);
  const [popover, setPopover] = useState<Popover | null>(null);
  const [note, setNote] = useState("");
  const initial = drafts.get(props.sessionId) ?? { text: "", picks: [] };
  const [text, setText] = useState(initial.text);
  const [picks, setPicks] = useState<CanvasPick[]>(initial.picks);
  const fittedFor = useRef<string>("");
  // Until the person moves or zooms the board, it follows the window: a
  // resize (a phone turned, the window expanded) fits it again.
  const userMoved = useRef(false);
  const fitTarget = useRef<string>("");

  useEffect(() => {
    drafts.set(props.sessionId, { text, picks });
  }, [props.sessionId, text, picks]);

  useLayoutEffect(() => {
    const el = boardRef.current;
    if (!el) return undefined;
    const measure = () =>
      setViewport({ width: el.clientWidth, height: el.clientHeight });
    measure();
    const ro =
      typeof ResizeObserver === "function" ? new ResizeObserver(measure) : null;
    ro?.observe(el);
    return () => ro?.disconnect();
  }, []);

  const fitTo = useCallback(
    (rects: Rect[]) => {
      const b = boundsOf(rects);
      if (!b || viewport.width <= 0 || viewport.height <= 0) return;
      setCamera(fitCamera(b, viewport));
    },
    [viewport],
  );

  // The board is fitted when it first shows frames, and on the frame the
  // address or the agent's last show points at.
  useEffect(() => {
    if (frames.length === 0 || viewport.width <= 0) return;
    const focus = frames.find((f) => f.id === props.focusFrameId);
    const key = focus ? `f:${focus.id}:${props.focusSeq}` : "all";
    if (fittedFor.current === key) return;
    if (!focus && fittedFor.current !== "") return;
    fittedFor.current = key;
    userMoved.current = false;
    fitTarget.current = focus ? focus.id : "";
    fitTo(focus ? [focus] : frames);
  }, [frames, props.focusFrameId, props.focusSeq, viewport.width, fitTo]);

  useEffect(() => {
    if (userMoved.current || frames.length === 0 || viewport.width <= 0) return;
    const focus = frames.find((f) => f.id === fitTarget.current);
    fitTo(focus ? [focus] : frames);
    // Only a new size refits; new frames are the effect above's.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [viewport.width, viewport.height]);

  const fitAll = () => {
    userMoved.current = false;
    fitTarget.current = "";
    fitTo(frames);
  };

  const zoomBy = (factor: number) => {
    userMoved.current = true;
    setCamera((c) =>
      zoomAt(c, factor, { x: viewport.width / 2, y: viewport.height / 2 }),
    );
  };

  // The wheel pans the board, Ctrl or Cmd with it zooms at the pointer; over
  // a page the wheel is the page's.
  useEffect(() => {
    const el = boardRef.current;
    if (!el) return undefined;
    const onWheel = (e: WheelEvent) => {
      e.preventDefault();
      userMoved.current = true;
      const r = el.getBoundingClientRect();
      if (e.ctrlKey || e.metaKey) {
        const factor = Math.exp(-e.deltaY / 400);
        setCamera((c) =>
          zoomAt(c, factor, { x: e.clientX - r.left, y: e.clientY - r.top }),
        );
      } else {
        setCamera((c) => pan(c, -e.deltaX, -e.deltaY));
      }
    };
    el.addEventListener("wheel", onWheel, { passive: false });
    return () => el.removeEventListener("wheel", onWheel);
  }, []);

  // Dragging the empty board pans it; two fingers pinch it.
  const pointers = useRef(new Map<number, { x: number; y: number }>());
  const pinch = useRef<{ dist: number } | null>(null);
  const onBoardDown = (e: ReactPointerEvent<HTMLDivElement>) => {
    if (e.target !== e.currentTarget) return;
    pointers.current.set(e.pointerId, { x: e.clientX, y: e.clientY });
    e.currentTarget.setPointerCapture?.(e.pointerId);
    if (pointers.current.size === 2) {
      const [a, b] = [...pointers.current.values()];
      pinch.current = { dist: Math.hypot(a!.x - b!.x, a!.y - b!.y) };
    }
  };
  const onBoardMove = (e: ReactPointerEvent<HTMLDivElement>) => {
    const prev = pointers.current.get(e.pointerId);
    if (!prev) return;
    pointers.current.set(e.pointerId, { x: e.clientX, y: e.clientY });
    userMoved.current = true;
    if (pointers.current.size === 2 && pinch.current) {
      const [a, b] = [...pointers.current.values()];
      const dist = Math.hypot(a!.x - b!.x, a!.y - b!.y);
      const r = e.currentTarget.getBoundingClientRect();
      const mid = {
        x: (a!.x + b!.x) / 2 - r.left,
        y: (a!.y + b!.y) / 2 - r.top,
      };
      if (pinch.current.dist > 0) {
        const factor = dist / pinch.current.dist;
        setCamera((c) => zoomAt(c, factor, mid));
      }
      pinch.current = { dist };
      return;
    }
    setCamera((c) => pan(c, e.clientX - prev.x, e.clientY - prev.y));
  };
  const onBoardUp = (e: ReactPointerEvent<HTMLDivElement>) => {
    pointers.current.delete(e.pointerId);
    if (pointers.current.size < 2) pinch.current = null;
  };

  // Escape: the popover first (its own layer), then Comment, then the window.
  const closePopover = useCallback(() => {
    setPopover(null);
    setNote("");
  }, []);
  useEscapeCloses(popover !== null, closePopover);
  const escapeStep = useCallback(() => {
    if (popover) {
      closePopover();
    } else if (mode === "comment") {
      setMode("interact");
    } else {
      props.onClose();
    }
  }, [popover, mode, closePopover, props]);
  useRightDockEscape(true, escapeStep);

  const onPick = useCallback(
    (frame: Frame, element: PickedElement, at: Rect) => {
      setPopover({ frame, element, at });
      setNote("");
    },
    [],
  );

  const addPick = () => {
    if (!popover) return;
    pickSeq += 1;
    setPicks((list) => [
      ...list,
      {
        id: `pick-${pickSeq}`,
        frameId: popover.frame.id,
        page: popover.frame.path,
        size: sizeLabel(popover.frame),
        element: popover.element,
        note,
      },
    ]);
    closePopover();
  };

  const send = () => {
    const prompt = composeCanvasPrompt(picks, text);
    if (!prompt) return;
    props.onSend(prompt);
    setText("");
    setPicks([]);
    setMode("interact");
  };

  const reload = props.reloadBoard;
  const onMove = useCallback(
    (f: Frame, x: number, y: number) => {
      void patchFrame(props.sessionId, f.id, { x, y }).finally(reload);
    },
    [props.sessionId, reload],
  );
  const onResize = useCallback(
    (f: Frame, device: CanvasDevice) => {
      void patchFrame(props.sessionId, f.id, { device }).finally(reload);
    },
    [props.sessionId, reload],
  );
  const onRemove = useCallback(
    (f: Frame) => {
      setPicks((list) => list.filter((p) => p.frameId !== f.id));
      void removeFrame(props.sessionId, f.id).finally(reload);
    },
    [props.sessionId, reload],
  );
  const onLeft = useCallback(
    (f: Frame) => {
      void rotateFrame(props.sessionId, f.id).finally(reload);
    },
    [props.sessionId, reload],
  );

  // The field takes the focus as the window opens, except on a touch screen.
  const windowRef = useRef<HTMLDivElement | null>(null);
  useEffect(() => {
    const opener =
      document.activeElement instanceof HTMLElement
        ? document.activeElement
        : null;
    if (composerAutoFocusAllowed()) {
      windowRef.current
        ?.querySelector<HTMLTextAreaElement>(".canvas-bar-field")
        ?.focus({ preventScroll: true });
    }
    return () => {
      if (opener?.isConnected) opener.focus({ preventScroll: true });
    };
  }, []);

  const popoverStyle = popover
    ? {
        left: Math.max(8, Math.min(popover.at.x, viewport.width - 288)),
        top: Math.max(
          8,
          Math.min(popover.at.y + popover.at.height + 8, viewport.height - 170),
        ),
      }
    : undefined;

  return (
    <div
      ref={windowRef}
      className={
        "files-dock-cluster canvas-window" + (expanded ? " is-expanded" : "")
      }
      data-testid="canvas-view"
      role="dialog"
      aria-label={t("canvas.title")}
    >
      <div className="files-header">
        <div className="files-title-block">
          <div className="files-title-text">
            <h2 className="files-title">{t("canvas.title")}</h2>
            <p className="files-subtitle">
              {tp("canvas.frames", frames.length)}
            </p>
          </div>
        </div>
        <div className="files-actions canvas-actions">
          <div
            className="canvas-mode"
            role="group"
            aria-label={t("canvas.mode")}
          >
            <button
              type="button"
              className={
                "canvas-mode-btn" + (mode === "interact" ? " is-active" : "")
              }
              aria-pressed={mode === "interact"}
              data-testid="canvas-mode-interact"
              title={t("canvas.interactTitle")}
              onClick={() => setMode("interact")}
            >
              {t("canvas.interact")}
            </button>
            <button
              type="button"
              className={
                "canvas-mode-btn" + (mode === "comment" ? " is-active" : "")
              }
              aria-pressed={mode === "comment"}
              data-testid="canvas-mode-comment"
              title={t("canvas.commentTitle")}
              onClick={() => setMode("comment")}
            >
              {t("canvas.comment")}
            </button>
          </div>
          <div
            className="canvas-zoom"
            role="group"
            aria-label={t("canvas.zoom")}
          >
            <button
              type="button"
              className="files-icon-btn"
              aria-label={t("canvas.zoomOut")}
              title={t("canvas.zoomOut")}
              onClick={() => zoomBy(1 / 1.25)}
            >
              −
            </button>
            <button
              type="button"
              className="canvas-zoom-value"
              data-testid="canvas-zoom"
              title={t("canvas.zoomReset")}
              onClick={() => {
                userMoved.current = true;
                setCamera((c) =>
                  zoomAt(c, 1 / c.scale, {
                    x: viewport.width / 2,
                    y: viewport.height / 2,
                  }),
                );
              }}
            >
              {zoomPercent(camera)}%
            </button>
            <button
              type="button"
              className="files-icon-btn"
              aria-label={t("canvas.zoomIn")}
              title={t("canvas.zoomIn")}
              onClick={() => zoomBy(1.25)}
            >
              +
            </button>
            <button
              type="button"
              className="canvas-fit"
              data-testid="canvas-fit"
              title={t("canvas.fitTitle")}
              onClick={fitAll}
            >
              {t("canvas.fit")}
            </button>
          </div>
          <button
            type="button"
            className={"files-icon-btn" + (expanded ? " is-active" : "")}
            aria-pressed={expanded}
            aria-label={t(expanded ? "files.restore" : "files.expand")}
            title={t(expanded ? "files.restore" : "files.expand")}
            onClick={() => setExpanded((v) => !v)}
          >
            {expanded ? <IconRestore /> : <IconExpand />}
          </button>
          <button
            type="button"
            className="sessions-close files-close"
            data-testid="canvas-close"
            aria-label={t("canvas.close")}
            onClick={props.onClose}
          >
            ×
          </button>
        </div>
      </div>

      <div
        ref={boardRef}
        className={
          "canvas-board" + (mode === "comment" ? " is-commenting" : "")
        }
        data-testid="canvas-board"
        onPointerDown={onBoardDown}
        onPointerMove={onBoardMove}
        onPointerUp={onBoardUp}
        onPointerCancel={onBoardUp}
      >
        {frames.length === 0 ? (
          <div className="canvas-empty" data-testid="canvas-empty">
            <p className="canvas-empty-title">{t("canvas.emptyTitle")}</p>
            <p className="canvas-empty-hint">{t("canvas.emptyHint")}</p>
          </div>
        ) : null}
        {frames.map((f) => (
          <CanvasFrame
            key={f.id}
            sessionId={props.sessionId}
            frame={f}
            screen={toScreen(camera, {
              x: f.x,
              y: f.y,
              width: f.width,
              height: f.height,
            })}
            scale={camera.scale}
            mode={mode}
            focused={f.id === props.focusFrameId}
            onPick={onPick}
            onEscape={escapeStep}
            onMove={onMove}
            onResize={onResize}
            onRemove={onRemove}
            onLeft={onLeft}
          />
        ))}
        {popover ? (
          <div
            className="canvas-popover"
            style={popoverStyle}
            data-testid="canvas-popover"
            role="dialog"
            aria-label={t("canvas.popoverTitle")}
          >
            <p className="canvas-popover-target">
              <code>{pickLabel({ element: popover.element })}</code>
            </p>
            <textarea
              className="canvas-popover-note"
              data-testid="canvas-popover-note"
              rows={2}
              value={note}
              autoFocus={!touchOnly}
              placeholder={t("canvas.notePlaceholder")}
              onChange={(e) => setNote(e.target.value)}
              onKeyDown={(e) => {
                if (
                  e.key === "Enter" &&
                  !e.shiftKey &&
                  !e.nativeEvent.isComposing &&
                  !touchOnly
                ) {
                  e.preventDefault();
                  addPick();
                }
              }}
            />
            <div className="canvas-popover-actions">
              <button
                type="button"
                className="canvas-popover-cancel"
                onClick={closePopover}
              >
                {t("canvas.cancel")}
              </button>
              <button
                type="button"
                className="canvas-popover-add"
                data-testid="canvas-popover-add"
                onClick={addPick}
              >
                {t("canvas.addPick")}
              </button>
            </div>
          </div>
        ) : null}
      </div>

      <CanvasPromptBar
        text={text}
        onText={setText}
        picks={picks}
        onRemovePick={(id) =>
          setPicks((list) => list.filter((p) => p.id !== id))
        }
        busy={props.busy}
        onSend={send}
        onStop={props.onStop}
        onChat={props.onClose}
        touchOnly={touchOnly}
      />
    </div>
  );
}
