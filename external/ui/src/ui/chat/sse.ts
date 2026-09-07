type SSEEvent = { event: string; data: string; id: string };

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
      if (ln.startsWith("data:")) {
        dataLines.push(ln.slice(5).trim());
      }
    });
    events.push({ event: evName, data: dataLines.join("\n"), id: evId });
  }

  return events;
}
