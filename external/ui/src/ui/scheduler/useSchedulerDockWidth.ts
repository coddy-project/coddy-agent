import { useLayoutEffect, useRef, useState, type RefObject } from "react";
import type { SchedulerEditorState } from "./useSchedulerJobs";

/**
 * Measures the scheduler dock cluster so `.shell-main` can reserve room for it
 * via the `--sched-dock-cluster-width` CSS variable.
 */
export function useSchedulerDockWidth({
  open,
  httpLinked,
  editor,
}: {
  open: boolean;
  httpLinked: boolean | null;
  editor: SchedulerEditorState;
}): {
  clusterRef: RefObject<HTMLDivElement | null>;
  widthPx: number;
} {
  const clusterRef = useRef<HTMLDivElement>(null);
  const [widthPx, setWidthPx] = useState(0);

  useLayoutEffect(() => {
    if (!open || httpLinked !== true) {
      setWidthPx(0);
      return;
    }
    const el = clusterRef.current;
    if (!el) {
      setWidthPx(0);
      return;
    }
    const ro = new ResizeObserver(() => {
      setWidthPx(Math.round(el.getBoundingClientRect().width));
    });
    ro.observe(el);
    setWidthPx(Math.round(el.getBoundingClientRect().width));
    return () => ro.disconnect();
  }, [open, httpLinked, editor]);

  return { clusterRef, widthPx };
}
