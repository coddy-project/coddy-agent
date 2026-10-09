import { useEffect, useLayoutEffect, useRef } from "react";
import type { NavItemId } from "./navOverflow";

/**
 * Escape and the screens of the rail.
 *
 * Every item of the rail but Sign out opens a screen over the chat - History,
 * Scheduler, Swarm, Docs, Settings - and Escape closes the one on screen, the
 * way its close control does (the swarm screen, which has none, the way its
 * backdrop does). The rule is written here once, over a table App fills in
 * for every screen (`RailScreens`), rather than as a handler in each screen:
 * while every screen had to remember a handler of its own, Docs and Settings
 * did not. The table is a Record over `RailScreenId`, so a screen added to the
 * rail does not pass the type check (tsc in make lint, the pre-commit hook and
 * CI) until it says whether it is open and what closes it.
 *
 * Escape undoes one step. What is nearer the key answers first - a menu, a
 * picker, a tip, a search box with text in it, a dialog, the image viewer -
 * and claims the key with `preventDefault` (or stops it); a screen takes only
 * an Escape nothing claimed. A screen with steps of its own registers the step
 * back with `useRailEscapeStep`, and Escape takes that step before it closes
 * the screen.
 */

/**
 * A screen the rail opens: every rail item but sign-out and the environment,
 * which opens a menu (it answers Escape through useEscapeCloses) rather than
 * a screen.
 */
export type RailScreenId = Exclude<NavItemId, "signOut" | "environment">;

/** Whether a screen is on screen, and what Escape does to it. */
export type RailScreen = { open: boolean; close: () => void };

export type RailScreens = Record<RailScreenId, RailScreen>;

/**
 * How high each screen is drawn, the topmost at 0. The rail keeps one screen
 * open at a time, so this only settles a tie; it is a Record so that a screen
 * added to the rail has to take a place in it.
 */
const RAIL_SCREEN_DEPTH: Record<RailScreenId, number> = {
  docs: 0,
  settings: 1,
  swarm: 2,
  scheduler: 3,
  history: 4,
};

/** Every screen of the rail, the topmost first. */
export const RAIL_SCREENS: readonly RailScreenId[] = (
  Object.keys(RAIL_SCREEN_DEPTH) as RailScreenId[]
).sort((a, b) => RAIL_SCREEN_DEPTH[a] - RAIL_SCREEN_DEPTH[b]);

/** The open screen Escape closes: the one drawn over the others; null when none is open. */
export function railScreenOnTop(screens: RailScreens): RailScreenId | null {
  return RAIL_SCREENS.find((id) => screens[id].open) ?? null;
}

/**
 * An Escape a screen may take: the key alone, pressed rather than held (one
 * press, one step), not the end of an input method's composition, and not
 * claimed by anything nearer the key.
 */
export function isUnclaimedEscape(ev: KeyboardEvent): boolean {
  return (
    ev.key === "Escape" &&
    !ev.defaultPrevented &&
    !ev.repeat &&
    !ev.isComposing &&
    ev.keyCode !== 229 &&
    !ev.altKey &&
    !ev.ctrlKey &&
    !ev.metaKey &&
    !ev.shiftKey
  );
}

type Step = { screen: RailScreenId; run: () => void };

// The steps back the mounted screens registered, oldest first. There is one
// rail on a page, so the registry is the module's rather than a context's: a
// screen registers a step without being handed anything.
let steps: Step[] = [];

function lastStep(screen: RailScreenId): Step | undefined {
  for (let i = steps.length - 1; i >= 0; i--) {
    const step = steps[i];
    if (step && step.screen === screen) {
      return step;
    }
  }
  return undefined;
}

/**
 * The step back a screen takes on Escape before it closes: what the arrow of
 * its head does while the head shows one (Settings: an open row back to its
 * list, a section of the stacked shell back to the tiles). `null` while there
 * is no step to take, and Escape closes the screen.
 */
export function useRailEscapeStep(
  screen: RailScreenId,
  step: (() => void) | null,
): void {
  const stepRef = useRef(step);
  useLayoutEffect(() => {
    stepRef.current = step;
  });
  const active = step !== null;
  useLayoutEffect(() => {
    if (!active) {
      return undefined;
    }
    const entry: Step = { screen, run: () => stepRef.current?.() };
    steps = [...steps, entry];
    return () => {
      steps = steps.filter((s) => s !== entry);
    };
  }, [screen, active]);
}

type Guard = { screen: RailScreenId; run: () => void };

// What closing a screen by a gesture of the shell - a click on the backdrop
// beside it - runs instead, while the screen asks to be asked first (Settings
// over unsaved edits). Registered by the screen like its Escape step.
let guards: Guard[] = [];

/**
 * useRailCloseGuard registers what a click on the backdrop beside the screen
 * runs instead of closing it: the screen's own close, which may ask first.
 * `null` while the screen may close without asking.
 */
export function useRailCloseGuard(
  screen: RailScreenId,
  guard: (() => void) | null,
): void {
  const guardRef = useRef(guard);
  useLayoutEffect(() => {
    guardRef.current = guard;
  });
  const active = guard !== null;
  useLayoutEffect(() => {
    if (!active) {
      return undefined;
    }
    const entry: Guard = { screen, run: () => guardRef.current?.() };
    guards = [...guards, entry];
    return () => {
      guards = guards.filter((g) => g !== entry);
    };
  }, [screen, active]);
}

/** railCloseGuard is the guard the screen registered, if it asks first now. */
export function railCloseGuard(screen: RailScreenId): (() => void) | null {
  for (let i = guards.length - 1; i >= 0; i--) {
    const guard = guards[i];
    if (guard && guard.screen === screen) {
      return guard.run;
    }
  }
  return null;
}

/**
 * Closes the screen on top, one step at a time, on an Escape nothing nearer
 * took. App calls it once, with every screen of the rail.
 *
 * It is one listener on document, in the bubble phase. That puts it after the
 * controls of the page (React answers at its root, below document) and after
 * the tips and menus that listen on document or window in the capture phase,
 * and before the listeners on window, which belong to the chat under the
 * screen: the question card's Escape skips its questions. An Escape that took a
 * step goes no further.
 */
export function useRailScreenEscape(screens: RailScreens): void {
  const screensRef = useRef(screens);
  useLayoutEffect(() => {
    screensRef.current = screens;
  });
  useEffect(() => {
    const onKey = (ev: KeyboardEvent) => {
      if (!isUnclaimedEscape(ev)) {
        return;
      }
      const top = railScreenOnTop(screensRef.current);
      if (top === null) {
        return;
      }
      ev.preventDefault();
      ev.stopPropagation();
      const step = lastStep(top);
      if (step) {
        step.run();
      } else {
        screensRef.current[top].close();
      }
    };
    document.addEventListener("keydown", onKey);
    return () => document.removeEventListener("keydown", onKey);
  }, []);
}
