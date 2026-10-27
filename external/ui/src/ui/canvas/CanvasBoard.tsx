import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import type {
  KeyboardEvent as ReactKeyboardEvent,
  PointerEvent as ReactPointerEvent,
} from "react";
import { useT } from "../i18n/I18nProvider";
import { useEscapeCloses } from "../components/useEscapeCloses";
import { ZoomControls } from "../components/ZoomControls";
import { ZOOM_STEP, wheelFactor } from "../components/zoom";
import {
  serverSnapshotTouchOnly,
  snapshotTouchOnly,
  subscribeTouchOnly,
} from "../shellBreakpoint";
import {
  openCanvasFile,
  patchNodes,
  removeFrame,
  rotateFrame,
  type CanvasDevice,
  type CanvasFrame as Frame,
  type CanvasItem,
} from "./api";
import type { PickedElement } from "./bridgeProtocol";
import { boundsOf, fitCamera, pan, zoomAt, type Rect } from "./camera";
import { CameraStore } from "./cameraStore";
import { CanvasFrame, sizeLabel } from "./CanvasFrame";
import { CanvasPromptBar } from "./CanvasPromptBar";
import { CanvasEdges, CanvasNode } from "./CanvasNodes";
import { useCanvasBoard } from "./useCanvasBoard";
import { PermissionPromptSection } from "../chat/PermissionPromptSection";
import { QuestionPromptSection } from "../chat/QuestionPromptSection";
import type { TranscriptItem } from "../chat/types";
import type { PermissionResolvedState } from "../chat/permissionTypes";
import type { QuestionResolvedState } from "../chat/questionTypes";
import { drawOrder, insideGroup } from "./jsonCanvas";
import { composeCanvasPrompt, pickLabel, type CanvasPick } from "./pickPrompt";

/** What the bar holds, per chat and file, for the life of the page: closing
 *  the window to read the transcript does not lose a half-written request. */
type Draft = { text: string; picks: CanvasPick[] };
const drafts = new Map<string, Draft>();
const draftListeners = new Map<string, (d: Draft) => void>();

function draftKey(sessionId: string, file: string): string {
  return `${sessionId}\n${file}`;
}

/** Forgets every draft (tests only). */
export function resetCanvasDraftsForTests(): void {
  drafts.clear();
}

/** Puts a request the server never took back in the bar it came from, ahead
 *  of anything written since, whether or not the canvas is open now. */
function giveBackDraft(key: string, sent: Draft): void {
  const cur = drafts.get(key) ?? { text: "", picks: [] };
  const text =
    !cur.text.trim() || cur.text.trim() === sent.text.trim()
      ? sent.text
      : `${sent.text}\n\n${cur.text}`;
  const picks = [
    ...sent.picks,
    ...cur.picks.filter((p) => !sent.picks.some((s) => s.id === p.id)),
  ];
  const next = { text, picks };
  drafts.set(key, next);
  draftListeners.get(key)?.(next);
}

export type CanvasPrompt = Extract<
  TranscriptItem,
  { type: "permission_prompt" } | { type: "question_prompt" }
>;

/** What App hands every canvas of the chat: the turn, the prompts it waits
 *  for, and how a message of the canvas goes. */
export interface CanvasChatProps {
  /** Moves whenever a board of the chat may have changed. */
  epoch: number;
  /** The chat's turn is running. */
  busy: boolean;
  /** Sends the prompt; giveBack puts the bar's text and picks back when the
   *  server never takes it. */
  onSend: (text: string, giveBack: () => void) => void;
  onStop: () => void;
  /** The chat's unanswered permission and question prompts: the window
   *  covers the chat, so a turn waiting on one is answered here. */
  prompts?: CanvasPrompt[];
  /** The chat's tool calls, for what a permission prompt shows. */
  toolCalls?: ReadonlyMap<
    string,
    Extract<TranscriptItem, { type: "tool_call" }>
  >;
  onPermissionResolved?: (
    sessionId: string,
    itemId: string,
    state: PermissionResolvedState,
  ) => void;
  onQuestionResolved?: (
    sessionId: string,
    itemId: string,
    state: QuestionResolvedState,
  ) => void;
}

