// Decode arbitrary UTF-8 chunk boundaries and multiline SSE data.
export async function consumeSSE(body, onEvent) {
  const reader = body.getReader(),
    decoder = new TextDecoder();
  let buffer = "";
  try {
    for (;;) {
      const { value, done } = await reader.read();
      buffer += decoder.decode(value, { stream: !done });
      buffer = buffer.replace(/\r\n/g, "\n");
      let boundary;
      while ((boundary = buffer.indexOf("\n\n")) >= 0) {
        const frame = buffer.slice(0, boundary);
        buffer = buffer.slice(boundary + 2);
        let event = "message";
        const data = [];
        for (const line of frame.split("\n")) {
          if (line.startsWith("event:")) event = line.slice(6).trim();
          if (line.startsWith("data:")) data.push(line.slice(5).trimStart());
        }
        if (data.length && data.join("\n") !== "[DONE]")
          await onEvent(event, JSON.parse(data.join("\n")));
      }
      if (buffer.length > 8 * 1024 * 1024)
        throw new Error("Chat event exceeds 8 MiB");
      if (done) return;
    }
  } finally {
    reader.releaseLock();
  }
}
