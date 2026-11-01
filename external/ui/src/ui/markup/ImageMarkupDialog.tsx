import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent as ReactKeyboardEvent,
  type PointerEvent as ReactPointerEvent,
} from "react";
import { createPortal } from "react-dom";
import { useT } from "../i18n/I18nProvider";
import { useEscapeCloses } from "../components/useEscapeCloses";
import {
  canvasToPng,
  drawShape,
  labelFont,
  loadPicture as loadPictureDefault,
  releaseCanvas,
  renderPicture,
  type LoadedPicture,
} from "./markupCanvas";
import {
  cropFor,
  dragDrew,
  fitView,
  MARKUP_PALETTE,
  MARKUP_TOOL_KEYS,
  penPressure,
  sizeAfter,
  strokeWidthFor,
  textSizeFor,
  type MarkupOp,
  type MarkupShape,
  type MarkupTool,
  type Pt,
  type Size,
} from "./markupModel";
import { MarkupToolIcon, RotateIcon, UndoIcon } from "./markupIcons";

type Draft =
  | { kind: "shape"; shape: MarkupShape }
  | { kind: "crop"; a: Pt; b: Pt };

type Label = { at: Pt; text: string };

/**
 * How long after a pen last touched or hovered a finger is taken for the
 * palm resting beside it and draws nothing.
 */
const PALM_AFTER_PEN_MS = 1000;

/** The points one move brings: every sample the browser coalesced into it. */
function moveSamples(e: ReactPointerEvent<HTMLCanvasElement>): {
  clientX: number;
  clientY: number;
  pointerType?: string;
  pressure?: number;
}[] {
  const native = e.nativeEvent as PointerEvent | undefined;
  const coalesced =
    native && typeof native.getCoalescedEvents === "function"
      ? native.getCoalescedEvents()
      : [];
  return coalesced.length > 0 ? coalesced : [e];
}

const DRAW_TOOLS: MarkupTool[] = ["pen", "ellipse", "rect", "arrow", "text"];

export type ImageMarkupDialogProps = {
  file: File;
  /** The picture as marked up, a PNG named after the original. */
  onDone: (file: File) => void;
  onCancel: () => void;
  /** Decodes the file; tests hand in a fake (jsdom decodes nothing). */
  loadPicture?: (file: File) => Promise<LoadedPicture>;
  /** Encodes the result; tests hand in a fake (jsdom has no toBlob). */
  encode?: (canvas: HTMLCanvasElement) => Promise<Blob>;
};

/** A picture's name with the extension of the PNG it becomes. */
export function markedUpName(name: string): string {
  const dot = name.lastIndexOf(".");
  return `${dot > 0 ? name.slice(0, dot) : name || "picture"}.png`;
}

/**
 * The picture markup editor: the picture fitted into the window, the tools
 * and the palette under it, Cancel on the left and Done on the right, as in
 * the console's window on an X display (external/cli/markup). Done hands back
 * the marked-up picture as PNG; Cancel and Escape hand back nothing.
 */
