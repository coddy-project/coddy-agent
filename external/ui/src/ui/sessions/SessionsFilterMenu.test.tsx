import React, { useState } from "react";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, test, vi } from "vitest";
import {
  SessionsFilterMenu,
  placeFilterSubmenu,
} from "./SessionsFilterMenu";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

function rect(left: number, right: number, top = 0, bottom = 0): DOMRect {
  return {
    left,
    right,
    top,
    bottom,
    width: right - left,
    height: bottom - top,
    x: left,
    y: top,
    toJSON: () => ({}),
  } as DOMRect;
}

/**
 * jsdom does no layout, so the window is given a width and the menu the
 * geometry a real engine gave it: `row` is where a section row's box sits
 * (the menu's padding inside it), `submenu` how wide a list of choices draws
 * when nothing constrains it.
 */
function layOut(opts: {
  viewport: number;
  row: [number, number];
  /** One width for every list, or a width per section key. */
  submenu: number | Record<string, number>;
}) {
  vi.stubGlobal("innerWidth", opts.viewport);
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(
    function (this: HTMLElement) {
      if (this.classList.contains("sessions-filter-parent")) {
        return rect(opts.row[0], opts.row[1]);
      }
      if (this.classList.contains("sessions-filter-submenu")) {
        const section = (this.dataset.testid ?? "").replace(
          "sessions-filter-submenu-",
          "",
        );
        const width =
          typeof opts.submenu === "number"
            ? opts.submenu
            : (opts.submenu[section] ?? 160);
        return rect(0, width);
      }
      return rect(0, 0);
    },
  );
}

/**
 * Moves a pointer of `kind` onto `el` from outside the menu. jsdom has no
 * PointerEvent, so a MouseEvent under the pointer event's name carries the
 * type React reads; React makes its enter events out of `pointerover`. A
 * touch is followed by the mouse events browsers emulate for it, as on a
 * phone.
 */
function pointerOnto(el: Element, kind: "mouse" | "touch") {
  const ev = new MouseEvent("pointerover", { bubbles: true, cancelable: true });
  Object.defineProperty(ev, "pointerType", { value: kind });
  fireEvent(el, ev);
  if (kind === "touch") {
    fireEvent.mouseEnter(el);
  }
}

/** Moves a mouse off `el`, to a point outside the menu. */
function pointerOff(el: Element) {
  const ev = new MouseEvent("pointerout", {
    bubbles: true,
    cancelable: true,
    relatedTarget: document.body,
  });
  Object.defineProperty(ev, "pointerType", { value: "mouse" });
  fireEvent(el, ev);
}

/** A click from a mouse or a tap carries its count; a key press carries 0. */
const POINTER_CLICK = { detail: 1 };

function renderMenu(anchor: DOMRect) {
  return render(
    <SessionsFilterMenu
      open
      onClose={() => {}}
      anchor={anchor}
      archiveFilter="exclude"
      onArchiveFilterChange={() => {}}
      groupMode="workspace"
      onGroupModeChange={() => {}}
      sortKey="updated"
      onSortKeyChange={() => {}}
    />,
  );
}

// Issue #460: on a 393px phone the trigger sits at the right edge of the full
// width drawer, so the choices opened on the left - from x = -100 to 60 - and
// only the column of check marks was on the screen.
describe("History filter menu on a phone", () => {
  // The geometry Chromium gives the Russian menu at 393px: the trigger ends at
  // 370, the menu is 313px wide, a row spans its width less the padding.
  const phone = { viewport: 393, row: [63, 364] as [number, number], submenu: 160 };
  const phoneTrigger = rect(334, 370, 150, 186);

  it("folds a section's choices out under its row, inside the screen", () => {
    layOut(phone);
    renderMenu(phoneTrigger);
    fireEvent.click(screen.getByTestId("sessions-filter-section-group"));

    const submenu = screen.getByTestId("sessions-filter-submenu-group");
    expect(submenu).toHaveClass("opens-inline");
    expect(submenu).not.toHaveClass("opens-left");
    // The list opens below its row, and the row's chevron says so.
    expect(
      screen
        .getByTestId("sessions-filter-section-group")
        .querySelector(".coddy-chevron"),
    ).toHaveClass("is-open");
    // Every choice is reachable and still says what it is.
    expect(screen.getByTestId("sessions-filter-group-tag")).toBeInTheDocument();
  });

  it("keeps an inline menu inside the screen's height and lets it scroll", () => {
    layOut(phone);
    vi.stubGlobal("innerHeight", 600);
    renderMenu(phoneTrigger);
    fireEvent.click(screen.getByTestId("sessions-filter-section-group"));

    const menu = screen.getByTestId("sessions-filter-menu");
    expect(menu).toHaveClass("has-inline-submenu");
    // Hung 6px under the trigger, 8px clear of the bottom edge.
    expect(menu.style.maxHeight).toBe(`${600 - (186 + 6) - 8}px`);
  });

  it("is never wider than the room left of its trigger", () => {
    layOut({ viewport: 320, row: [14, 291], submenu: 160 });
    renderMenu(rect(261, 297, 150, 186));

    const menu = screen.getByTestId("sessions-filter-menu");
    expect(menu.style.right).toBe(`${320 - 297}px`);
    expect(menu.style.maxWidth).toBe(`${297 - 8}px`);
  });

  it("switches sections by tap, not by the pointer crossing the rows", () => {
    // Once the choices fold out under a row, the rows below move down; a
    // section opening on hover would fold them back under the pointer.
    layOut(phone);
    renderMenu(phoneTrigger);
    fireEvent.click(screen.getByTestId("sessions-filter-section-group"));

    pointerOnto(screen.getByTestId("sessions-filter-section-sort"), "mouse");
    expect(screen.getByTestId("sessions-filter-group-tag")).toBeInTheDocument();
    expect(screen.queryByTestId("sessions-filter-sort-title")).toBeNull();

    fireEvent.click(screen.getByTestId("sessions-filter-section-sort"));
    expect(screen.queryByTestId("sessions-filter-group-tag")).toBeNull();
    expect(screen.getByTestId("sessions-filter-submenu-sort")).toHaveClass(
      "opens-inline",
    );
  });
});

