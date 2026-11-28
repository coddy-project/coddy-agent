import { useEffect, useRef } from "react";
import { useExtensions } from "./ExtensionsProvider";
import type { ExtensionAnchorName } from "./anchors";

/** An empty box of the host surface an extension may draw into. It registers
 * itself with the anchor registry on mount and clears on unmount, so
 * `ui.anchor(name)` and `ui.onAnchor` see the surface come and go.
 *
 * Composer control anchors also attach the registered-control host. The mixed
 * composer pipeline can give one control its own box (`controlKey`) and keep
 * the generic anchor out of the registry (`registerAnchor: false`). */
export function ExtensionAnchor(props: {
  name: ExtensionAnchorName;
  className?: string;
  controlKey?: string;
  registerAnchor?: boolean;
  mountComposerControls?: boolean;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const runtime = useExtensions()?.runtime;
  const {
    name,
    className,
    controlKey,
    registerAnchor = true,
    mountComposerControls = true,
  } = props;
  useEffect(() => {
    const el = ref.current;
    if (!el || !runtime) return;
    if (registerAnchor) runtime.anchors.attach(name, el);
    const controller = new AbortController();
    const composerPlacement =
      name === "composer.actions"
        ? "actions"
        : name === "composer.tabs"
          ? "tabs"
          : null;
    const attached =
      mountComposerControls && composerPlacement
        ? runtime.composerHost.attach(
            composerPlacement,
            el,
            controller.signal,
            controlKey,
          )
        : null;
    return () => {
      attached?.remove();
      controller.abort();
      if (registerAnchor) runtime.anchors.detach(name, el);
    };
  }, [controlKey, mountComposerControls, name, registerAnchor, runtime]);
  return (
    <div
      ref={ref}
      className={
        className ? `extension-anchor ${className}` : "extension-anchor"
      }
      data-extension-anchor={registerAnchor ? name : undefined}
      data-extension-composer-control={controlKey}
    />
  );
}