export function ImageMarkupDialog(props: ImageMarkupDialogProps) {
  const { t } = useT();
  const { file, onDone, onCancel } = props;
  const load = props.loadPicture ?? loadPictureDefault;
  const encode = props.encode ?? canvasToPng;

  const [picture, setPicture] = useState<LoadedPicture | null>(null);
  const [failed, setFailed] = useState(false);
  const [ops, setOps] = useState<MarkupOp[]>([]);
  const [tool, setTool] = useState<MarkupTool>("pen");
  const [colorIndex, setColorIndex] = useState(0);
  const [label, setLabelState] = useState<Label | null>(null);
  // The label as of the last change, read by the commit: Enter and Escape
  // move the focus, and the blur that follows must see the label already
  // committed or dropped, not the one the render it closes over showed.
  const labelRef = useRef<Label | null>(null);
  const setLabel = useCallback((next: Label | null) => {
    labelRef.current = next;
    setLabelState(next);
  }, []);
  const [busy, setBusy] = useState(false);
  const [stage, setStage] = useState<Size>({ w: 0, h: 0 });
  const [, setTick] = useState(0);
  const draftRef = useRef<Draft | null>(null);
  // The pointer a draft belongs to: a second finger, or a palm under a pen,
  // adds nothing to it.
  const draftPointerRef = useRef<{ id: number; type: string } | null>(null);
  // When a pen last touched or hovered the picture, for palm rejection.
  const lastPenAtRef = useRef(Number.NEGATIVE_INFINITY);
  const rootRef = useRef<HTMLDivElement | null>(null);
  const stageRef = useRef<HTMLDivElement | null>(null);
  const canvasRef = useRef<HTMLCanvasElement | null>(null);
  const color = (MARKUP_PALETTE[colorIndex] ?? MARKUP_PALETTE[0]).color;

  // Set once the editor is cancelled or gone: a PNG still being encoded
  // then hands nothing back.
  const closedRef = useRef(false);
  useEffect(() => {
    closedRef.current = false;
    return () => {
      closedRef.current = true;
    };
  }, []);
  const cancel = useCallback(() => {
    closedRef.current = true;
    onCancel();
  }, [onCancel]);

  useEffect(() => {
    let live = true;
    let loaded: LoadedPicture | null = null;
    load(file).then(
      (p) => {
        loaded = p;
        if (live) setPicture(p);
        else p.release?.();
      },
      () => {
        if (live) setFailed(true);
      },
    );
    return () => {
      live = false;
      loaded?.release?.();
    };
  }, [file, load]);

  const baseSize: Size = useMemo(
    () => ({ w: picture?.width ?? 0, h: picture?.height ?? 0 }),
    [picture],
  );
  const size = useMemo(() => sizeAfter(baseSize, ops), [baseSize, ops]);
  // The picture as it stands. A new shape is drawn onto the current raster;
  // anything else (undo, a crop, a turn) replays the history onto a fresh
  // one and the old one is released, so a large photo is not held once per
  // step.
  const rasterRef = useRef<{
    picture: LoadedPicture;
    ops: readonly MarkupOp[];
    canvas: HTMLCanvasElement;
  } | null>(null);
  const raster = useMemo(() => {
    if (!picture) return null;
    const prev =
      rasterRef.current?.picture === picture ? rasterRef.current : null;
    if (prev && prev.ops === ops) return prev.canvas;
    const last = ops[ops.length - 1];
    if (
      prev &&
      last?.kind === "shape" &&
      ops.length === prev.ops.length + 1 &&
      prev.ops.every((op, i) => op === ops[i])
    ) {
      const ctx = prev.canvas.getContext("2d");
      if (ctx) {
        drawShape(ctx, last.shape);
        rasterRef.current = { picture, ops, canvas: prev.canvas };
        return prev.canvas;
      }
    }
    const next = renderPicture(picture.source, baseSize, ops);
    if (rasterRef.current && rasterRef.current.canvas !== next) {
      releaseCanvas(rasterRef.current.canvas);
    }
    rasterRef.current = { picture, ops, canvas: next };
    return next;
  }, [picture, baseSize, ops]);
  // The last raster goes when its picture does: on close, Cancel included.
  useEffect(
    () => () => {
      if (rasterRef.current?.picture === picture) {
        releaseCanvas(rasterRef.current.canvas);
        rasterRef.current = null;
      }
    },
    [picture],
  );
  const view = fitView(size, stage);

  // The stage's size decides how large the picture is shown.
  useLayoutEffect(() => {
    const el = stageRef.current;
    if (!el) return undefined;
    const read = () => setStage({ w: el.clientWidth, h: el.clientHeight });
    read();
    if (typeof ResizeObserver === "undefined") return undefined;
    const ro = new ResizeObserver(read);
    ro.observe(el);
    return () => ro.disconnect();
  }, []);

  // Focus moves into the editor so its keys never type into the composer,
  // and back where it was when the editor closes.
  useEffect(() => {
    const before = document.activeElement as HTMLElement | null;
    rootRef.current?.focus();
    return () => before?.focus?.();
  }, []);

  // Leaving the screen closes the editor, like the picture viewer.
  useEffect(() => {
    window.addEventListener("hashchange", cancel);
    window.addEventListener("popstate", cancel);
    return () => {
      window.removeEventListener("hashchange", cancel);
      window.removeEventListener("popstate", cancel);
    };
  }, [cancel]);

  const paint = useCallback(() => {
    const canvas = canvasRef.current;
    const ctx = canvas?.getContext("2d");
    if (!canvas || !ctx || !raster) return;
    ctx.setTransform(1, 0, 0, 1, 0, 0);
    ctx.clearRect(0, 0, canvas.width, canvas.height);
    ctx.drawImage(raster, 0, 0);
    const draft = draftRef.current;
    if (draft?.kind === "shape") {
      drawShape(ctx, draft.shape);
    } else if (draft?.kind === "crop") {
      const x = Math.min(draft.a.x, draft.b.x);
      const y = Math.min(draft.a.y, draft.b.y);
      const w = Math.abs(draft.b.x - draft.a.x);
      const h = Math.abs(draft.b.y - draft.a.y);
      ctx.save();
      ctx.fillStyle = "rgba(0, 0, 0, 0.5)";
      ctx.beginPath();
      ctx.rect(0, 0, canvas.width, canvas.height);
      ctx.rect(x, y, w, h);
      ctx.fill("evenodd");
      ctx.strokeStyle = "#ffffff";
      ctx.lineWidth = Math.max(1, 2 / (view.k || 1));
      ctx.strokeRect(x, y, w, h);
      ctx.restore();
    }
  }, [raster, view.k]);

  useEffect(() => {
    paint();
  });

  const commitLabel = useCallback(() => {
    const current = labelRef.current;
    if (!current) return;
    setLabel(null);
    if (current.text.trim() === "") return;
    setOps((prev) => [
      ...prev,
      {
        kind: "shape",
        shape: {
          kind: "text",
          color,
          size: textSizeFor(size),
          at: current.at,
          text: current.text,
        },
      },
    ]);
  }, [color, size, setLabel]);

  const toPicture = (e: { clientX: number; clientY: number }): Pt => {
    const canvas = canvasRef.current;
    if (!canvas) return { x: 0, y: 0 };
    const r = canvas.getBoundingClientRect();
    const sx = r.width > 0 ? canvas.width / r.width : 1;
    const sy = r.height > 0 ? canvas.height / r.height : 1;
    return { x: (e.clientX - r.left) * sx, y: (e.clientY - r.top) * sy };
  };

  const screenPerPixel = (): number => {
    const canvas = canvasRef.current;
    if (!canvas) return 1;
    const w = canvas.getBoundingClientRect().width;
    return w > 0 ? w / canvas.width : 1;
  };

  const onPointerDown = (e: ReactPointerEvent<HTMLCanvasElement>) => {
    if (e.button !== 0 || !raster) return;
    const now = performance.now();
    if (e.pointerType === "pen") {
      lastPenAtRef.current = now;
      // A pen that lands while a finger is drawing takes over: the finger
      // was the palm.
      if (draftPointerRef.current?.type === "touch") {
        draftRef.current = null;
        draftPointerRef.current = null;
      }
    } else if (
      e.pointerType === "touch" &&
      (draftPointerRef.current?.type === "pen" ||
        now - lastPenAtRef.current < PALM_AFTER_PEN_MS)
    ) {
      return;
    }
    if (draftRef.current) return;
    // The press must not move the focus: a press with the text tool opens a
    // label whose field takes the focus, and the mousedown that follows would
    // hand it to the dialog and close the label empty.
    e.preventDefault();
    commitLabel();
    const p = toPicture(e);
    const width = strokeWidthFor(size);
    if (tool === "text") {
      setLabel({ at: p, text: "" });
      return;
    }
    e.currentTarget.setPointerCapture?.(e.pointerId);
    draftPointerRef.current = { id: e.pointerId, type: e.pointerType ?? "" };
    if (tool === "crop") {
      draftRef.current = { kind: "crop", a: p, b: p };
    } else if (tool === "pen") {
      // A pen tracks the pressure of every point, even when the driver has
      // no reading at the press itself: seeded at the resting press, so a
      // pen whose first report is 0 does not draw its whole stroke flat.
      const pressure =
        e.pointerType === "pen" ? (penPressure(e) ?? 0.5) : undefined;
      draftRef.current = {
        kind: "shape",
        shape:
          pressure === undefined
            ? { kind: "pen", color, width, points: [p] }
            : { kind: "pen", color, width, points: [p], pressures: [pressure] },
      };
    } else {
      draftRef.current = {
        kind: "shape",
        shape: { kind: tool, color, width, a: p, b: p },
      };
    }
    setTick((n) => n + 1);
  };

  const onPointerMove = (e: ReactPointerEvent<HTMLCanvasElement>) => {
    if (e.pointerType === "pen") lastPenAtRef.current = performance.now();
    const draft = draftRef.current;
    if (!draft) return;
    const owner = draftPointerRef.current;
    if (owner && owner.id !== e.pointerId) return;
    const p = toPicture(e);
    if (draft.kind === "crop") {
      draftRef.current = { ...draft, b: p };
    } else if (draft.shape.kind === "pen") {
      // A pen reports more points than there are frames: the browser hands
      // the ones between two events over as coalesced samples.
      const pts = [...draft.shape.points];
      const pressures = draft.shape.pressures
        ? [...draft.shape.pressures]
        : null;
      const k = screenPerPixel();
      for (const sample of moveSamples(e)) {
        const q = toPicture(sample);
        const last = pts[pts.length - 1];
        if (last && Math.hypot(q.x - last.x, q.y - last.y) * k < 0.5) continue;
        pts.push(q);
        if (pressures) {
          pressures.push(
            penPressure(sample) ?? pressures[pressures.length - 1] ?? 0.5,
          );
        }
      }
      if (pts.length === draft.shape.points.length) return;
      draftRef.current = {
        kind: "shape",
        shape: pressures
          ? { ...draft.shape, points: pts, pressures }
          : { ...draft.shape, points: pts },
      };
    } else if (draft.shape.kind !== "text") {
      draftRef.current = { kind: "shape", shape: { ...draft.shape, b: p } };
    }
    paint();
  };

  const onPointerUp = (e: ReactPointerEvent<HTMLCanvasElement>) => {
    const owner = draftPointerRef.current;
    if (owner && owner.id !== e.pointerId) return;
    onPointerMove(e);
    const draft = draftRef.current;
    draftRef.current = null;
    draftPointerRef.current = null;
    if (!draft) return;
    if (draft.kind === "crop") {
      const crop = cropFor(size, draft.a, draft.b);
      if (crop) setOps((prev) => [...prev, crop]);
      setTick((n) => n + 1);
      return;
    }
    const s = draft.shape;
    if (
      s.kind !== "pen" &&
      s.kind !== "text" &&
      !dragDrew(s.a, s.b, screenPerPixel())
    ) {
      setTick((n) => n + 1);
      return;
    }
    setOps((prev) => [...prev, { kind: "shape", shape: s }]);
  };

  const onPointerCancel = (e: ReactPointerEvent<HTMLCanvasElement>) => {
    const owner = draftPointerRef.current;
    if (owner && owner.id !== e.pointerId) return;
    draftRef.current = null;
    draftPointerRef.current = null;
    setTick((n) => n + 1);
  };

  const pickTool = (next: MarkupTool) => {
    commitLabel();
    setTool(next);
  };

  const undo = () => {
    if (labelRef.current) {
      setLabel(null);
      return;
    }
    setOps((prev) => prev.slice(0, -1));
  };

  const rotate = () => {
    commitLabel();
    setOps((prev) => [...prev, { kind: "rotate" }]);
  };

  const done = async () => {
    if (!picture || busy) return;
    let final = ops;
    const label = labelRef.current;
    if (label && label.text.trim() !== "") {
      final = [
        ...ops,
        {
          kind: "shape",
          shape: {
            kind: "text",
            color,
            size: textSizeFor(size),
            at: label.at,
            text: label.text,
          },
        },
      ];
    }
    setBusy(true);
    try {
      const canvas = renderPicture(picture.source, baseSize, final);
      let blob: Blob;
      try {
        blob = await encode(canvas);
      } finally {
        releaseCanvas(canvas);
      }
      if (closedRef.current) return;
      closedRef.current = true;
      onDone(new File([blob], markedUpName(file.name), { type: "image/png" }));
    } catch {
      if (closedRef.current) return;
      setBusy(false);
      setFailed(true);
    }
  };

  useEscapeCloses(!label, cancel);

  const onKeyDown = (e: ReactKeyboardEvent<HTMLDivElement>) => {
    if (e.key === "Tab") {
      // aria-modal promises the page behind is inert; the portal sits
      // outside the shell's DOM order, so Tab is wrapped by hand, as
      // ConfirmDialog does.
      const root = rootRef.current;
      if (!root) return;
      const items = Array.from(
        root.querySelectorAll<HTMLElement>("button:not([disabled]), input"),
      );
      const first = items[0];
      const last = items[items.length - 1];
      if (!first || !last) {
        e.preventDefault();
        return;
      }
      const active = document.activeElement;
      if (
        e.shiftKey &&
        (active === first || active === root || !root.contains(active))
      ) {
        e.preventDefault();
        last.focus();
      } else if (
        !e.shiftKey &&
        (active === last || active === root || !root.contains(active))
      ) {
        e.preventDefault();
        first.focus();
      }
      return;
    }
    if (e.target instanceof HTMLInputElement || e.nativeEvent.isComposing)
      return;
    if (e.key === "Enter") {
      // Enter is Done only from the editor itself: on a focused button it
      // is that button's own press.
      if (e.target !== e.currentTarget || e.repeat) return;
      e.preventDefault();
      void done();
      return;
    }
    // Shortcuts go by the key, not the character, so they work in any
    // layout and with caps lock on.
    const letter =
      /^Key([A-Z])$/.exec(e.code)?.[1]?.toLowerCase() ?? e.key.toLowerCase();
    if ((e.ctrlKey || e.metaKey) && letter === "z") {
      e.preventDefault();
      undo();
      return;
    }
    if (!e.ctrlKey && !e.metaKey && !e.altKey) {
      const next = MARKUP_TOOL_KEYS[letter];
      if (next) {
        e.preventDefault();
        pickTool(next);
      }
    }
  };

  // A press on a control of the toolbar keeps the focus on the editor, so
  // Enter after it is still Done.
  const keepFocus = (e: { preventDefault: () => void }) => e.preventDefault();

  const toolTitle = (tl: MarkupTool) => {
    const key = Object.keys(MARKUP_TOOL_KEYS).find(
      (k) => MARKUP_TOOL_KEYS[k] === tl,
    );
    return `${t(`composer.markup.tool.${tl}`)} (${(key ?? "").toUpperCase()})`;
  };

  const toolButton = (tl: MarkupTool) => (
    <button
      key={tl}
      type="button"
      className={`markup-tool${tool === tl ? " is-active" : ""}`}
      aria-pressed={tool === tl}
      aria-label={t(`composer.markup.tool.${tl}`)}
      title={toolTitle(tl)}
      data-testid={`markup-tool-${tl}`}
      onMouseDown={keepFocus}
      onClick={() => pickTool(tl)}
    >
      <MarkupToolIcon tool={tl} />
    </button>
  );

  return createPortal(
    <div
      ref={rootRef}
      className="markup-dialog"
      role="dialog"
      aria-modal="true"
      aria-label={t("composer.markup.title")}
      tabIndex={-1}
      onKeyDown={onKeyDown}
      // A file dropped on the editor must not reach the composer under it
      // (React bubbles through the portal) or the browser's own navigation.
      onDragOver={(e) => {
        e.preventDefault();
        e.stopPropagation();
      }}
      onDrop={(e) => {
        e.preventDefault();
        e.stopPropagation();
      }}
      data-testid="markup-dialog"
    >
      <div className="markup-stage" ref={stageRef}>
        {failed ? (
          <p className="markup-message" role="alert">
            {t("composer.markup.failed")}
          </p>
        ) : null}
        {raster && !failed ? (
          <div
            className="markup-picture"
            style={{
              width: `${view.w}px`,
              height: `${view.h}px`,
              left: `${view.x}px`,
              top: `${view.y}px`,
            }}
          >
            <canvas
              ref={canvasRef}
              className={`markup-canvas markup-canvas--${tool}`}
              width={size.w}
              height={size.h}
              data-testid="markup-canvas"
              onPointerDown={onPointerDown}
              onPointerMove={onPointerMove}
              onPointerUp={onPointerUp}
              onPointerCancel={onPointerCancel}
            />
            {label ? (
              <input
                className="markup-label-input"
                data-testid="markup-label-input"
                aria-label={t("composer.markup.tool.text")}
                autoFocus
                value={label.text}
                style={{
                  left: `${label.at.x * view.k}px`,
                  top: `${label.at.y * view.k}px`,
                  font: labelFont(textSizeFor(size) * view.k),
                  color,
                }}
                onChange={(e) =>
                  setLabel({ at: label.at, text: e.target.value })
                }
                onBlur={commitLabel}
                onKeyDown={(e) => {
                  if (e.nativeEvent.isComposing || e.keyCode === 229) return;
                  if (e.key === "Enter") {
                    e.preventDefault();
                    e.stopPropagation();
                    commitLabel();
                    rootRef.current?.focus();
                  } else if (e.key === "Escape") {
                    e.preventDefault();
                    e.stopPropagation();
                    setLabel(null);
                    rootRef.current?.focus();
                  }
                }}
              />
            ) : null}
          </div>
        ) : null}
      </div>
      <div className="markup-bar">
        <button
          type="button"
          className="markup-action markup-action--cancel"
          data-testid="markup-cancel"
          onClick={cancel}
        >
          {t("common.cancel")}
        </button>
        <div
          className="markup-tools"
          role="toolbar"
          aria-label={t("composer.markup.tools")}
        >
          {DRAW_TOOLS.map(toolButton)}
          <span className="markup-sep" aria-hidden="true" />
          {toolButton("crop")}
          <button
            type="button"
            className="markup-tool"
            aria-label={t("composer.markup.rotate")}
            title={t("composer.markup.rotate")}
            data-testid="markup-rotate"
            onMouseDown={keepFocus}
            disabled={!raster}
            onClick={rotate}
          >
            <RotateIcon />
          </button>
          <button
            type="button"
            className="markup-tool"
            aria-label={t("composer.markup.undo")}
            title={`${t("composer.markup.undo")} (Ctrl+Z)`}
            data-testid="markup-undo"
            onMouseDown={keepFocus}
            disabled={ops.length === 0 && !label}
            onClick={undo}
          >
            <UndoIcon />
          </button>
          <span className="markup-sep" aria-hidden="true" />
          <span
            className="markup-swatches"
            role="group"
            aria-label={t("composer.markup.colors")}
          >
            {MARKUP_PALETTE.map((c, i) => (
              <button
                key={c.id}
                type="button"
                className={`markup-swatch${i === colorIndex ? " is-active" : ""}`}
                style={{ background: c.color }}
                aria-pressed={i === colorIndex}
                aria-label={t(`composer.markup.color.${c.id}`)}
                title={t(`composer.markup.color.${c.id}`)}
                data-testid={`markup-color-${c.id}`}
                onMouseDown={keepFocus}
                onClick={() => setColorIndex(i)}
              />
            ))}
          </span>
        </div>
        <button
          type="button"
          className="markup-action markup-action--done"
          data-testid="markup-done"
          disabled={!raster || busy}
          onClick={() => void done()}
        >
          {t("composer.markup.done")}
        </button>
      </div>
    </div>,
    document.body,
  );
}
