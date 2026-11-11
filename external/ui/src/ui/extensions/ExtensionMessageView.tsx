import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import { useExtensions } from "./ExtensionsProvider";
import {
  selectMessageRenderer,
  type ExtensionMessageItem,
  type MessageView,
} from "./messageRenderers";
import type { Owned } from "./contributions";
import type { MessageRenderer } from "./messageRenderers";

/** Draws one transcript row through a registered message renderer. Falls
 * back to `fallback` (dropping an ext_frame row when it is absent) when no
 * renderer matches, and reports a renderer that throws. */
export function ExtensionMessageView(props: {
  item: ExtensionMessageItem;
  fallback?: React.ReactNode;
}) {
  const context = useExtensions();
  return context ? (
    <RegisteredMessage {...props} runtime={context.runtime} />
  ) : (
    (props.fallback ?? null)
  );
}

function RegisteredMessage(props: {
  item: ExtensionMessageItem;
  fallback?: React.ReactNode;
  runtime: NonNullable<ReturnType<typeof useExtensions>>["runtime"];
}) {
  const { messageRenderers } = useSyncExternalStore(
    props.runtime.subscribe,
    props.runtime.snapshot,
  );
  const renderer = selectMessageRenderer(
    messageRenderers,
    props.item.type,
    props.item.event,
  );
  if (!renderer) return props.fallback ?? null;
  return (
    <div
      className="message-row extension-message-row"
      data-row-id={props.item.itemId}
      data-extension-renderer={renderer.key}
    >
      <MountedMessage
        key={renderer.key}
        renderer={renderer}
        item={props.item}
        fail={(error) =>
          props.runtime.failMessageRenderer(renderer.key, renderer.owner, error)
        }
      />
    </div>
  );
}

function MountedMessage({
  renderer,
  item,
  fail,
}: {
  renderer: Owned<MessageRenderer>;
  item: ExtensionMessageItem;
  fail: (error: unknown) => void;
}) {
  const element = useRef<HTMLDivElement>(null);
  const currentItem = useRef(item);
  currentItem.current = item;
  const failure = useRef(fail);
  failure.current = fail;
  const view = useRef<MessageView | undefined>(undefined);
  const mountedItem = useRef<ExtensionMessageItem | null>(null);
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
      const result = renderer.mount(el, currentItem.current, renderer.api);
      mountedItem.current = currentItem.current;
      if (typeof result === "function") cleanup.current = result;
      else if (result && typeof result === "object" && !("then" in result)) {
        view.current = result;
        cleanup.current = () => result.dispose?.();
      } else if (result !== undefined) {
        Promise.resolve(result).catch(() => {});
        throw new Error("Message renderer mount must be synchronous");
      }
    } catch (error) {
      failure.current(error);
    }
    return dispose;
  }, [renderer.key, renderer.mount, renderer.api, generation]);
  useEffect(() => {
    if (mountedItem.current === item) return;
    if (view.current?.update) {
      try {
        const result = view.current.update(item) as unknown;
        if (result && typeof result === "object" && "then" in result) {
          Promise.resolve(result).catch(() => {});
          throw new Error("Message renderer update must be synchronous");
        }
        mountedItem.current = item;
      } catch (error) {
        failure.current(error);
      }
    } else setGeneration((value) => value + 1);
  }, [item]);
  return <div ref={element} className="extension-message-view" />;
}