export interface CanvasBoardProps extends CanvasChatProps {
  sessionId: string;
  /** The .canvas file, relative to the workspace. */
  file: string;
  /** The frame the agent's last show points at, and a counter that moves
   *  whenever it is asked again, for the same frame too. */
  focusFrameId: string;
  focusSeq: number;
  /** Escape with nothing nearer to close, and the bar's Chat: the Files
   *  window goes, the conversation shows. */
  onClose: () => void;
}

interface Popover {
  frame: Frame;
  element: PickedElement;
  at: Rect;
}

let pickSeq = 0;

/**
 * A .canvas file as the Files window shows it: the board of live frames and
 * the file's other nodes and edges, the prompts the turn waits for and a
 * prompt bar under it. Everything on the board stands at its own size in one
 * world layer the camera moves and scales as a whole, so a pan or a zoom draws
 * nothing again; the wheel zooms at the pointer, dragging the empty board pans
 * it. Interact hands the pointer to the pages, Comment picks elements, which
 * become chips of the bar and, sent, a prompt of the chat.
 */
export function CanvasBoard(props: CanvasBoardProps) {
  const { t } = useT();
  const { sessionId, file } = props;
  const touchOnly = useSyncExternalStore(
    subscribeTouchOnly,
    snapshotTouchOnly,
    serverSnapshotTouchOnly,
  );
  const {
    board,
    error: readError,
    reload,
  } = useCanvasBoard(sessionId, file, props.epoch);

  // Opening the file makes it the one the agent shows pages on.
  useEffect(() => {
    if (!sessionId.trim() || !file) return;
    void openCanvasFile(sessionId, file).then(reload, () => {
      /* the read of the board says why */
    });
  }, [sessionId, file, reload]);

  const boardFrames = useMemo(() => board?.frames ?? [], [board]);
  const items = useMemo(() => board?.items ?? [], [board]);
  const edges = useMemo(() => board?.edges ?? [], [board]);
  const revision = board?.revision ?? 0;
  const stamp = board?.stamp ?? "";
  // Where a node was put down, until the board read after the move says so:
  // a dragged node does not jump back while its change is on its way.
  const [placed, setPlaced] = useState<
    Map<string, { x: number; y: number; rev: number }>
  >(() => new Map());
  useEffect(() => {
    setPlaced((m) => {
      if (m.size === 0) return m;
      const next = new Map([...m].filter(([, p]) => p.rev >= revision));
      return next.size === m.size ? m : next;
    });
  }, [revision]);
  const frames = useMemo(
    () =>
      boardFrames.map((f) => {
        const p = placed.get(f.id);
        return p ? { ...f, x: p.x, y: p.y } : f;
      }),
    [boardFrames, placed],
  );
  const nodes = useMemo(
    () =>
      items.map((it) => {
        const p = placed.get(it.id);
        return p ? { ...it, x: p.x, y: p.y } : it;
      }),
    [items, placed],
  );

  const camera = useMemo(() => new CameraStore(), []);
  useEffect(() => () => camera.dispose(), [camera]);
  const boardRef = useRef<HTMLDivElement | null>(null);
  const worldRef = useRef<HTMLDivElement | null>(null);
  useLayoutEffect(() => {
    camera.attach(worldRef.current, boardRef.current);
  }, [camera]);

  const [viewport, setViewport] = useState({ width: 0, height: 0 });
  const viewportRef = useRef(viewport);
  viewportRef.current = viewport;
  const [mode, setMode] = useState<"interact" | "comment">("interact");
  const [popover, setPopover] = useState<Popover | null>(null);
  const [note, setNote] = useState("");
  const key = draftKey(sessionId, file);
  const initial = drafts.get(key) ?? { text: "", picks: [] };
  const [text, setText] = useState(initial.text);
  const [picks, setPicks] = useState<CanvasPick[]>(initial.picks);
  const fittedFor = useRef<string>("");
  const fittedOnce = useRef(false);
  const fitTarget = useRef<string>("");

  useEffect(() => {
    drafts.set(key, { text, picks });
  }, [key, text, picks]);
  useEffect(() => {
    draftListeners.set(key, (d) => {
      setText(d.text);
      setPicks(d.picks);
    });
    return () => {
      draftListeners.delete(key);
    };
  }, [key]);

  useLayoutEffect(() => {
    const el = boardRef.current;
    if (!el) return undefined;
    const measure = () =>
      setViewport((v) =>
        v.width === el.clientWidth && v.height === el.clientHeight
          ? v
          : { width: el.clientWidth, height: el.clientHeight },
      );
    measure();
    const ro =
      typeof ResizeObserver === "function" ? new ResizeObserver(measure) : null;
    ro?.observe(el);
    return () => ro?.disconnect();
  }, []);

  const fitTo = useCallback(
    (rects: Rect[]) => {
      const b = boundsOf(rects);
      const v = viewportRef.current;
      if (!b || v.width <= 0 || v.height <= 0) return;
      camera.fitTo(fitCamera(b, v));
    },
    [camera],
  );

  // Everything the board holds, for Fit: the frames and the other nodes.
  const everything = useMemo<Rect[]>(
    () => [...frames, ...nodes],
    [frames, nodes],
  );
  // The board is fitted when it first shows something, and on the frame the
  // agent's last show points at.
  useEffect(() => {
    if (everything.length === 0 || viewport.width <= 0) return;
    const focus = frames.find((f) => f.id === props.focusFrameId);
    if (focus) {
      const k = `${focus.id}:${props.focusSeq}`;
      if (fittedFor.current === k) return;
      fittedFor.current = k;
    } else if (fittedOnce.current) {
      return;
    }
    fittedOnce.current = true;
    fitTarget.current = focus ? focus.id : "";
    fitTo(focus ? [focus] : everything);
  }, [
    everything,
    frames,
    props.focusFrameId,
    props.focusSeq,
    viewport.width,
    fitTo,
  ]);

  // Until the person moves or zooms the board, it follows the window: a
  // resize (a phone turned, the window expanded) fits it again.
  useEffect(() => {
    if (!camera.fitted() || everything.length === 0 || viewport.width <= 0)
      return;
    const focus = frames.find((f) => f.id === fitTarget.current);
    fitTo(focus ? [focus] : everything);
    // Only a new size refits; new frames are the effect above's.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [viewport.width, viewport.height]);

  const everythingRef = useRef(everything);
  everythingRef.current = everything;
  const fitAll = useCallback(() => {
    fitTarget.current = "";
    fitTo(everythingRef.current);
  }, [fitTo]);

  const zoomBy = useCallback(
    (factor: number) => {
      const v = viewportRef.current;
      camera.move((c) =>
        zoomAt(c, factor, { x: v.width / 2, y: v.height / 2 }),
      );
    },
    [camera],
  );

  // The wheel zooms at the pointer, as the picture viewer and the swarm map
  // do; over a page in Interact the wheel is the page's.
  useEffect(() => {
    const el = boardRef.current;
    if (!el) return undefined;
    const onWheel = (e: WheelEvent) => {
      e.preventDefault();
      const r = el.getBoundingClientRect();
      const factor = wheelFactor(e.deltaY, e.deltaMode);
      camera.move((c) =>
        zoomAt(c, factor, { x: e.clientX - r.left, y: e.clientY - r.top }),
      );
    };
    el.addEventListener("wheel", onWheel, { passive: false });
    return () => el.removeEventListener("wheel", onWheel);
  }, [camera]);

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
        camera.move((c) => zoomAt(c, factor, mid));
      }
      pinch.current = { dist };
      return;
    }
    const dx = e.clientX - prev.x;
    const dy = e.clientY - prev.y;
    camera.move((c) => pan(c, dx, dy));
  };
  const onBoardUp = (e: ReactPointerEvent<HTMLDivElement>) => {
    pointers.current.delete(e.pointerId);
    if (pointers.current.size < 2) pinch.current = null;
  };
  const onBoardKey = (e: ReactKeyboardEvent<HTMLDivElement>) => {
    if (e.target !== e.currentTarget || e.altKey || e.ctrlKey || e.metaKey)
      return;
    if (e.key === "+" || e.key === "=") {
      e.preventDefault();
      zoomBy(ZOOM_STEP);
    } else if (e.key === "-") {
      e.preventDefault();
      zoomBy(1 / ZOOM_STEP);
    } else if (e.key === "0") {
      e.preventDefault();
      fitAll();
    }
  };

  // Escape: the popover first (its own layer), then Comment, then the window.
  const closePopover = useCallback(() => {
    setPopover(null);
    setNote("");
  }, []);
  useEscapeCloses(popover !== null, closePopover);
  useEscapeCloses(popover === null && mode === "comment", () =>
    setMode("interact"),
  );
  const stepRef = useRef({ popover, mode, onClose: props.onClose });
  stepRef.current = { popover, mode, onClose: props.onClose };
  const escapeStep = useCallback(() => {
    const s = stepRef.current;
    if (s.popover) closePopover();
    else if (s.mode === "comment") setMode("interact");
    else s.onClose();
  }, [closePopover]);

  const onPick = useCallback(
    (frame: Frame, element: PickedElement) => {
      const c = camera.get();
      const r = element.rect;
      setPopover({
        frame,
        element,
        at: {
          x: c.x + (frame.x + r.x) * c.scale,
          y: c.y + (frame.y + r.y) * c.scale,
          width: r.width * c.scale,
          height: r.height * c.scale,
        },
      });
      setNote("");
    },
    [camera],
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
    const sent = { text, picks };
    setText("");
    setPicks([]);
    drafts.set(key, { text: "", picks: [] });
    props.onSend(prompt, () => giveBackDraft(key, sent));
    setMode("interact");
  };

  const place = useCallback(
    (moves: { id: string; x: number; y: number }[]) => {
      if (moves.length === 0) return;
      setPlaced((m) => {
        const next = new Map(m);
        for (const mv of moves)
          next.set(mv.id, { x: mv.x, y: mv.y, rev: revision });
        return next;
      });
      void patchNodes(sessionId, file, moves)
        .catch(() =>
          // Refused (the node is gone, the file unreadable): the board as it is.
          setPlaced((m) => {
            const next = new Map(m);
            for (const mv of moves) next.delete(mv.id);
            return next;
          }),
        )
        .finally(reload);
    },
    [sessionId, file, reload, revision],
  );
  const onMove = useCallback(
    (f: Frame, x: number, y: number) => place([{ id: f.id, x, y }]),
    [place],
  );

  // A frame dragged by its title bar, so the edges at it follow.
  const [frameDrag, setFrameDrag] = useState<{
    id: string;
    dx: number;
    dy: number;
  } | null>(null);
  const onFrameDrag = useCallback(
    (f: Frame, dx: number, dy: number) =>
      setFrameDrag(dx === 0 && dy === 0 ? null : { id: f.id, dx, dy }),
    [],
  );

  // Dragging a node of the file moves it, a group with everything inside it,
  // in board units. The pointer is captured only once the drag starts, so a
  // click on a link in a card stays a click.
  const [nodeDrag, setNodeDrag] = useState<{
    ids: string[];
    dx: number;
    dy: number;
  } | null>(null);
  const nodeDragStart = useRef<{
    ids: string[];
    x: number;
    y: number;
    scale: number;
    moving: boolean;
  } | null>(null);
  const framesRef = useRef(frames);
  framesRef.current = frames;
  const nodesRef = useRef(nodes);
  nodesRef.current = nodes;
  const onNodeDown = useCallback(
    (item: CanvasItem, e: ReactPointerEvent<HTMLElement>) => {
      if (e.button !== 0) return;
      if ((e.target as HTMLElement).closest("a, button, input, textarea"))
        return;
      e.stopPropagation();
      const all = [...framesRef.current, ...nodesRef.current];
      const ids =
        item.type === "group"
          ? [item.id, ...insideGroup(item, all).filter((id) => id !== item.id)]
          : [item.id];
      nodeDragStart.current = {
        ids,
        x: e.clientX,
        y: e.clientY,
        scale: camera.get().scale,
        moving: false,
      };
    },
    [camera],
  );
  const onNodeMove = useCallback((e: ReactPointerEvent<HTMLElement>) => {
    const s = nodeDragStart.current;
    if (!s) return;
    const dx = e.clientX - s.x;
    const dy = e.clientY - s.y;
    if (!s.moving) {
      if (Math.abs(dx) + Math.abs(dy) < 4) return;
      s.moving = true;
      e.currentTarget.setPointerCapture?.(e.pointerId);
    }
    setNodeDrag({ ids: s.ids, dx: dx / s.scale, dy: dy / s.scale });
  }, []);
  const onNodeUp = useCallback(
    (e: ReactPointerEvent<HTMLElement>) => {
      const s = nodeDragStart.current;
      nodeDragStart.current = null;
      setNodeDrag(null);
      if (!s?.moving || e.type === "pointercancel") return;
      const dx = Math.round((e.clientX - s.x) / s.scale);
      const dy = Math.round((e.clientY - s.y) / s.scale);
      if (dx === 0 && dy === 0) return;
      const at = new Map<string, { x: number; y: number }>();
      for (const n of [...framesRef.current, ...nodesRef.current])
        at.set(n.id, n);
      place(
        s.ids.flatMap((id) => {
          const p = at.get(id);
          return p ? [{ id, x: p.x + dx, y: p.y + dy }] : [];
        }),
      );
    },
    [place],
  );

  // Where every node stands on the board, a drag in progress included: what
  // the edges are drawn between.
  const rects = useMemo(() => {
    const out = new Map<string, Rect>();
    const shifted = (id: string, r: Rect): Rect => {
      let { x, y } = r;
      if (nodeDrag?.ids.includes(id)) {
        x += nodeDrag.dx;
        y += nodeDrag.dy;
      }
      if (frameDrag?.id === id) {
        x += frameDrag.dx;
        y += frameDrag.dy;
      }
      return { x, y, width: r.width, height: r.height };
    };
    for (const f of frames) out.set(f.id, shifted(f.id, f));
    for (const n of nodes) out.set(n.id, shifted(n.id, n));
    return out;
  }, [frames, nodes, nodeDrag, frameDrag]);
  const ordered = useMemo(() => drawOrder(nodes), [nodes]);
  const groups = ordered.filter((n) => n.type === "group");
  const cards = ordered.filter((n) => n.type !== "group");

  const onResize = useCallback(
    (f: Frame, device: CanvasDevice) => {
      void patchNodes(sessionId, file, [{ id: f.id, device }]).finally(reload);
    },
    [sessionId, file, reload],
  );
  const onRemove = useCallback(
    (f: Frame) => {
      setPicks((list) => list.filter((p) => p.frameId !== f.id));
      void removeFrame(sessionId, file, f.id).finally(reload);
    },
    [sessionId, file, reload],
  );
  const onLeft = useCallback(
    (f: Frame) => {
      void rotateFrame(sessionId, file, f.id).finally(reload);
    },
    [sessionId, file, reload],
  );

  const popoverStyle = popover
    ? {
        left: Math.max(8, Math.min(popover.at.x, viewport.width - 288)),
        top: Math.max(
          8,
          Math.min(popover.at.y + popover.at.height + 8, viewport.height - 170),
        ),
      }
    : undefined;

  const fileError = board?.error || "";
  const empty = board !== null && everything.length === 0 && !fileError;

  return (
    <div
      className="canvas-pane"
      data-testid="canvas-view"
      data-board={board ? "read" : readError ? "error" : "reading"}
      aria-label={t("canvas.title")}
    >
      <div
        ref={boardRef}
        className={
          "canvas-board" + (mode === "comment" ? " is-commenting" : "")
        }
        data-testid="canvas-board"
        tabIndex={0}
        onPointerDown={onBoardDown}
        onPointerMove={onBoardMove}
        onPointerUp={onBoardUp}
        onPointerCancel={onBoardUp}
        onKeyDown={onBoardKey}
      >
        <div ref={worldRef} className="canvas-world" data-testid="canvas-world">
          {groups.map((n) => (
            <CanvasNode
              key={n.id}
              sessionId={sessionId}
              item={n}
              dx={nodeDrag?.ids.includes(n.id) ? nodeDrag.dx : 0}
              dy={nodeDrag?.ids.includes(n.id) ? nodeDrag.dy : 0}
              version={stamp}
              dragging={!!nodeDrag?.ids.includes(n.id)}
              onPointerDown={onNodeDown}
              onPointerMove={onNodeMove}
              onPointerUp={onNodeUp}
            />
          ))}
          <CanvasEdges edges={edges} rects={rects} />
          {cards.map((n) => (
            <CanvasNode
              key={n.id}
              sessionId={sessionId}
              item={n}
              dx={nodeDrag?.ids.includes(n.id) ? nodeDrag.dx : 0}
              dy={nodeDrag?.ids.includes(n.id) ? nodeDrag.dy : 0}
              version={stamp}
              dragging={!!nodeDrag?.ids.includes(n.id)}
              onPointerDown={onNodeDown}
              onPointerMove={onNodeMove}
              onPointerUp={onNodeUp}
            />
          ))}
          {frames.map((f) => (
            <CanvasFrameAt
              key={f.id}
              sessionId={sessionId}
              file={file}
              frame={f}
              shift={nodeDrag?.ids.includes(f.id) ? nodeDrag : null}
              camera={camera}
              mode={mode}
              focused={f.id === props.focusFrameId}
              onPick={onPick}
              onEscape={escapeStep}
              onMove={onMove}
              onDragging={onFrameDrag}
              onResize={onResize}
              onRemove={onRemove}
              onLeft={onLeft}
            />
          ))}
        </div>
        {fileError || readError ? (
          <div
            className="canvas-link-error"
            role="alert"
            data-testid="canvas-file-error"
          >
            {t("canvas.fileError", { file, error: fileError || readError })}
          </div>
        ) : null}
        {empty ? (
          <div className="canvas-empty" data-testid="canvas-empty">
            <p className="canvas-empty-title">{t("canvas.emptyTitle")}</p>
            <p className="canvas-empty-hint">{t("canvas.emptyHint")}</p>
          </div>
        ) : null}
        <div
          className="canvas-mode"
          role="group"
          aria-label={t("canvas.mode")}
          onPointerDown={(e) => e.stopPropagation()}
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
        <CanvasZoom camera={camera} onZoom={zoomBy} onFit={fitAll} />
        {popover ? (
          <div
            className="canvas-popover"
            style={popoverStyle}
            data-testid="canvas-popover"
            role="dialog"
            aria-label={t("canvas.popoverTitle")}
            onPointerDown={(e) => e.stopPropagation()}
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

      {props.prompts && props.prompts.length > 0 ? (
        <section
          className="canvas-prompts"
          data-testid="canvas-prompts"
          aria-label={t("canvas.promptsTitle")}
        >
          <p className="canvas-prompts-title">{t("canvas.promptsTitle")}</p>
          {props.prompts.map((it) =>
            it.type === "permission_prompt" ? (
              <PermissionPromptSection
                key={it.id}
                itemId={it.id}
                payload={it.payload}
                toolCall={props.toolCalls?.get(it.payload.toolCall.toolCallId)}
                onResolved={(state) =>
                  props.onPermissionResolved?.(
                    it.payload.sessionId,
                    it.id,
                    state,
                  )
                }
              />
            ) : (
              <QuestionPromptSection
                key={it.id}
                itemId={it.id}
                payload={it.payload}
                onResolved={(state) =>
                  props.onQuestionResolved?.(it.payload.sessionId, it.id, state)
                }
              />
            ),
          )}
        </section>
      ) : null}

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

export default CanvasBoard;

/** A frame, shifted while the group it lies in is dragged. */
function CanvasFrameAt(
  props: Omit<Parameters<typeof CanvasFrame>[0], "frame"> & {
    frame: Frame;
    shift: { dx: number; dy: number } | null;
  },
) {
  const { shift, frame, ...rest } = props;
  const at = useMemo(
    () =>
      shift
        ? { ...frame, x: frame.x + shift.dx, y: frame.y + shift.dy }
        : frame,
    [frame, shift],
  );
  return <CanvasFrame {...rest} frame={at} />;
}

/** The zoom controls of the board, the only part a zoom draws again. */
function CanvasZoom(props: {
  camera: CameraStore;
  onZoom: (factor: number) => void;
  onFit: () => void;
}) {
  const { t } = useT();
  const { camera } = props;
  const percent = useSyncExternalStore(
    useCallback((fn: () => void) => camera.subscribe(fn), [camera]),
    () => camera.percent(),
    () => null,
  );
  return (
    <ZoomControls
      testId="canvas"
      percent={percent}
      onZoomOut={() => props.onZoom(1 / ZOOM_STEP)}
      onZoomIn={() => props.onZoom(ZOOM_STEP)}
      onFit={props.onFit}
      labels={{
        group: t("canvas.zoom"),
        zoomOut: t("canvas.zoomOut"),
        zoomIn: t("canvas.zoomIn"),
        fit: t("canvas.fitTitle"),
      }}
    />
  );
}
