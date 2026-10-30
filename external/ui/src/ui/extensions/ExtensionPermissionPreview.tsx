import {
  useEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
} from "react";
import { useExtensions } from "./ExtensionsProvider";
import {
  selectPermissionPreview,
  type PermissionPreview,
} from "./permissionPreviews";
import type { ExtensionToolCall, Owned, ToolView } from "./contributions";
import { parseMcpToolName } from "../messages/toolDisplayName";
import type { CoddyPermissionPayload } from "../chat/permissionTypes";

/** The extension's decoration of a permission card. The prompt and its
 * options stay the host's: the preview only draws under the host's own. */
export function ExtensionPermissionPreview(props: {
  payload: CoddyPermissionPayload;
}) {
  const context = useExtensions();
  return context ? (
    <RegisteredPreview {...props} runtime={context.runtime} />
  ) : null;
}

function RegisteredPreview({
  payload,
  runtime,
}: {
  payload: CoddyPermissionPayload;
  runtime: NonNullable<ReturnType<typeof useExtensions>>["runtime"];
}) {
  const { permissionPreviews } = useSyncExternalStore(
    runtime.subscribe,
    runtime.snapshot,
  );
  const name = payload.toolCall.title || payload.toolCall.kind || "";
  const parsed =
    parseMcpToolName(name) ?? (name ? { server: "", tool: name } : null);
  // A preview names the call in any spelling: the raw name, `server__tool`,
  // `mcp__server__tool` or just the tool part.
  const names = parsed
    ? [
        name,
        parsed.tool,
        ...(parsed.server
          ? [
              `${parsed.server}__${parsed.tool}`,
              `mcp__${parsed.server}__${parsed.tool}`,
            ]
          : []),
      ].filter(Boolean)
    : [];
  const preview = parsed
    ? selectPermissionPreview(permissionPreviews, names)
    : undefined;
  const call = useMemo(
    () =>
      parsed
        ? Object.freeze({
            sessionId: payload.sessionId,
            toolCallId: payload.toolCall.toolCallId,
            name,
            server: parsed.server,
            tool: parsed.tool,
            status: "pending",
            argsText: "",
            resultText: "",
            resultWasTruncated: false,
            permissionWaiting: true,
            durationMs: null,
            startedAtMs: null,
          } satisfies ExtensionToolCall)
        : null,
    [
      payload.sessionId,
      payload.toolCall.toolCallId,
      name,
      parsed?.server,
      parsed?.tool,
    ],
  );
  if (!preview || !call) return null;
  return (
    <div
      className="extension-permission-preview"
      data-extension-renderer={preview.key}
    >
      <MountedPreview
        key={preview.key}
        preview={preview}
        call={call}
        fail={(error) =>
          runtime.failPermissionPreview(preview.key, preview.owner, error)
        }
      />
    </div>
  );
}

function MountedPreview({
  preview,
  call,
  fail,
}: {
  preview: Owned<PermissionPreview>;
  call: ExtensionToolCall;
  fail: (error: unknown) => void;
}) {
  const element = useRef<HTMLDivElement>(null);
  const currentCall = useRef(call);
  currentCall.current = call;
  const failure = useRef(fail);
  failure.current = fail;
  const view = useRef<ToolView | undefined>(undefined);
  const mountedCall = useRef<ExtensionToolCall | null>(null);
  const cleanup = useRef<() => void>(() => {});
  const [generation, setGeneration] = useState(0);
  useEffect(() => {
    const el = element.current;
    if (!el) return;
    const dispose = () => {
      try {
        Promise.resolve(cleanup.current()).catch(failure.current);
      } catch (error) {
        failure.current(error);
      }
      cleanup.current = () => {};
      view.current = undefined;
      el.replaceChildren();
    };
    try {
      const result = preview.mount(el, currentCall.current, preview.api);
      mountedCall.current = currentCall.current;
      if (typeof result === "function") cleanup.current = result;
      else if (result && typeof result === "object" && !("then" in result)) {
        view.current = result;
        cleanup.current = () => result.dispose?.();
      } else if (result !== undefined) {
        Promise.resolve(result).catch(() => {});
        throw new Error("Permission preview mount must be synchronous");
      }
    } catch (error) {
      failure.current(error);
    }
    return dispose;
  }, [preview.key, preview.mount, preview.api, generation]);
  useEffect(() => {
    if (mountedCall.current === call) return;
    if (view.current?.update) {
      try {
        const result = view.current.update(call) as unknown;
        if (result && typeof result === "object" && "then" in result) {
          Promise.resolve(result).catch(() => {});
          throw new Error("Permission preview update must be synchronous");
        }
        mountedCall.current = call;
      } catch (error) {
        failure.current(error);
      }
    } else setGeneration((value) => value + 1);
  }, [call]);
  return <div ref={element} className="extension-permission-preview-view" />;
}