describe("History filter menu between the two", () => {
  it("keeps every list inline once one had to go inline", () => {
    // 510px: the status list fits on the left of the menu, the wider sort
    // list fits nowhere. Switching kinds of list under the reader's finger
    // would move the rows, so the menu stays inline until it closes.
    layOut({ viewport: 510, row: [180, 481], submenu: { status: 160, sort: 199 } });
    renderMenu(rect(451, 487, 150, 186));

    fireEvent.click(screen.getByTestId("sessions-filter-section-status"), POINTER_CLICK);
    expect(screen.getByTestId("sessions-filter-submenu-status")).toHaveClass(
      "opens-left",
    );

    fireEvent.click(screen.getByTestId("sessions-filter-section-sort"), POINTER_CLICK);
    expect(screen.getByTestId("sessions-filter-submenu-sort")).toHaveClass(
      "opens-inline",
    );

    fireEvent.click(screen.getByTestId("sessions-filter-section-status"), POINTER_CLICK);
    expect(screen.getByTestId("sessions-filter-submenu-status")).toHaveClass(
      "opens-inline",
    );
  });
});

describe("History filter menu beside the drawer", () => {
  it("opens the choices on the right while there is room there", () => {
    // Desktop: the drawer sits at the left of the window.
    layOut({ viewport: 1280, row: [86, 387], submenu: 160 });
    renderMenu(rect(357, 393, 150, 186));
    fireEvent.click(screen.getByTestId("sessions-filter-section-group"));

    const submenu = screen.getByTestId("sessions-filter-submenu-group");
    expect(submenu).not.toHaveClass("opens-left");
    expect(submenu).not.toHaveClass("opens-inline");
    // Beside the menu the chevron keeps pointing at the list.
    expect(
      screen
        .getByTestId("sessions-filter-section-group")
        .querySelector(".coddy-chevron"),
    ).not.toHaveClass("is-open");
    expect(screen.getByTestId("sessions-filter-menu")).not.toHaveClass(
      "has-inline-submenu",
    );
  });

  it("opens them on the left near the right edge of the window", () => {
    // A tablet: the drawer is the width of the window, its trigger at the right.
    layOut({ viewport: 700, row: [370, 671], submenu: 199 });
    renderMenu(rect(641, 677, 150, 186));
    fireEvent.click(screen.getByTestId("sessions-filter-section-sort"));

    const submenu = screen.getByTestId("sessions-filter-submenu-sort");
    expect(submenu).toHaveClass("opens-left");
    expect(submenu).not.toHaveClass("opens-inline");
  });
});

describe("History filter menu with a mouse", () => {
  it("keeps a section the pointer just opened when the same pointer clicks it", () => {
    // Moving onto a row opens it, and the click that follows lands on a row
    // that is already open; toggling there folded it straight back.
    renderMenu(rect(357, 393, 150, 186));
    const row = screen.getByTestId("sessions-filter-section-group");

    pointerOnto(row, "mouse");
    expect(screen.getByTestId("sessions-filter-group-tag")).toBeInTheDocument();
    fireEvent.click(row, POINTER_CLICK);
    expect(screen.getByTestId("sessions-filter-group-tag")).toBeInTheDocument();

    // A second click is a deliberate one and folds the section.
    fireEvent.click(row, POINTER_CLICK);
    expect(screen.queryByTestId("sessions-filter-group-tag")).toBeNull();
  });

  it("folds a section on click once the pointer has been in its list", () => {
    renderMenu(rect(357, 393, 150, 186));
    const row = screen.getByTestId("sessions-filter-section-group");

    pointerOnto(row, "mouse");
    // Over to the choices and back: the open section is the reader's now.
    pointerOff(row);
    fireEvent.click(row, POINTER_CLICK);
    expect(screen.queryByTestId("sessions-filter-group-tag")).toBeNull();
  });

  it("folds a section the pointer opened when a key presses its row", () => {
    // Enter or Space on the focused row is never the pointer's own click.
    renderMenu(rect(357, 393, 150, 186));
    const row = screen.getByTestId("sessions-filter-section-group");

    pointerOnto(row, "mouse");
    fireEvent.click(row, { detail: 0 });
    expect(screen.queryByTestId("sessions-filter-group-tag")).toBeNull();
  });
});

