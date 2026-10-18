import { useCallback, useEffect, useRef, useState } from "react";
import type { PointerEvent as ReactPointerEvent } from "react";
import { useT } from "../i18n/I18nProvider";
import {
  frameBase,
  frameSrc,
  postReport,
  type CanvasConsoleEntry,
  type CanvasDevice,
  type CanvasFrame as Frame,
} from "./api";
import {
  bridgeCommand,
  keepConsole,
  parseBridgeMessage,
  staysInFrame,
  type PickedElement,
} from "./bridgeProtocol";
import type { Rect } from "./camera";

/** How long a load of the iframe waits for the bridge before it counts as
 *  the page having left the canvas. */
export const LEFT_CANVAS_MS = 3000;

/** An address that has less than this left is renewed with a reload. */
const RENEW_BEFORE_MS = 30 * 60 * 1000;

/** How long a frame's address lasts from its signing (CapabilityTTL of
 *  internal/canvas). The renewal is timed on this page's own clock from the
 *  moment the address arrived, never against the server's expires_at, so a
 *  clock that is off does not reload the frame on every read of the board. */
export const CANVAS_ADDRESS_TTL_MS = 12 * 3600 * 1000;

/** What a frame shows: its page under its root. */
function pageOf(f: Pick<Frame, "path" | "root">): string {
  return `${f.root}\n${f.path}`;
}

function renewAt(): number {
  return Date.now() + CANVAS_ADDRESS_TTL_MS - RENEW_BEFORE_MS;
}

/** At most one report a frame every this long, whatever the page sends. */
const REPORT_EVERY_MS = 500;

/** The iframe's sandbox: scripts, script-handled forms, alert and confirm.
 *  Never allow-same-origin, top navigation or popups. */
export const FRAME_SANDBOX = "allow-scripts allow-forms allow-modals";

export function sizeLabel(
  f: Pick<Frame, "device" | "width" | "height">,
): string {
  return f.device
    ? `${f.device} ${f.width}x${f.height}`
    : `${f.width}x${f.height}`;
}

export function frameTitle(f: Pick<Frame, "title" | "path">): string {
  return f.title || f.path.split("/").pop() || f.path;
}

interface Props {
  sessionId: string;
  frame: Frame;
  /** Where the frame is drawn, in the board's screen coordinates. */
  screen: Rect;
  scale: number;
  mode: "interact" | "comment";
  focused: boolean;
  onPick: (frame: Frame, element: PickedElement, screenRect: Rect) => void;
  onEscape: () => void;
  onMove: (frame: Frame, x: number, y: number) => void;
  /** A drag of the title bar in progress, in screen pixels; 0, 0 when it ends. */
  onDragging?: (frame: Frame, dx: number, dy: number) => void;
  onResize: (frame: Frame, device: CanvasDevice) => void;
  onRemove: (frame: Frame) => void;
  /** The page navigated away from the canvas: revoke its address. */
  onLeft: (frame: Frame) => void;
}

/**
 * One frame on the board: a title bar of constant size above the page, and
 * the page in a sandboxed iframe at its own size, scaled with the board. The
 * frame keeps its address until its files change (the stamp moves), it is
 * rotated, or the address nears its end; then it reloads with a fresh one.
 */
