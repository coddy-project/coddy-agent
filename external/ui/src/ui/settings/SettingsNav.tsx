import { useCallback, useEffect, useRef, useState } from "react";
import type { SectionDescriptor } from "./settingsSections";

/**
 * SettingsNav lists the settings sections. CSS renders it as a vertical rail on
 * desktop and a horizontal strip on narrow shells. On the narrow strip the
 * scrollbar is hidden and edge arrows (shown only when there is more to scroll)
 * let the user page through the tabs by clicking.
 */
export function SettingsNav(props: {
  sections: SectionDescriptor[];
  active: string;
  onSelect: (id: string) => void;
}) {
  const { sections, active, onSelect } = props;
  const navRef = useRef<HTMLElement>(null);
  const [canLeft, setCanLeft] = useState(false);
  const [canRight, setCanRight] = useState(false);

  const update = useCallback(() => {
    const el = navRef.current;
    if (!el) {
      return;
    }
    const max = el.scrollWidth - el.clientWidth;
    setCanLeft(el.scrollLeft > 1);
    setCanRight(el.scrollLeft < max - 1);
  }, []);

  useEffect(() => {
    const el = navRef.current;
    if (!el) {
      return;
    }
    update();
    el.addEventListener("scroll", update, { passive: true });
    const ro =
      typeof ResizeObserver !== "undefined" ? new ResizeObserver(update) : null;
    ro?.observe(el);
    return () => {
      el.removeEventListener("scroll", update);
      ro?.disconnect();
    };
  }, [update, sections.length]);

  const scrollByDir = (dir: number) => {
    const el = navRef.current;
    if (!el) {
      return;
    }
    const amount = dir * Math.max(140, el.clientWidth * 0.6);
    const max = el.scrollWidth - el.clientWidth;
    el.scrollLeft = Math.max(0, Math.min(el.scrollLeft + amount, max));
    // Refresh the arrow enabled/disabled state immediately (don't depend on the
    // scroll event, which some embeds skip for programmatic scrolling).
    update();
  };

  return (
    <div className="settings-nav-wrap">
      <button
        type="button"
        className="settings-nav-arrow settings-nav-arrow-left"
        aria-label="Scroll sections left"
        data-testid="settings-nav-left"
        disabled={!canLeft}
        onClick={() => scrollByDir(-1)}
      >
        ‹
      </button>
      <nav className="settings-nav" aria-label="Settings sections" ref={navRef}>
        {sections.map((s) => (
          <button
            key={s.id}
            type="button"
            className={`settings-nav-item${s.id === active ? " is-active" : ""}`}
            aria-current={s.id === active ? "page" : undefined}
            data-testid={`settings-tab-${s.id}`}
            onClick={() => onSelect(s.id)}
          >
            {s.label}
          </button>
        ))}
      </nav>
      <button
        type="button"
        className="settings-nav-arrow settings-nav-arrow-right"
        aria-label="Scroll sections right"
        data-testid="settings-nav-right"
        disabled={!canRight}
        onClick={() => scrollByDir(1)}
      >
        ›
      </button>
    </div>
  );
}
