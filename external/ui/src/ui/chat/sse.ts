type SSEEvent = { event: string; data: string };

export function parseSSEBlocks(chunk: string, carry: { buf: string }): SSEEvent[] {
  const text = carry.buf + chunk;
  const parts = text.split(/\n\n+/);
  carry.buf = parts.pop() || '';
  const events: SSEEvent[] = [];

  for (const blk of parts) {
    let evName = '';
    const dataLines: string[] = [];
    blk.split('\n').forEach((ln) => {
      if (ln.startsWith('event:')) {
        evName = ln.slice(6).trim();
        return;
      }
      if (ln.startsWith('data:')) {
        dataLines.push(ln.slice(5).trim());
      }
    });
    events.push({ event: evName, data: dataLines.join('\n') });
  }

  return events;
}

