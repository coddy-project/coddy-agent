import {
  useEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
  type ReactNode,
} from "react";
import { useExtensions } from "./ExtensionsProvider";
import {
  selectRenderer,
  type ExtensionToolCall,
  type Owned,
  type ToolRenderer,
  type ToolView,
} from "./contributions";
import { parseMcpToolName } from "../messages/toolDisplayName";
import { Chevron } from "../components/Chevron";
import { useT } from "../i18n/I18nProvider";

export type ToolViewProps = {
  sessionId?: string | undefined;
  toolCallId: string;
  rowId?: string | undefined;
  title?: string | undefined;
  kind?: string | undefined;
  status: string;
  argsText?: string | undefined;
  resultText?: string | undefined;
  fullResultText?: string | undefined;
  resultWasTruncated?: boolean | undefined;
  permissionWaiting?: boolean | undefined;
  durationMs?: number | undefined;
  startedAtMs?: number | undefined;
  fallback: ReactNode;
  original: ReactNode;
};

export function ExtensionToolView(props: ToolViewProps) {
  const context = useExtensions();
  return context ? (
    <RegisteredTool {...props} runtime={context.runtime} />
  ) : (
    props.fallback
  );
}
function RegisteredTool(
  props: ToolViewProps & {
    runtime: NonNullable<ReturnType<typeof useExtensions>>["runtime"];
  },
) {
  const { renderers } = useSyncExternalStore(
    props.runtime.subscribe,
    props.runtime.snapshot,
  );
  // A built-in call (no `server__tool` spelling) matches renderers by its own
  // name with an empty server, so a renderer can take a host tool over.
  const name = props.title || props.kind || "";
  const parsed =
    parseMcpToolName(name) ?? (name ? { server: "", tool: name } : null);
  const renderer = parsed
    ? selectRenderer(renderers, parsed.server, parsed.tool)
    : undefined;
  const call = useMemo(
    () =>
      parsed
        ? Object.freeze({
            sessionId: props.sessionId ?? "",
            toolCallId: props.toolCallId,
            name,
            server: parsed.server,
            tool: parsed.tool,
            status: props.status,
            argsText: props.argsText ?? "",
            resultText: props.fullResultText ?? props.resultText ?? "",
            resultWasTruncated:
              props.resultWasTruncated === true &&
              props.fullResultText === undefined,
            permissionWaiting: props.permissionWaiting === true,
            durationMs: props.durationMs ?? null,
            startedAtMs: props.startedAtMs ?? null,
          })
        : null,
    [
      props.sessionId,
      props.toolCallId,
      props.title,
      props.kind,
      props.status,
      props.argsText,
      props.resultText,
      props.fullResultText,
      props.resultWasTruncated,
      props.permissionWaiting,
      props.durationMs,
      props.startedAtMs,
      parsed?.server,
      parsed?.tool,
    ],
  );
  const { t } = useT();
  if (!renderer || !call) return props.fallback;
  return (
    <div
      className="thinking-row coddy-tool-call-row extension-tool-call"
      data-kind={props.kind || ""}
      data-status={props.status}
      data-row-id={props.rowId}
      data-extension-renderer={renderer.key}
    >
      <MountedTool
        key={renderer.key}
        renderer={renderer}
        call={call}
        fail={(error) =>
          props.runtime.failRenderer(renderer.key, renderer.owner, error)
        }
      />
      <details className="extension-tool-original">
        <summary>
          <Chevron />
          {t("extensions.originalToolDetails")}
        </summary>
        {props.original}
      </details>
    </div>
  );
}

function MountedTool({
  renderer,
  call,
  fail,
}: {
  renderer: Owned<ToolRenderer>;
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
      const result = renderer.mount(el, currentCall.current, renderer.api);
      mountedCall.current = currentCall.current;
      if (typeof result === "function") cleanup.current = result;
      else if (result && typeof result === "object" && !("then" in result)) {
        if (
          (result.update !== undefined &&
            typeof result.update !== "function") ||
          (result.dispose !== undefined && typeof result.dispose !== "function")
        )
          throw new Error("Invalid tool view handle");
        view.current = result;
        cleanup.current = () => result.dispose?.();
      } else if (result !== undefined) {
        Promise.resolve(result).catch(() => {});
        throw new Error("Tool renderer mount must be synchronous");
      }
    } catch (error) {
      failure.current(error);
    }
    return dispose;
  }, [renderer.key, renderer.mount, renderer.api, generation]);
  useEffect(() => {
    if (mountedCall.current === call) return;
    if (view.current?.update) {
      try {
        const result = view.current.update(call) as unknown;
        if (result && typeof result === "object" && "then" in result) {
          Promise.resolve(result).catch(() => {});
          throw new Error("Tool renderer update must be synchronous");
        }
        mountedCall.current = call;
      } catch (error) {
        failure.current(error);
      }
    } else setGeneration((value) => value + 1);
  }, [call]);
  return <div ref={element} className="extension-tool-view" />;
}