export function CanvasFrame(props: Props) {
  const { t, tp } = useT();
  const { frame, screen, scale } = props;
  const iframeRef = useRef<HTMLIFrameElement | null>(null);
  const [loaded, setLoaded] = useState<{
    src: string;
    url: string;
    page: string;
    stamp: string;
    gen: number;
    renewAt: number;
    seq: number;
  }>(() => ({
    src: frameSrc(frame.url),
    url: frame.url,
    page: pageOf(frame),
    stamp: frame.stamp,
    gen: frame.gen,
    renewAt: renewAt(),
    seq: 0,
  }));
  const [left, setLeft] = useState(false);
  const [errors, setErrors] = useState(0);
  const [menuOpen, setMenuOpen] = useState(false);
  const [drag, setDrag] = useState<{ dx: number; dy: number } | null>(null);

  // Bridge bookkeeping, outside React state: messages come fast.
  const bridge = useRef({
    loadGen: 0,
    readyCount: 0,
    readyAtLastLoad: 0,
    helloed: false,
    title: "",
    loadedFlag: false,
    console: [] as CanvasConsoleEntry[],
    dirty: false,
  });
  // Two watches for a page leaving the canvas: a load of the iframe that
  // brings no bridge, and a document that said it is leaving with no next one
  // saying ready. Only a ready ends either.
  const loadTimer = useRef<number | null>(null);
  const leavingTimer = useRef<number | null>(null);
  // Set while the window itself reloads the frame (a new stamp, Reload, Back
  // to the page): the document going away then is ours, not the page's.
  const ownReload = useRef(false);
  const propsRef = useRef(props);
  propsRef.current = props;
  // The address the frame loaded, read by the message handler as it is now.
  const baseRef = useRef("");
  baseRef.current = frameBase(loaded.src);

  // A new stamp, a new generation or an address near its end: reload. A
  // page that left the canvas is not brought back by itself - only Back to
  // the page does that - so a page that leaves again cannot loop.
  useEffect(() => {
    if (left) return;
    // Renewed only with an address the board signed since: the same one
    // again would end as soon.
    const nearEnd = Date.now() >= loaded.renewAt && frame.url !== loaded.url;
    // The frame pointed at another page or root keeps its stamp and its
    // generation, and its page changes all the same.
    if (
      frame.stamp !== loaded.stamp ||
      frame.gen !== loaded.gen ||
      pageOf(frame) !== loaded.page ||
      nearEnd
    ) {
      ownReload.current = true;
      setLoaded((cur) => ({
        src: frameSrc(frame.url),
        url: frame.url,
        page: pageOf(frame),
        stamp: frame.stamp,
        gen: frame.gen,
        renewAt: renewAt(),
        seq: cur.seq + 1,
      }));
      setLeft(false);
    }
  }, [
    left,
    frame.stamp,
    frame.gen,
    frame.url,
    frame.path,
    frame.root,
    loaded.stamp,
    loaded.gen,
    loaded.url,
    loaded.page,
    loaded.renewAt,
  ]);

  const post = useCallback((msg: Record<string, unknown>) => {
    const win = iframeRef.current?.contentWindow;
    if (!win) return;
    try {
      // An opaque-origin frame accepts "*" and nothing else.
      win.postMessage(msg, "*");
    } catch {
      /* the frame went away */
    }
  }, []);

  // A report, coalesced: the page cannot make the window flood the server.
  useEffect(() => {
    const timer = window.setInterval(() => {
      const b = bridge.current;
      if (!b.dirty) return;
      b.dirty = false;
      void postReport(props.sessionId, frame.id, {
        stamp: loaded.stamp,
        loaded: b.loadedFlag,
        title: b.title,
        console: b.console,
      });
    }, REPORT_EVERY_MS);
    return () => window.clearInterval(timer);
  }, [props.sessionId, frame.id, loaded.stamp]);

  // Put away (the window closing, the frame removed): say so, best effort.
  useEffect(() => {
    const sid = props.sessionId;
    const id = frame.id;
    return () => {
      const b = bridge.current;
      void postReport(
        sid,
        id,
        { stamp: "", loaded: b.loadedFlag, closed: true, console: [] },
        true,
      );
    };
  }, [props.sessionId, frame.id]);

  useEffect(() => {
    const onMessage = (ev: MessageEvent) => {
      const win = iframeRef.current?.contentWindow;
      if (!win) return;
      const msg = parseBridgeMessage(ev.data);
      if (!msg) return;
      if (ev.source !== win) {
        // A document being discarded posts with no source: only its
        // "leaving", naming this frame, is taken. A live page cannot post
        // without a source, so it cannot leave on another frame's behalf.
        if (
          ev.source !== null ||
          msg.type !== "leaving" ||
          msg.frame !== propsRef.current.frame.id
        )
          return;
      }
      const b = bridge.current;
      if (msg.type === "ready") {
        ownReload.current = false;
        for (const timer of [loadTimer, leavingTimer]) {
          if (timer.current !== null) {
            window.clearTimeout(timer.current);
            timer.current = null;
          }
        }
        b.loadGen += 1;
        b.readyCount += 1;
        b.helloed = true;
        b.loadedFlag = false;
        b.title = "";
        b.console = [];
        b.dirty = true;
        setErrors(0);
        post(
          bridgeCommand("hello", {
            gen: b.loadGen,
            frame: propsRef.current.frame.id,
          }),
        );
        post(bridgeCommand("mode", { mode: propsRef.current.mode }));
        return;
      }
      // A document that leaves before the window greeted it says gen 0: it is
      // the newest one, since every older one was greeted.
      const leavingUngreeted = msg.type === "leaving" && msg.gen === 0;
      if (msg.gen !== b.loadGen && !leavingUngreeted) return; // an older document
      switch (msg.type) {
        case "location":
          if (!staysInFrame(msg.href, baseRef.current)) {
            setLeft(true);
            propsRef.current.onLeft(propsRef.current.frame);
          }
          return;
        case "loaded":
          b.loadedFlag = true;
          b.title = msg.title;
          b.dirty = true;
          return;
        case "console":
          b.console = keepConsole(b.console, {
            level: msg.level,
            message: msg.message,
            ...(msg.source ? { source: msg.source } : {}),
            ...(msg.line ? { line: msg.line } : {}),
            ...(msg.column ? { column: msg.column } : {}),
          });
          b.dirty = true;
          if (msg.level === "error") setErrors((n) => n + 1);
          return;
        case "pick": {
          const p = propsRef.current;
          const r = msg.element.rect;
          p.onPick(p.frame, msg.element, {
            x: p.screen.x + r.x * p.scale,
            y: p.screen.y + r.y * p.scale,
            width: r.width * p.scale,
            height: r.height * p.scale,
          });
          return;
        }
        case "escape":
          propsRef.current.onEscape();
          return;
        case "leaving": {
          // The page navigates. Its next document must say ready, or it left.
          if (ownReload.current) return;
          const count = b.readyCount;
          if (leavingTimer.current !== null)
            window.clearTimeout(leavingTimer.current);
          leavingTimer.current = window.setTimeout(() => {
            leavingTimer.current = null;
            if (bridge.current.readyCount > count || ownReload.current) return;
            setLeft(true);
            propsRef.current.onLeft(propsRef.current.frame);
          }, LEFT_CANVAS_MS);
          return;
        }
      }
    };
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, [post]);

  useEffect(() => {
    if (bridge.current.helloed)
      post(bridgeCommand("mode", { mode: props.mode }));
  }, [props.mode, post]);

  useEffect(
    () => () => {
      for (const timer of [loadTimer, leavingTimer]) {
        if (timer.current !== null) window.clearTimeout(timer.current);
      }
    },
    [],
  );

  // Every load of the iframe must bring the bridge: one that does not is a
  // page that navigated somewhere else, and its address may have gone along.
  const onLoad = () => {
    const b = bridge.current;
    const before = b.readyAtLastLoad;
    b.readyAtLastLoad = b.readyCount;
    if (loadTimer.current !== null) window.clearTimeout(loadTimer.current);
    if (b.readyCount > before) return;
    loadTimer.current = window.setTimeout(() => {
      loadTimer.current = null;
      if (bridge.current.readyCount > before || ownReload.current) return;
      setLeft(true);
      propsRef.current.onLeft(propsRef.current.frame);
    }, LEFT_CANVAS_MS);
  };

  const reload = () => {
    ownReload.current = true;
    setLoaded((cur) => ({
      src: frameSrc(frame.url),
      url: frame.url,
      page: pageOf(frame),
      stamp: frame.stamp,
      gen: frame.gen,
      renewAt: renewAt(),
      seq: cur.seq + 1,
    }));
    setLeft(false);
  };

  // Dragging the title bar moves the frame on the board.
  const dragStart = useRef<{ x: number; y: number } | null>(null);
  const onBarDown = (e: ReactPointerEvent<HTMLDivElement>) => {
    if (e.button !== 0 || (e.target as HTMLElement).closest("button")) return;
    dragStart.current = { x: e.clientX, y: e.clientY };
    (e.currentTarget as HTMLDivElement).setPointerCapture?.(e.pointerId);
  };
  const onBarMove = (e: ReactPointerEvent<HTMLDivElement>) => {
    const s = dragStart.current;
    if (!s) return;
    setDrag({ dx: e.clientX - s.x, dy: e.clientY - s.y });
    props.onDragging?.(frame, e.clientX - s.x, e.clientY - s.y);
  };
  const onBarUp = (e: ReactPointerEvent<HTMLDivElement>) => {
    const s = dragStart.current;
    dragStart.current = null;
    if (!s) return;
    const dx = (e.clientX - s.x) / scale;
    const dy = (e.clientY - s.y) / scale;
    setDrag(null);
    props.onDragging?.(frame, 0, 0);
    if (Math.abs(dx) + Math.abs(dy) >= 2) {
      props.onMove(frame, Math.round(frame.x + dx), Math.round(frame.y + dy));
    }
  };

  const dx = drag?.dx ?? 0;
  const dy = drag?.dy ?? 0;
  const barWidth = Math.max(screen.width, 180);

  return (
    <>
      <div
        className={"canvas-frame-bar" + (props.focused ? " is-focused" : "")}
        style={{
          left: screen.x + dx,
          top: screen.y + dy - 30,
          width: barWidth,
        }}
        data-testid="canvas-frame-bar"
        data-frame-id={frame.id}
        onPointerDown={onBarDown}
        onPointerMove={onBarMove}
        onPointerUp={onBarUp}
        onPointerCancel={() => {
          dragStart.current = null;
          setDrag(null);
          props.onDragging?.(frame, 0, 0);
        }}
      >
        <span className="canvas-frame-title" title={frame.path}>
          {frameTitle(frame)}
        </span>
        <span className="canvas-frame-size">{sizeLabel(frame)}</span>
        {errors > 0 ? (
          <span
            className="canvas-frame-errors"
            data-testid="canvas-frame-errors"
            title={tp("canvas.frame.errors", errors)}
          >
            {errors}
          </span>
        ) : null}
        <span className="canvas-frame-actions">
          <span className="canvas-frame-menu-host">
            <button
              type="button"
              className="canvas-frame-btn"
              aria-label={t("canvas.frame.size")}
              title={t("canvas.frame.size")}
              aria-haspopup="menu"
              aria-expanded={menuOpen}
              onClick={() => setMenuOpen((v) => !v)}
            >
              <IconDevices />
            </button>
            {menuOpen ? (
              <>
                <button
                  type="button"
                  className="mode-menu-backdrop"
                  aria-hidden="true"
                  tabIndex={-1}
                  onMouseDown={(e) => {
                    e.preventDefault();
                    setMenuOpen(false);
                  }}
                />
                <div className="mode-menu canvas-frame-menu" role="menu">
                  {(["desktop", "tablet", "phone"] as const).map((d) => (
                    <button
                      key={d}
                      type="button"
                      role="menuitemradio"
                      aria-checked={frame.device === d}
                      className="mode-item files-menu-item"
                      onClick={() => {
                        setMenuOpen(false);
                        props.onResize(frame, d);
                      }}
                    >
                      <span className="files-menu-check" aria-hidden>
                        {frame.device === d ? "✓" : ""}
                      </span>
                      {t(`canvas.device.${d}`)}
                    </button>
                  ))}
                </div>
              </>
            ) : null}
          </span>
          <button
            type="button"
            className="canvas-frame-btn"
            data-testid="canvas-frame-reload"
            aria-label={t("canvas.frame.reload")}
            title={t("canvas.frame.reload")}
            onClick={reload}
          >
            <IconReload />
          </button>
          <button
            type="button"
            className="canvas-frame-btn"
            data-testid="canvas-frame-remove"
            aria-label={t("canvas.frame.remove")}
            title={t("canvas.frame.remove")}
            onClick={() => props.onRemove(frame)}
          >
            ×
          </button>
        </span>
      </div>
      <div
        className={
          "canvas-frame" +
          (props.focused ? " is-focused" : "") +
          (props.mode === "comment" ? " is-commenting" : "")
        }
        style={{
          left: screen.x + dx,
          top: screen.y + dy,
          width: screen.width,
          height: screen.height,
        }}
        data-testid="canvas-frame"
        data-frame-id={frame.id}
      >
        <iframe
          key={loaded.seq}
          ref={iframeRef}
          className="canvas-frame-page"
          title={frameTitle(frame)}
          src={loaded.src}
          sandbox={FRAME_SANDBOX}
          data-stamp={loaded.stamp}
          data-gen={loaded.gen}
          data-seq={loaded.seq}
          referrerPolicy="no-referrer"
          allow=""
          loading="lazy"
          style={{
            width: frame.width,
            height: frame.height,
            transform: `scale(${scale})`,
          }}
          onLoad={onLoad}
        />
        {left ? (
          <div
            className="canvas-frame-left"
            role="status"
            data-testid="canvas-frame-left"
          >
            <p>{t("canvas.frame.left")}</p>
            <button
              type="button"
              className="canvas-frame-back"
              onClick={reload}
            >
              {t("canvas.frame.back")}
            </button>
          </div>
        ) : null}
      </div>
    </>
  );
}

function IconReload() {
  return (
    <svg width="14" height="14" viewBox="0 0 16 16" aria-hidden="true">
      <path
        d="M13 8a5 5 0 1 1-1.46-3.54M13 2.5V5h-2.5"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

function IconDevices() {
  return (
    <svg width="14" height="14" viewBox="0 0 16 16" aria-hidden="true">
      <rect
        x="1.5"
        y="3"
        width="9"
        height="7"
        rx="1"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.4"
      />
      <rect
        x="11"
        y="5.5"
        width="3.5"
        height="7"
        rx="0.8"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.4"
      />
      <path
        d="M4 12.5h4"
        stroke="currentColor"
        strokeWidth="1.4"
        strokeLinecap="round"
      />
    </svg>
  );
}
