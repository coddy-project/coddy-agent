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
  patchNodes,
  removeFrame,
  rotateFrame,
  type CanvasBoard,
  type CanvasDevice,
  type CanvasFrame as Frame,
  type CanvasItem,
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
import { CanvasEdges, CanvasNode } from "./CanvasNodes";
import { CanvasFileMenu } from "./CanvasFileMenu";
import { drawOrder, insideGroup } from "./jsonCanvas";
import { composeCanvasPrompt, pickLabel, type CanvasPick } from "./pickPrompt";

/** What the bar holds, per chat, for the life of the page: closing the window
 *  to read the transcript does not lose a half-written request. */
type Draft = { text: string; picks: CanvasPick[] };
const drafts = new Map<string, Draft>();
const draftListeners = new Map<string, (d: Draft) => void>();

/** Forgets every draft (tests only). */
export function resetCanvasDraftsForTests(): void {
  drafts.clear();
}

/** Puts a request the server never took back in the bar of its chat, ahead
 *  of anything written since, whether or not the window is open now. */
function giveBackDraft(sessionId: string, sent: Draft): void {
  const cur = drafts.get(sessionId) ?? { text: "", picks: [] };
  const text =
    !cur.text.trim() || cur.text.trim() === sent.text.trim()
      ? sent.text
      : `${sent.text}\n\n${cur.text}`;
  const picks = [
    ...sent.picks,
    ...cur.picks.filter((p) => !sent.picks.some((s) => s.id === p.id)),
  ];
  const next = { text, picks };
  drafts.set(sessionId, next);
  draftListeners.get(sessionId)?.(next);
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
  /** Sends the prompt; giveBack puts the bar's text and picks back when the
   *  server never takes it. */
  onSend: (text: string, giveBack: () => void) => void;
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
  const boardFrames = useMemo(() => props.board?.frames ?? [], [props.board]);
  const items = useMemo(() => props.board?.items ?? [], [props.board]);
  const edges = useMemo(() => props.board?.edges ?? [], [props.board]);
  const link = props.board?.link;
  const revision = props.board?.revision ?? 0;
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
  // The board (its file, or "" for the chat's own) last fitted whole.
  const fittedBoard = useRef<string | null>(null);
  // Until the person moves or zooms the board, it follows the window: a
  // resize (a phone turned, the window expanded) fits it again.
  const userMoved = useRef(false);
  const fitTarget = useRef<string>("");

  useEffect(() => {
    drafts.set(props.sessionId, { text, picks });
  }, [props.sessionId, text, picks]);
  useEffect(() => {
    const sid = props.sessionId;
    draftListeners.set(sid, (d) => {
      setText(d.text);
      setPicks(d.picks);
    });
    return () => {
      draftListeners.delete(sid);
    };
  }, [props.sessionId]);

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

  // Everything the board holds, for Fit: the frames and the nodes of a file.
  const everything = useMemo<Rect[]>(
    () => [...frames, ...nodes],
    [frames, nodes],
  );
  // The board is fitted when it first shows something, again when it becomes
  // another file, and on the frame the address or the agent's last show
  // points at.
  const boardKey = link?.path ?? "";
  useEffect(() => {
    if (everything.length === 0 || viewport.width <= 0) return;
    const focus = frames.find((f) => f.id === props.focusFrameId);
    if (focus) {
      const key = `f:${focus.id}:${props.focusSeq}`;
      if (fittedFor.current === key) return;
      fittedFor.current = key;
    } else if (fittedBoard.current === boardKey) {
      return;
    }
    fittedBoard.current = boardKey;
    userMoved.current = false;
    fitTarget.current = focus ? focus.id : "";
    fitTo(focus ? [focus] : everything);
  }, [
    everything,
    frames,
    boardKey,
    props.focusFrameId,
    props.focusSeq,
    viewport.width,
    fitTo,
  ]);

  useEffect(() => {
    if (userMoved.current || everything.length === 0 || viewport.width <= 0)
      return;
    const focus = frames.find((f) => f.id === fitTarget.current);
    fitTo(focus ? [focus] : everything);
    // Only a new size refits; new frames are the effect above's.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [viewport.width, viewport.height]);

  const fitAll = () => {
    userMoved.current = false;
    fitTarget.current = "";
    fitTo(everything);
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
    const sent = { text, picks };
    const sid = props.sessionId;
    setText("");
    setPicks([]);
    drafts.set(sid, { text: "", picks: [] });
    props.onSend(prompt, () => giveBackDraft(sid, sent));
    setMode("interact");
  };

  const reload = props.reloadBoard;
  const place = useCallback(
    (moves: { id: string; x: number; y: number }[]) => {
      if (moves.length === 0) return;
      setPlaced((m) => {
        const next = new Map(m);
        for (const mv of moves)
          next.set(mv.id, { x: mv.x, y: mv.y, rev: revision });
        return next;
      });
      void patchNodes(props.sessionId, moves)
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
    [props.sessionId, reload, revision],
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

  // Dragging a node of the file moves it, a group with everything inside it.
  // The pointer is captured only once the drag starts, so a click on a link
  // in a card stays a click.
  const [nodeDrag, setNodeDrag] = useState<{
    ids: string[];
    dx: number;
    dy: number;
  } | null>(null);
  const nodeDragStart = useRef<{
    ids: string[];
    x: number;
    y: number;
    moving: boolean;
  } | null>(null);
  const onNodeDown = useCallback(
    (item: CanvasItem, e: ReactPointerEvent<HTMLElement>) => {
      if (e.button !== 0) return;
      if ((e.target as HTMLElement).closest("a, button, input, textarea"))
        return;
      e.stopPropagation();
      const ids =
        item.type === "group"
          ? [
              item.id,
              ...insideGroup(item, [...frames, ...nodes]).filter(
                (id) => id !== item.id,
              ),
            ]
          : [item.id];
      nodeDragStart.current = {
        ids,
        x: e.clientX,
        y: e.clientY,
        moving: false,
      };
    },
    [frames, nodes],
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
    setNodeDrag({ ids: s.ids, dx, dy });
  }, []);
  const onNodeUp = useCallback(
    (e: ReactPointerEvent<HTMLElement>) => {
      const s = nodeDragStart.current;
      nodeDragStart.current = null;
      setNodeDrag(null);
      if (!s?.moving || e.type === "pointercancel") return;
      const dx = Math.round((e.clientX - s.x) / camera.scale);
      const dy = Math.round((e.clientY - s.y) / camera.scale);
      if (dx === 0 && dy === 0) return;
      const at = new Map<string, { x: number; y: number }>();
      for (const n of [...frames, ...nodes]) at.set(n.id, n);
      place(
        s.ids.flatMap((id) => {
          const p = at.get(id);
          return p ? [{ id, x: p.x + dx, y: p.y + dy }] : [];
        }),
      );
    },
    [camera.scale, frames, nodes, place],
  );

  /** Where a node stands on the screen, a drag in progress included. */
  const screenOf = (id: string, r: Rect): Rect => {
    const s = toScreen(camera, r);
    if (nodeDrag?.ids.includes(id)) {
      return { ...s, x: s.x + nodeDrag.dx, y: s.y + nodeDrag.dy };
    }
    return s;
  };
  // A frame draws its own title-bar drag; the edges add it.
  const screenRects = new Map<string, Rect>();
  for (const f of frames) {
    const r = screenOf(f.id, f);
    screenRects.set(
      f.id,
      frameDrag?.id === f.id
        ? { ...r, x: r.x + frameDrag.dx, y: r.y + frameDrag.dy }
        : r,
    );
  }
  for (const n of nodes) screenRects.set(n.id, screenOf(n.id, n));
  const ordered = drawOrder(nodes);
  const groups = ordered.filter((n) => n.type === "group");
  const cards = ordered.filter((n) => n.type !== "group");
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
            <p className="files-subtitle" data-testid="canvas-subtitle">
              {link ? (
                <>
                  <span className="canvas-subtitle-file" title={link.path}>
                    {link.path}
                  </span>
                  {" · "}
                </>
              ) : null}
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
          <CanvasFileMenu
            sessionId={props.sessionId}
            link={link}
            hasFrames={frames.length > 0}
            onChanged={reload}
          />
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
        {link?.error ? (
          <div
            className="canvas-link-error"
            role="alert"
            data-testid="canvas-link-error"
          >
            {t("canvas.linkError", { file: link.path, error: link.error })}
          </div>
        ) : null}
        {everything.length === 0 && !link?.error ? (
          <div className="canvas-empty" data-testid="canvas-empty">
            <p className="canvas-empty-title">
              {t(link ? "canvas.emptyFileTitle" : "canvas.emptyTitle")}
            </p>
            <p className="canvas-empty-hint">
              {t(link ? "canvas.emptyFileHint" : "canvas.emptyHint")}
            </p>
          </div>
        ) : null}
        {groups.map((n) => (
          <CanvasNode
            key={n.id}
            sessionId={props.sessionId}
            item={n}
            screen={screenRects.get(n.id)!}
            scale={camera.scale}
            version={link?.stamp ?? ""}
            dragging={!!nodeDrag?.ids.includes(n.id)}
            onPointerDown={onNodeDown}
            onPointerMove={onNodeMove}
            onPointerUp={onNodeUp}
          />
        ))}
        <CanvasEdges edges={edges} rects={screenRects} scale={camera.scale} />
        {cards.map((n) => (
          <CanvasNode
            key={n.id}
            sessionId={props.sessionId}
            item={n}
            screen={screenRects.get(n.id)!}
            scale={camera.scale}
            version={link?.stamp ?? ""}
            dragging={!!nodeDrag?.ids.includes(n.id)}
            onPointerDown={onNodeDown}
            onPointerMove={onNodeMove}
            onPointerUp={onNodeUp}
          />
        ))}
        {frames.map((f) => (
          <CanvasFrame
            key={f.id}
            sessionId={props.sessionId}
            frame={f}
            screen={screenOf(f.id, f)}
            scale={camera.scale}
            onDragging={onFrameDrag}
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
