import {
  isTabMessage,
  ServerEventsHub,
  type HubMessage,
  type PortLike,
} from "./serverEventsHub";

/**
 * createEventsWorkerHost is the body of the SharedWorker behind the shared events
 * stream (eventsWorker.ts), kept apart from the worker global so tests can run it.
 *
 * Each tab connects a port and says hello with where the stream is; the first hello
 * opens the connection and later ones join it. The connection closes when the last
 * tab says bye. A tab that vanished without one keeps its port on the list, which
 * costs a message nobody reads until the worker ends with the last open tab.
 */
export function createEventsWorkerHost(deps: {
  fetchImpl: typeof fetch;
  sleep?: (ms: number) => Promise<void>;
}) {
  const ports = new Map<string, PortLike>();
  let stream: AbortController | null = null;
  const hub = new ServerEventsHub((message: HubMessage) => {
    if (message.kind !== "refused" && message.to !== undefined) {
      ports.get(message.to)?.postMessage(message);
      return;
    }
    for (const port of ports.values()) port.postMessage(message);
  });

  return {
    connect(port: PortLike): void {
      port.addEventListener("message", (event) => {
        const message = event.data;
        if (!isTabMessage(message)) return;
        if (message.kind === "bye") {
          ports.delete(message.tab);
          if (ports.size === 0 && stream) {
            stream.abort();
            stream = null;
          }
          return;
        }
        ports.set(message.tab, port);
        if (!stream && message.url) {
          stream = new AbortController();
          void hub.stream({
            signal: stream.signal,
            url: message.url,
            ...(message.headers ? { headers: message.headers } : {}),
            fetchImpl: deps.fetchImpl,
            ...(deps.sleep ? { sleep: deps.sleep } : {}),
          });
        }
        hub.hello(message.tab);
      });
      port.start();
    },
  };
}
