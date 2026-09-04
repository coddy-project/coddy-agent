import { useEffect, useState } from "react";
import { readNavRailCookie, writeNavRailCookie } from "./navRailCookie";

/**
 * Shell chrome layout state: XL viewport detection plus the wide/narrow
 * nav-rail label preference persisted in a cookie.
 */
export function useShellLayout(): {
  viewportXL: boolean;
  railLabelsWide: boolean;
  toggleRailWidth: () => void;
} {
  const [viewportXL, setViewportXL] = useState(false);
  const [railLabelsWide, setRailLabelsWide] = useState(false);

  useEffect(() => {
    const mq = window.matchMedia("(min-width: 1920px)");
    const apply = () => setViewportXL(mq.matches);
    apply();
    mq.addEventListener("change", apply);
    return () => mq.removeEventListener("change", apply);
  }, []);

  useEffect(() => {
    if (!viewportXL) {
      return;
    }
    const c = readNavRailCookie();
    setRailLabelsWide(c === "wide");
  }, [viewportXL]);

  const toggleRailWidth = () => {
    setRailLabelsWide((prev) => {
      const next = !prev;
      writeNavRailCookie(next ? "wide" : "narrow");
      return next;
    });
  };

  return { viewportXL, railLabelsWide, toggleRailWidth };
}
