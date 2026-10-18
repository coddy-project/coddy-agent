/**
 * Going to a setting the search found: find its box (an element with
 * `data-settings-anchor`; lookups compare `dataset` values and never build a
 * selector from a name), unfold what hides it, scroll to it, ring it, focus it.
 */
import type { SettingsSearchEntry } from "./settingsSearch";

/**
 * Dispatched (bubbling) from a setting's box before it is scrolled to: a folded
 * block above it, or the box itself when it is a fold, opens on it.
 */
export const SETTINGS_REVEAL_EVENT = "coddy:settings-reveal";

export const SEARCH_TARGET_MS = 1600;
export const REVEAL_WAIT_MS = 1000;
const TARGET_CLASS = "is-search-target";

function squash(text: string | null | undefined): string {
  return (text ?? "").replace(/\s+/g, " ").trim();
}

function boxesIn(scope: ParentNode): HTMLElement[] {
  return [...scope.querySelectorAll<HTMLElement>("[data-settings-anchor]")];
}

/**
 * The box of an entry, or null while it is not drawn. A repeated box (a field
 * of a list of objects) resolves to the first in document order. A row's box is
 * looked up only inside that row's form; the list's own box answers only when
 * the list draws no entry of the field.
 */
export function findSettingsBox(
  root: HTMLElement,
  entry: SettingsSearchEntry,
): HTMLElement | null {
  if (entry.anchor === null) {
    return null;
  }
  let scope: ParentNode = root;
  if (entry.row !== null) {
    const form = boxesIn(root).find(
      (box) =>
        box.dataset["settingsRow"] === entry.row &&
        (box.dataset["settingsAnchor"] ?? "").startsWith("row:"),
    );
    if (form === undefined) {
      return null;
    }
    if (entry.kind === "row") {
      return form;
    }
    scope = form;
  }
  const boxes = boxesIn(scope);
  return (
    boxes.find((box) => box.dataset["settingsAnchor"] === entry.anchor) ??
    (entry.fallbackAnchor === null
      ? null
      : (boxes.find(
          (box) => box.dataset["settingsAnchor"] === entry.fallbackAnchor,
        ) ?? null))
  );
}

/** What a tab entry is taken to: the scroll area of the tab. */
function findPane(
  root: HTMLElement,
  entry: SettingsSearchEntry,
): HTMLElement | null {
  return (
    [...root.querySelectorAll<HTMLElement>("[data-settings-tab]")].find(
      (pane) => pane.dataset["settingsTab"] === entry.section,
    ) ?? null
  );
}

const SHUT_FOLD = ".settings-fieldset--collapsible:not(.is-open)";

/** Whether the box can be seen: no hidden ancestor, and it is not itself a shut fold. */
function isShown(box: HTMLElement): boolean {
  return box.closest("[hidden]") === null && !box.matches(SHUT_FOLD);
}

const CONTROLS =
  "input, select, textarea, button, [role='combobox'], [role='switch'], [role='textbox'], [contenteditable='true']";

/** The accessible name of a control from its aria-labelledby, aria-label or label. */
function controlName(control: HTMLElement): string {
  const by = control.getAttribute("aria-labelledby");
  if (by) {
    const text = squash(
      by
        .split(/\s+/)
        .map((id) => control.ownerDocument.getElementById(id)?.textContent)
        .join(" "),
    );
    if (text !== "") {
      return text;
    }
  }
  const aria = squash(control.getAttribute("aria-label"));
  if (aria !== "") {
    return aria;
  }
  const labels = (control as HTMLInputElement).labels;
  return labels && labels.length > 0
    ? squash([...labels].map((l) => l.textContent).join(" "))
    : "";
}

/**
 * The control of a box whose accessible name is `name`, skipping disabled
 * ones, the (i) and those of a nested box: how a primary name picks the input
 * out of a box that also draws a switch.
 */
export function controlNamed(
  box: HTMLElement,
  name: string,
): HTMLElement | null {
  const wanted = squash(name);
  if (wanted === "") {
    return null;
  }
  for (const control of box.querySelectorAll<HTMLElement>(CONTROLS)) {
    if (
      control.classList.contains("field-hint") ||
      control.closest(".field-hint") !== null ||
      control.closest("[data-settings-anchor]") !== box ||
      (control as HTMLInputElement).disabled ||
      control.getAttribute("aria-disabled") === "true" ||
      control.getAttribute("type") === "hidden"
    ) {
      continue;
    }
    if (controlName(control) === wanted) {
      return control;
    }
  }
  return null;
}

/**
 * Makes a box focusable and named while it holds the focus (a fieldset by its
 * legend, any other unnamed box as a group). Returns what puts it back.
 */
