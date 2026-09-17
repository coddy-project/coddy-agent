// The SharedWorker every tab of one environment connects to for GET /coddy/events
// (see sharedServerEvents.ts). Vite builds it as events-worker.js next to app.js.

import { createEventsWorkerHost } from "./eventsWorkerHost";
import type { PortLike } from "./serverEventsHub";

const host = createEventsWorkerHost({
  // The worker's own fetch: the page's remote shim does not exist here, so a
  // remote environment's address and token arrive with each tab's hello.
  fetchImpl: (input, init) => fetch(input, init),
});

const scope = self as unknown as {
  addEventListener(
    type: "connect",
    fn: (event: { ports: readonly unknown[] }) => void,
  ): void;
};

scope.addEventListener("connect", (event) => {
  const port = event.ports[0];
  if (port) host.connect(port as PortLike);
});