describe("History filter menu with a finger", () => {
  it("folds a row the mouse opened when a finger taps it", () => {
    // A laptop with a touchscreen: the mouse opened the row, and the tap that
    // follows is the reader's own click, not the mouse's.
    renderMenu(rect(357, 393, 150, 186));
    const row = screen.getByTestId("sessions-filter-section-group");

    pointerOnto(row, "mouse");
    expect(screen.getByTestId("sessions-filter-group-tag")).toBeInTheDocument();
    pointerOnto(row, "touch");
    fireEvent.click(row, POINTER_CLICK);
    expect(screen.queryByTestId("sessions-filter-group-tag")).toBeNull();
  });

  it("opens a row by its tap, not by the hover a touch emulates", () => {
    // iOS drops the click of a tap whose hover shows new content: a row that
    // opened on that hover took a second tap to fold.
    renderMenu(rect(357, 393, 150, 186));
    const row = screen.getByTestId("sessions-filter-section-group");

    pointerOnto(row, "touch");
    expect(screen.queryByTestId("sessions-filter-group-tag")).toBeNull();
    fireEvent.click(row, POINTER_CLICK);
    expect(screen.getByTestId("sessions-filter-group-tag")).toBeInTheDocument();
    fireEvent.click(row, POINTER_CLICK);
    expect(screen.queryByTestId("sessions-filter-group-tag")).toBeNull();
  });
});

describe("History filter menu and Escape", () => {
  it("folds the section, then closes the menu, without updating the drawer mid-render", () => {
    // The drawer owns `open`. Closing it from inside a state updater ran the
    // drawer's update while React rendered the menu, which React reports.
    const errors = vi.spyOn(console, "error").mockImplementation(() => {});
    function Drawer() {
      const [open, setOpen] = useState(true);
      return (
        <SessionsFilterMenu
          open={open}
          onClose={() => setOpen(false)}
          archiveFilter="exclude"
          onArchiveFilterChange={() => {}}
          groupMode="workspace"
          onGroupModeChange={() => {}}
          sortKey="updated"
          onSortKeyChange={() => {}}
        />
      );
    }
    render(<Drawer />);
    fireEvent.click(screen.getByTestId("sessions-filter-section-sort"));
    expect(screen.getByTestId("sessions-filter-sort-title")).toBeInTheDocument();

    fireEvent.keyDown(window, { key: "Escape" });
    expect(screen.queryByTestId("sessions-filter-sort-title")).toBeNull();
    expect(screen.getByTestId("sessions-filter-menu")).toBeInTheDocument();

    fireEvent.keyDown(window, { key: "Escape" });
    expect(screen.queryByTestId("sessions-filter-menu")).toBeNull();
    expect(errors).not.toHaveBeenCalled();
  });
});

describe("placeFilterSubmenu", () => {
  test("prefers the right, where the drawer leaves the room", () => {
    expect(placeFilterSubmenu({ left: 86, right: 387 }, 160, 1280)).toBe("right");
  });

  test("goes left when the right is short of room", () => {
    expect(placeFilterSubmenu({ left: 370, right: 671 }, 199, 700)).toBe("left");
  });

  test("folds inline when neither side has the room", () => {
    expect(placeFilterSubmenu({ left: 63, right: 364 }, 160, 393)).toBe("inline");
    // One window, two lists: the status list fits on the left, the wider sort
    // list does not.
    expect(placeFilterSubmenu({ left: 180, right: 481 }, 160, 510)).toBe("left");
    expect(placeFilterSubmenu({ left: 180, right: 481 }, 199, 510)).toBe("inline");
  });

  test("keeps the list 8px clear of the window edge", () => {
    // Right: 4px gap from the row, then the list, then 8px to the edge.
    expect(placeFilterSubmenu({ left: 0, right: 100 }, 188, 300)).toBe("right");
    expect(placeFilterSubmenu({ left: 0, right: 100 }, 189, 300)).toBe("inline");
    // Left: 8px from the edge, the list, then the 4px gap to the row.
    expect(placeFilterSubmenu({ left: 212, right: 500 }, 200, 500)).toBe("left");
    expect(placeFilterSubmenu({ left: 211, right: 500 }, 200, 500)).toBe("inline");
  });
});