function nameForFocus(box: HTMLElement, name: string): () => void {
  const undo: (() => void)[] = [];
  if (!box.hasAttribute("tabindex")) {
    box.setAttribute("tabindex", "-1");
    undo.push(() => box.removeAttribute("tabindex"));
  }
  const named =
    box.tagName === "FIELDSET"
      ? [...box.children].some((child) => child.tagName === "LEGEND")
      : box.hasAttribute("aria-label") || box.hasAttribute("aria-labelledby");
  if (!named) {
    const role = box.getAttribute("role");
    box.setAttribute("role", "group");
    box.setAttribute("aria-label", name);
    undo.push(() => {
      box.removeAttribute("aria-label");
      if (role === null) {
        box.removeAttribute("role");
      } else {
        box.setAttribute("role", role);
      }
    });
  }
  return () => undo.forEach((step) => step());
}

/**
 * Takes the operator to the setting of an entry once the page can show it:
 * waits (within REVEAL_WAIT_MS) until the box is drawn and nothing above it is
 * hidden, then scrolls it into view, rings it for SEARCH_TARGET_MS and focuses
 * it. A field takes the control named by its primary name, except on a
 * touch-only device (no keyboard rises), where, like every tab, row, block and
 * landmark, the box itself is focused. A tab is scrolled to its top, with no
 * ring. It gives up quietly when the budget runs out; returns what cancels it.
 */
export function revealWhenReady(
  root: HTMLElement,
  entry: SettingsSearchEntry,
  opts: { touchOnly: boolean; reducedMotion: boolean },
): () => void {
  let cancelled = false;
  let frame: number | null = null;
  let ringTimer: number | null = null;
  let ringed: HTMLElement | null = null;
  const observer = new MutationObserver(() => attempt());
  const budget = window.setTimeout(stopWaiting, REVEAL_WAIT_MS);

  const isTab = entry.anchor === null;
  const locate = (): HTMLElement | null =>
    isTab ? findPane(root, entry) : findSettingsBox(root, entry);

  function stopWaiting() {
    observer.disconnect();
    window.clearTimeout(budget);
    if (frame !== null) {
      window.cancelAnimationFrame(frame);
      frame = null;
    }
  }

  function attempt() {
    if (cancelled || frame !== null) {
      return;
    }
    const target = locate();
    if (target === null) {
      return;
    }
    if (!isShown(target)) {
      // A fold opens on the event and the hidden attribute going away brings
      // the next attempt. Asking at every mutation is cheap, and reaches a
      // fold that was not listening yet.
      target.dispatchEvent(new Event(SETTINGS_REVEAL_EVENT, { bubbles: true }));
      return;
    }
    // A frame later, so the page has laid out what just opened.
    frame = window.requestAnimationFrame(() => {
      frame = null;
      if (cancelled) {
        return;
      }
      const again = locate();
      if (again === null || !isShown(again)) {
        attempt();
        return;
      }
      reveal(again);
    });
  }

  function reveal(target: HTMLElement) {
    stopWaiting();
    const behavior = opts.reducedMotion ? "auto" : "smooth";
    if (isTab) {
      if (typeof target.scrollTo === "function") {
        target.scrollTo({ top: 0, behavior });
      } else {
        target.scrollTop = 0;
      }
    } else {
      // The middle of the area, or its top when the box is taller than it.
      const area = target.closest<HTMLElement>(".settings-scroll");
      const tall =
        area !== null &&
        target.getBoundingClientRect().height > area.clientHeight;
      target.scrollIntoView?.({ block: tall ? "start" : "center", behavior });
      ring(target);
    }
    const name = entry.names[0] ?? "";
    const control =
      entry.kind === "field" && !opts.touchOnly
        ? controlNamed(target, name)
        : null;
    const focused = control ?? target;
    if (control === null) {
      const undo = nameForFocus(target, name);
      target.addEventListener("blur", undo, { once: true });
    }
    focused.focus({ preventScroll: true });
  }

  function ring(target: HTMLElement) {
    // Taking the class off and putting it on again restarts the fade.
    target.classList.remove(TARGET_CLASS);
    void target.offsetWidth;
    target.classList.add(TARGET_CLASS);
    ringed = target;
    ringTimer = window.setTimeout(() => {
      ringTimer = null;
      target.classList.remove(TARGET_CLASS);
    }, SEARCH_TARGET_MS);
  }

  observer.observe(root, {
    childList: true,
    subtree: true,
    attributes: true,
    attributeFilter: ["hidden"],
  });
  attempt();

  return () => {
    cancelled = true;
    stopWaiting();
    if (ringTimer !== null) {
      window.clearTimeout(ringTimer);
    }
    ringed?.classList.remove(TARGET_CLASS);
  };
}
