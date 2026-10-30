import { useEffect, useRef } from "react";
import { useExtensions } from "./ExtensionsProvider";
import type { ExtensionAnchorName } from "./anchors";

/** An empty box of the host surface an extension may draw into. It registers
 * itself with the anchor registry on mount and clears on unmount, so
 * `ui.anchor(name)` and `ui.onAnchor` see the surface come and go. */
export function ExtensionAnchor(props: {
  name: ExtensionAnchorName;
  className?: string;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const runtime = useExtensions()?.runtime;
  const { name, className } = props;
  useEffect(() => {
    const el = ref.current;
    if (!el || !runtime) return;
    runtime.anchors.attach(name, el);
    return () => runtime.anchors.detach(name, el);
  }, [name, runtime]);
  return (
    <div
      ref={ref}
      className={
        className ? `extension-anchor ${className}` : "extension-anchor"
      }
      data-extension-anchor={name}
    />
  );
}
