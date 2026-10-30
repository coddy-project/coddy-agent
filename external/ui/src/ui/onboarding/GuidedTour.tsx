import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useState,
  type CSSProperties,
} from "react";
import { useT } from "../i18n/I18nProvider";
import type { TourStep } from "./tourSteps";

type Rect = { top: number; left: number; width: number; height: number };

const BUBBLE_W = 320;
const BUBBLE_H = 168;
const GAP = 12;
const EDGE = 12;

function readRect(el: Element): Rect {
  const rect = el.getBoundingClientRect();
  return {
    top: rect.top,
    left: rect.left,
    width: rect.width,
    height: rect.height,
  };
}

export function GuidedTour(props: {
  open: boolean;
  steps: TourStep[];
  onClose: () => void;
}) {
  const { t } = useT();
  const { open, steps, onClose } = props;
  const [active, setActive] = useState<TourStep[]>([]);
  const [index, setIndex] = useState(0);
  const [rect, setRect] = useState<Rect | null>(null);

  useEffect(() => {
    if (!open) {
      setActive([]);
      setIndex(0);
      return;
    }
    const present = steps.filter((step) => document.querySelector(step.anchor));
    if (present.length === 0) {
      onClose();
      return;
    }
    setActive(present);
    setIndex(0);
  }, [open, steps, onClose]);

  const step = active[index] ?? null;

  useLayoutEffect(() => {
    if (!open || !step) {
      setRect(null);
      return;
    }
    const measure = () => {
      const el = document.querySelector(step.anchor);
      setRect(el ? readRect(el) : null);
    };
    measure();
    window.addEventListener("resize", measure, { passive: true });
    window.addEventListener("scroll", measure, {
      passive: true,
      capture: true,
    });
    return () => {
      window.removeEventListener("resize", measure);
      window.removeEventListener("scroll", measure, { capture: true });
    };
  }, [open, step]);

  const goNext = useCallback(() => {
    if (index >= active.length - 1) onClose();
    else setIndex((current) => current + 1);
  }, [index, active.length, onClose]);

  useEffect(() => {
    if (!open) return;
    const onKey = (event: KeyboardEvent) => {
      if (event.key === "Escape") onClose();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [open, onClose]);

  if (!open || !step) return null;

  const isLast = index >= active.length - 1;
  const vw = window.innerWidth;
  const vh = window.innerHeight;
  const hasRect = !!rect && (rect.width > 0 || rect.height > 0);
  const spotlightStyle: CSSProperties | undefined = hasRect
    ? {
        top: rect.top - 6,
        left: rect.left - 6,
        width: rect.width + 12,
        height: rect.height + 12,
      }
    : undefined;
  let bubbleStyle: CSSProperties;
  if (hasRect) {
    const centerX = rect.left + rect.width / 2;
    const left = Math.min(
      Math.max(centerX - BUBBLE_W / 2, EDGE),
      Math.max(vw - BUBBLE_W - EDGE, EDGE),
    );
    const below = rect.top + rect.height + GAP;
    bubbleStyle =
      below + BUBBLE_H <= vh
        ? { top: below, left }
        : { top: Math.max(rect.top - GAP - BUBBLE_H, EDGE), left };
  } else {
    bubbleStyle = {
      top: Math.max(vh / 2 - BUBBLE_H / 2, EDGE),
      left: Math.max(vw / 2 - BUBBLE_W / 2, EDGE),
    };
  }

  return (
    <div
      className="tour-overlay"
      data-testid="guided-tour"
      role="dialog"
      aria-modal="true"
    >
      {hasRect ? (
        <div className="tour-spotlight" style={spotlightStyle} aria-hidden />
      ) : (
        <div className="tour-scrim" aria-hidden />
      )}
      <div className="tour-bubble" style={bubbleStyle}>
        <div className="tour-bubble-body" aria-live="polite">
          <span className="tour-counter" data-testid="tour-counter">
            {t("tour.stepCounter", {
              current: index + 1,
              total: active.length,
            })}
          </span>
          <h3 className="tour-title" data-testid="tour-title">
            {t(step.titleKey)}
          </h3>
          <p className="tour-text" data-testid="tour-body">
            {t(step.bodyKey)}
          </p>
        </div>
        <div className="tour-bubble-actions">
          <button
            type="button"
            className="tour-btn tour-btn-ghost"
            data-testid="tour-skip"
            onClick={onClose}
          >
            {t("tour.skip")}
          </button>
          <div className="tour-bubble-actions-right">
            {index > 0 ? (
              <button
                type="button"
                className="tour-btn tour-btn-ghost"
                data-testid="tour-back"
                onClick={() => setIndex((current) => Math.max(0, current - 1))}
              >
                {t("tour.back")}
              </button>
            ) : null}
            <button
              type="button"
              className="tour-btn tour-btn-primary"
              data-testid="tour-next"
              onClick={goNext}
            >
              {isLast ? t("tour.done") : t("tour.next")}
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
