import { useCallback, useEffect, useRef, useState } from "react";
import { readBoard, type CanvasBoard } from "./api";

/** How often an open canvas reads its board again: a background build can
 *  rewrite a frame's files after the last tool call, and the stamps say so. */
export const CANVAS_POLL_MS = 4000;

/** A board read again is kept as it was while nothing on it changed, so the
 *  frames and nodes are not drawn again every poll; the frames' addresses,
 *  which every read signs anew, are taken this often all the same, well
 *  ahead of their renewal (CanvasFrame). */
export const CANVAS_ADDRESS_REFRESH_MS = 10 * 60 * 1000;

export interface CanvasBoardState {
  /** The board; null until read. */
  board: CanvasBoard | null;
  /** Why the board could not be read, "" while it could. */
  error: string;
  /** Reads the board again now. */
  reload: () => void;
}

/** What a read of the board says, without the frames' fresh signatures. */
export function boardFingerprint(b: CanvasBoard, now = Date.now()): string {
  return JSON.stringify([
    Math.floor(now / CANVAS_ADDRESS_REFRESH_MS),
    b.file,
    b.current,
    b.revision,
    b.stamp,
    b.error ?? "",
    b.frames.map((f) => {
      const { url: _url, expires_at: _exp, ...rest } = f;
      return rest;
    }),
    b.items,
    b.edges,
  ]);
}

/**
 * One .canvas file of the chat as a board, read when it opens, whenever epoch
 * moves (App bumps it on canvas_changed and a finished tool call) and, while
 * the page is visible, every CANVAS_POLL_MS. A late answer for another file or
 * an older read is dropped; an answer that changed nothing is not drawn again.
 */
export function useCanvasBoard(
  sessionId: string,
  file: string,
  epoch: number,
): CanvasBoardState {
  const [board, setBoard] = useState<CanvasBoard | null>(null);
  const [error, setError] = useState("");
  const generation = useRef(0);
  const fingerprint = useRef("");
  const sid = sessionId.trim();

  const read = useCallback(() => {
    if (!sid || !file) return;
    const gen = ++generation.current;
    readBoard(sid, file).then(
      (b) => {
        if (gen !== generation.current) return;
        setError("");
        const fp = boardFingerprint(b);
        if (fp === fingerprint.current) return;
        fingerprint.current = fp;
        setBoard(b);
      },
      (err: unknown) => {
        if (gen !== generation.current) return;
        // A server restarting keeps what is on screen; the reason shows.
        setError(err instanceof Error ? err.message : String(err));
      },
    );
  }, [sid, file]);

  // Another file starts from nothing.
  useEffect(() => {
    generation.current++;
    fingerprint.current = "";
    setBoard(null);
    setError("");
  }, [sid, file]);

  useEffect(() => {
    read();
  }, [read, epoch]);

  useEffect(() => {
    if (!sid || !file) return undefined;
    const tick = () => {
      if (document.visibilityState === "visible") read();
    };
    const timer = window.setInterval(tick, CANVAS_POLL_MS);
    document.addEventListener("visibilitychange", tick);
    window.addEventListener("focus", tick);
    return () => {
      window.clearInterval(timer);
      document.removeEventListener("visibilitychange", tick);
      window.removeEventListener("focus", tick);
    };
  }, [sid, file, read]);

  return { board, error, reload: read };
}
