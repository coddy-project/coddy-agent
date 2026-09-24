import {
  useEffect,
  useId,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { createPortal } from "react-dom";

import { useT } from "../i18n/I18nProvider";

/** Space between the (i) and its tip, and the least the tip keeps from the
 * viewport edges. */
const TIP_GAP = 8;
const VIEWPORT_MARGIN = 12;

type TipPosition = { left: number; top: number; placement: "above" | "below" };

/**
 * FieldHint is the (i) beside a settings field's name that carries the
 * field's description, so the form reads as a list of names and controls
 * instead of a wall of text. Hover or keyboard focus opens the tip, a tap
 * opens it on a touch screen; leaving, blurring, Escape, a tap elsewhere or
 * any scroll closes it.
 *
 * The tip is portalled to <body> and positioned with `position: fixed` above
 * the (i) (below when there is no room), clamped to the viewport. Rendered
 * inside the field it would live in the settings scroll container: it would
 * be clipped by it, or widen it and bring out a stray scrollbar. It exists
 * only while open, and the (i) names it through aria-describedby then, so a
 * screen reader hears the description when the control takes focus.
 */
export function FieldHint(props: {
  text: ReactNode;
  label?: string | undefined;
}) {
  const { t } = useT();
  const [open, setOpen] = useState(false);
  const [position, setPosition] = useState<TipPosition | null>(null);
  const buttonRef = useRef<HTMLButtonElement>(null);
  const tipRef = useRef<HTMLDivElement>(null);
  const tipId = useId();
  // A tap opens the tip for good (until a tap elsewhere): the mouse events a
  // touch screen synthesizes around it must not close it again.
  const pinned = useRef(false);

  // Measure the tip once it is in the document, then place it: above the
  // (i) when it fits, below otherwise, never past the viewport edges.
  useLayoutEffect(() => {
    if (!open) {
      pinned.current = false;
      setPosition(null);
      return;
    }
    const button = buttonRef.current;
    const tip = tipRef.current;
    if (!button || !tip) {
      return;
    }
    const anchor = button.getBoundingClientRect();
    const width = tip.offsetWidth;
    const height = tip.offsetHeight;
    const maxLeft = window.innerWidth - VIEWPORT_MARGIN - width;
    const left = Math.max(
      VIEWPORT_MARGIN,
      Math.min(anchor.left + anchor.width / 2 - width / 2, maxLeft),
    );
    const above = anchor.top - TIP_GAP - height >= VIEWPORT_MARGIN;
    // Below, it never runs past the bottom edge either.
    const below = Math.min(
      anchor.bottom + TIP_GAP,
      window.innerHeight - VIEWPORT_MARGIN - height,
    );
    setPosition({
      left,
      top: above
        ? anchor.top - TIP_GAP - height
        : Math.max(VIEWPORT_MARGIN, below),
      placement: above ? "above" : "below",
    });
  }, [open]);

  useEffect(() => {
    if (!open) {
      return;
    }
    const close = () => setOpen(false);
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") {
        close();
      }
    };
    const onPointerDown = (e: Event) => {
      if (!buttonRef.current?.contains(e.target as Node)) {
        close();
      }
    };
    // A fixed tip would stay behind while the field scrolls away under it.
    document.addEventListener("scroll", close, true);
    window.addEventListener("resize", close);
    document.addEventListener("keydown", onKey);
    document.addEventListener("mousedown", onPointerDown);
    document.addEventListener("touchstart", onPointerDown);
    return () => {
      document.removeEventListener("scroll", close, true);
      window.removeEventListener("resize", close);
      document.removeEventListener("keydown", onKey);
      document.removeEventListener("mousedown", onPointerDown);
      document.removeEventListener("touchstart", onPointerDown);
    };
  }, [open]);

  const aria = props.label
    ? t("settings.fieldHint.aria", { label: props.label })
    : t("settings.fieldHint.ariaGeneric");

  return (
    <>
      <button
        ref={buttonRef}
        type="button"
        className={`field-hint${open ? " is-open" : ""}`}
        data-testid="field-hint"
        aria-label={aria}
        aria-describedby={open ? tipId : undefined}
        onPointerDown={(e) => {
          pinned.current = e.pointerType === "touch" || e.pointerType === "pen";
        }}
        onMouseEnter={() => setOpen(true)}
        onMouseLeave={() => {
          if (!pinned.current) {
            setOpen(false);
          }
        }}
        onFocus={() => setOpen(true)}
        onBlur={() => {
          if (!pinned.current) {
            setOpen(false);
          }
        }}
        onClick={(e) => {
          // Inside a <label> or a <legend> the click must not reach the
          // control; on a touch screen the tap is what opens the tip.
          e.preventDefault();
          e.stopPropagation();
          setOpen(true);
        }}
      >
        <svg
          width="14"
          height="14"
          viewBox="0 0 16 16"
          fill="none"
          aria-hidden="true"
          focusable="false"
        >
          <circle
            cx="8"
            cy="8"
            r="6.5"
            stroke="currentColor"
            strokeWidth="1.3"
          />
          <path
            d="M8 7.2v4"
            stroke="currentColor"
            strokeWidth="1.4"
            strokeLinecap="round"
          />
          <circle cx="8" cy="4.9" r="0.85" fill="currentColor" />
        </svg>
      </button>
      {open
        ? createPortal(
            <div
              ref={tipRef}
              id={tipId}
              role="tooltip"
              className={`field-hint-tip${position ? ` is-${position.placement}` : ""}`}
              data-testid="field-hint-tip"
              style={
                position
                  ? { left: position.left, top: position.top }
                  : { left: 0, top: 0, visibility: "hidden" }
              }
            >
              {props.text}
            </div>,
            document.body,
          )
        : null}
    </>
  );
}

/**
 * FieldLabel is a settings field's name with its description in a FieldHint
 * beside it, or the bare name when there is no description. Every settings
 * control that has a name and a description goes through it (or through
 * SwitchField, which lays out the same pair next to its switch).
 */
export function FieldLabel(props: {
  label: ReactNode;
  description?: ReactNode | undefined;
  /** Accessible name of the (i), when the label is not plain text. */
  hintLabel?: string | undefined;
}) {
  const { label, description } = props;
  if (!description) {
    return <span className="settings-label">{label}</span>;
  }
  return (
    <span className="settings-label settings-label-with-hint">
      <span className="settings-label-text">{label}</span>
      <FieldHint
        text={description}
        label={
          props.hintLabel ?? (typeof label === "string" ? label : undefined)
        }
      />
    </span>
  );
}

/** A fieldset legend with the fieldset's description in a FieldHint. */
export function LegendWithHint(props: {
  label: string;
  description?: ReactNode | undefined;
}) {
  return (
    <legend>
      <span className="settings-legend-line">
        {props.label}
        {props.description ? (
          <FieldHint text={props.description} label={props.label} />
        ) : null}
      </span>
    </legend>
  );
}
