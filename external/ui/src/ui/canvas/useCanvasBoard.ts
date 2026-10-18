import { useCallback, useEffect, useRef, useState } from "react";
import { HttpError } from "../files/api";
import { readBoard, type CanvasBoard } from "./api";

/** How often the open canvas reads its board again: a background build can
 *  rewrite a frame's files after the last tool call, and the stamps say so. */
export const CANVAS_POLL_MS = 4000;

export interface CanvasBoardState {
  /** The chat's board; null until read, or when the chat has none. */
  board: CanvasBoard | null;
  /** False once the server answered that it has no canvas (tools.canvas off,
   *  an older server): the view button stays away. */
  available: boolean;
  /** Reads the board again now. */
  reload: () => void;
}

/**
 * The board of the chat on screen, read when the chat opens, whenever epoch
 * moves (App bumps it on canvas_changed, turn_ended and a finished tool call)
 * and, while the canvas window is open and the page visible, every
 * CANVAS_POLL_MS. A late answer for another chat or an older read is dropped.
 */
export function useCanvasBoard(
  sessionId: string,
  opts: { open: boolean; epoch: number },
): CanvasBoardState {
  const [board, setBoard] = useState<CanvasBoard | null>(null);
  const [available, setAvailable] = useState(true);
  const generation = useRef(0);
  const sid = sessionId.trim();

  const read = useCallback(() => {
    if (!sid) return;
    const gen = ++generation.current;
    readBoard(sid).then(
      (b) => {
        if (gen !== generation.current) return;
        setAvailable(true);
        setBoard(b);
      },
      (err: unknown) => {
        if (gen !== generation.current) return;
        if (err instanceof HttpError && err.status === 404) {
          setAvailable(false);
          setBoard(null);
        }
        // Anything else (a server restarting) keeps what is on screen.
      },
    );
  }, [sid]);

  // Another chat starts from nothing.
  useEffect(() => {
    generation.current++;
    setBoard(null);
    setAvailable(true);
  }, [sid]);

  useEffect(() => {
    read();
  }, [read, opts.epoch]);

  useEffect(() => {
    if (!opts.open || !sid) return undefined;
    const tick = () => {
      if (document.visibilityState === "visible") read();
    };
    const timer = window.setInterval(tick, CANVAS_POLL_MS);
    const onVisible = () => {
      if (document.visibilityState === "visible") read();
    };
    document.addEventListener("visibilitychange", onVisible);
    window.addEventListener("focus", onVisible);
    return () => {
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", onVisible);
      window.removeEventListener("focus", onVisible);
    };
  }, [opts.open, sid, read]);

  return { board, available, reload: read };
}
