/**
 * The reserved host channel. Today it reports which manifest permissions this
 * build honours and publishes extension events; the sessions/messages host API
 * of the specification lands on the same object, so an extension written
 * against it keeps working.
 */
export type ExtensionFireEvent = {
  /** Lowercase event type (`^[a-z][a-z0-9-]{0,63}$`); listeners address it
   * as `ext.<extension id>.<type>`. */
  type: string;
  /** One JSON value, delivered frozen. */
  data?: unknown;
};
export type ExtensionHostAPI = {
  api: 1;
  /** Names of the manifest permissions this host build honours. */
  capabilities: () => Promise<readonly string[]>;
  /** Publishes one host event: `extension_event` on the shared server stream
   * for hook handlers and an IDE bridge to answer, and `ext.<id>.<type>` to
   * the listeners of this page. Requires the `events.publish` scope. */
  fireEvent: (event: ExtensionFireEvent) => Promise<void>;
};

/** Permissions this phase understands. `ui.page` is implied by enablement;
 * `events.publish` gates `fireEvent` at the manifest level. */
export const HONORED_PERMISSIONS: readonly string[] = ["ui.page"];

const EVENT_TYPE_RE = /^[a-z][a-z0-9-]{0,63}$/;

export function extensionHost(
  owner: string,
  sha256: string,
  signal: AbortSignal,
  emitLocal: (type: string, data: unknown) => void,
): ExtensionHostAPI {
  return {
    api: 1,
    capabilities: async () => HONORED_PERMISSIONS,
    async fireEvent(event) {
      if (signal.aborted) throw new Error("Extension disposed");
      if (!event || !EVENT_TYPE_RE.test(event.type ?? ""))
        throw new Error("An event type must match ^[a-z][a-z0-9-]{0,63}$");
      let data: unknown = event.data ?? null;
      try {
        data = JSON.parse(JSON.stringify(data));
      } catch {
        throw new Error("Event data must be one JSON value");
      }
      const response = await fetch(
        `/coddy/extensions/${encodeURIComponent(owner)}/events`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ sha256, type: event.type, data }),
          signal,
        },
      );
      if (!response.ok) {
        let reason = `HTTP ${response.status}`;
        try {
          const body = (await response.json()) as { error?: unknown };
          if (body.error) reason = String(body.error);
        } catch {
          /* Status is enough for non-JSON errors. */
        }
        throw new Error(`Extension event: ${reason}`);
      }
      if (signal.aborted) throw new Error("Extension disposed");
      emitLocal(event.type, data);
    },
  };
}
