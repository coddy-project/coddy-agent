import { useCallback, useRef } from "react";

/**
 * Returns an identity-stable wrapper that always invokes the latest `fn`.
 * Used for event handlers passed to `React.memo` message components, so a
 * re-render of the app shell does not break their prop equality. The wrapper
 * must not be called during render.
 */
export function useStableHandler<A extends unknown[], R>(
  fn: (...args: A) => R,
): (...args: A) => R {
  const ref = useRef(fn);
  ref.current = fn;
  return useCallback((...args: A) => ref.current(...args), []);
}
