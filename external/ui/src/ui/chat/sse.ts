type SSEEvent = {
  event: string;
  data: string;
  id: string;
  /** How old the relay says a replayed frame is, in milliseconds. */
  ageMs?: number;
};

export function parseSSEBlocks(
  chunk: string,
  carry: { buf: string },
): SSEEvent[] {
  const text = carry.buf + chunk;
  const parts = text.split(/\n\n+/);
  carry.buf = parts.pop() || "";
  const events: SSEEvent[] = [];

  for (const blk of parts) {
    let evName = "";
    let evId = "";
    let ageMs: number | undefined;
    const dataLines: string[] = [];
    blk.split("\n").forEach((ln) => {
      if (ln.startsWith("event:")) {
        evName = ln.slice(6).trim();
        return;
      }
      // The composer relay numbers the frames it replays, so a reconnecting client can
      // ask to resume after the last one it saw.
      if (ln.startsWith("id:")) {
        evId = ln.slice(3).trim();
        return;
      }
      // A frame the relay replays, or one a lagging subscriber receives late, says
      // how old it is, so it can be dated when it happened.
      if (ln.startsWith("age:")) {
        const n = Number(ln.slice(4).trim());
        if (Number.isFinite(n) && n >= 0) ageMs = n;
        return;
      }
      if (ln.startsWith("data:")) {
        dataLines.push(ln.slice(5).trim());
      }
    });
    events.push({
      event: evName,
      data: dataLines.join("\n"),
      id: evId,
      ...(ageMs !== undefined ? { ageMs } : {}),
    });
  }

  return events;
}
