import { useCallback, useLayoutEffect, useRef } from "react";

/**
 * Returns an identity-stable wrapper that always invokes the latest committed
 * `fn`. Used for event handlers passed to `React.memo` message components, so
 * a re-render of the app shell does not break their prop equality.
 *
 * The ref is updated in a layout effect rather than during render, so an
 * interrupted (never committed) concurrent render cannot leak its closure
 * into the wrapper. The wrapper must not be called during render.
 */
export function useStableHandler<A extends unknown[], R>(
  fn: (...args: A) => R,
): (...args: A) => R {
  const ref = useRef(fn);
  useLayoutEffect(() => {
    ref.current = fn;
  });
  return useCallback((...args: A) => ref.current(...args), []);
}
