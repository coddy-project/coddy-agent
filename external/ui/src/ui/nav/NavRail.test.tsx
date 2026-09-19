import React from "react";
import { afterEach, describe, expect, it, test, vi } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { NavRail } from "./NavRail";
import { resetAuthStateForTests, setAuthState } from "../auth/authState";

afterEach(() => cleanup());

test("nav brand uses Coddy agent label (compact rail)", () => {
  render(
    <NavRail
      onNewChat={() => {}}
      onOpenHistory={() => {}}
      historyOpen={false}
      onOpenScheduler={() => {}}
      schedulerOpen={false}
      onOpenSettings={() => {}}
      settingsOpen={false}
      canWidenRail={false}
      railLabelsWide={false}
      onToggleRailLabels={() => {}}
    />,
  );

  expect(
    screen.getByRole("link", { name: "Coddy agent home" }),
  ).toBeInTheDocument();
  expect(screen.getByText("agent")).toBeInTheDocument();
});

test("nav brand uses Coddy agent label (wide header row)", () => {
  render(
    <NavRail
      onNewChat={() => {}}
      onOpenHistory={() => {}}
      historyOpen={false}
      onOpenScheduler={() => {}}
      schedulerOpen={false}
      onOpenSettings={() => {}}
      settingsOpen={false}
      canWidenRail
      railLabelsWide
      onToggleRailLabels={() => {}}
    />,
  );

  expect(
    screen.getByRole("link", { name: "Coddy agent home" }),
  ).toBeInTheDocument();
  expect(screen.getByTestId("nav-home")).toHaveTextContent("Coddy agent");
});

test("nav hides Scheduler when showScheduler is false", () => {
  render(
    <NavRail
      onNewChat={() => {}}
      onOpenHistory={() => {}}
      historyOpen={false}
      showScheduler={false}
      onOpenScheduler={() => {}}
      schedulerOpen={false}
      onOpenSettings={() => {}}
      settingsOpen={false}
      canWidenRail={false}
      railLabelsWide={false}
      onToggleRailLabels={() => {}}
    />,
  );

  expect(screen.queryByTestId("nav-scheduler")).toBeNull();
});

test("in-app nav links expose hash hrefs for new-tab open", () => {
  render(
    <NavRail
      onNewChat={() => {}}
      onOpenHistory={() => {}}
      historyOpen={false}
      onOpenScheduler={() => {}}
      schedulerOpen={false}
      onOpenSettings={() => {}}
      settingsOpen={false}
      canWidenRail={false}
      railLabelsWide={false}
      onToggleRailLabels={() => {}}
    />,
  );
  expect(screen.getByTestId("nav-home")).toHaveAttribute("href", "#/");
  expect(screen.getByTestId("nav-history")).toHaveAttribute(
    "href",
    "#/history",
  );
  expect(screen.getByTestId("nav-scheduler")).toHaveAttribute(
    "href",
    "#/scheduler",
  );
  expect(screen.getByTestId("nav-settings")).toHaveAttribute(
    "href",
    "#/settings",
  );
});

test("the rail no longer carries a Tasks entry", () => {
  render(
    <NavRail
      onNewChat={() => {}}
      onOpenHistory={() => {}}
      historyOpen={false}
      onOpenScheduler={() => {}}
      schedulerOpen={false}
      onOpenSettings={() => {}}
      settingsOpen={false}
      canWidenRail={false}
      railLabelsWide={false}
      onToggleRailLabels={() => {}}
    />,
  );

  // Background tasks belong to a chat, so the opener sits under the transcript
  // rather than in the global rail.
  expect(screen.queryByTestId("nav-tasks")).toBeNull();
  expect(screen.queryByTestId("nav-tasks-badge")).toBeNull();
});

// A relay holds no sessions of its own, so a history drawer there is furniture
// for a room nobody can enter.
describe("NavRail on a relay", () => {
  const base = {
    onNewChat: () => {},
    onOpenHistory: () => {},
    historyOpen: false,
    onOpenScheduler: () => {},
    schedulerOpen: false,
    onOpenSettings: () => {},
    settingsOpen: false,
    canWidenRail: false,
    railLabelsWide: false,
    onToggleRailLabels: () => {},
  };

  afterEach(cleanup);

  it("hides history when there is none to show", () => {
    render(<NavRail {...base} showHistory={false} showSwarm />);
    expect(screen.queryByTestId("nav-history")).not.toBeInTheDocument();
    expect(screen.getByTestId("nav-swarm")).toBeInTheDocument();
  });

  it("keeps history everywhere else", () => {
    render(<NavRail {...base} />);
    expect(screen.getByTestId("nav-history")).toBeInTheDocument();
  });

  // The rail splits at the spacer: what belongs to this session above it,
  // what belongs to the installation below. Swarm is the fleet, so it sits
  // with Settings.
  it("puts swarm at the foot of the rail, above settings", () => {
    render(<NavRail {...base} showSwarm showScheduler />);
    const order = Array.from(
      document.querySelectorAll("[data-testid^='nav-']"),
    ).map((e) => e.getAttribute("data-testid"));
    expect(order).toEqual([
      "nav-home",
      "nav-history",
      "nav-scheduler",
      "nav-swarm",
      "nav-settings",
    ]);
    const spacer = document.querySelector(".rail-spacer-between");
    const swarm = screen.getByTestId("nav-swarm");
    expect(spacer).not.toBeNull();
    expect(
      spacer?.compareDocumentPosition(swarm) &&
        spacer.compareDocumentPosition(swarm) &
          Node.DOCUMENT_POSITION_FOLLOWING,
        ).toBeTruthy();
  });

  it("offers the documentation between the swarm and settings", () => {
    const onOpenDocs = vi.fn();
    render(<NavRail {...base} showSwarm onOpenDocs={onOpenDocs} docsOpen={false} />);
    const order = Array.from(
      document.querySelectorAll("[data-testid^='nav-']"),
    ).map((e) => e.getAttribute("data-testid"));
    expect(order.slice(-3)).toEqual(["nav-swarm", "nav-docs", "nav-settings"]);
    const docs = screen.getByTestId("nav-docs");
    expect(docs.getAttribute("href")).toBe("#/docs");
    fireEvent.click(docs);
    expect(onOpenDocs).toHaveBeenCalledTimes(1);
  });

  it("hides the documentation entry when nothing opens it", () => {
    render(<NavRail {...base} />);
    expect(screen.queryByTestId("nav-docs")).toBeNull();
  });
});

// On a phone the top bar shows what fits and folds the rest behind a More
// button. jsdom does no layout, so the widths the bar measures are stubbed:
// the pill's clientWidth, the brand's natural scrollWidth and an icon's rect.
describe("NavRail on a phone: the More menu", () => {
  const base = {
    onNewChat: () => {},
    onOpenHistory: () => {},
    historyOpen: false,
    onOpenScheduler: () => {},
    schedulerOpen: false,
    onOpenSettings: () => {},
    settingsOpen: false,
    canWidenRail: false,
    railLabelsWide: false,
    onToggleRailLabels: () => {},
  };
  const restore: Array<() => void> = [];

  function stubLayout(opts: { stacked: boolean; pill: number }) {
    vi.stubGlobal("matchMedia", (query: string) => ({
      matches: query.includes("max-width") ? opts.stacked : false,
      media: query,
      addEventListener: () => {},
      removeEventListener: () => {},
      addListener: () => {},
      removeListener: () => {},
      dispatchEvent: () => false,
    }));
    const clientWidth = Object.getOwnPropertyDescriptor(HTMLElement.prototype, "clientWidth");
    const scrollWidth = Object.getOwnPropertyDescriptor(Element.prototype, "scrollWidth");
    const rect = HTMLElement.prototype.getBoundingClientRect;
    Object.defineProperty(HTMLElement.prototype, "clientWidth", {
      configurable: true,
      get() {
        return (this as HTMLElement).classList.contains("rail-pill") ? opts.pill : 0;
      },
    });
    Object.defineProperty(Element.prototype, "scrollWidth", {
      configurable: true,
      get() {
        return (this as Element).classList.contains("rail-brand") ? 60 : 0;
      },
    });
    HTMLElement.prototype.getBoundingClientRect = function (this: HTMLElement) {
      const w = this.classList.contains("rail-hit") ? 40 : 0;
      return { width: w, height: w, x: 0, y: 0, top: 0, left: 0, right: w, bottom: w, toJSON: () => ({}) } as DOMRect;
    };
    restore.push(() => {
      if (clientWidth) Object.defineProperty(HTMLElement.prototype, "clientWidth", clientWidth);
      if (scrollWidth) Object.defineProperty(Element.prototype, "scrollWidth", scrollWidth);
      HTMLElement.prototype.getBoundingClientRect = rect;
      vi.unstubAllGlobals();
    });
  }

  function signIn() {
    setAuthState({ loginRequired: true, authRequired: true, authenticated: true, user: "demo", loaded: true });
    restore.push(() => resetAuthStateForTests());
  }

  function barIds(): string[] {
    const middle = document.querySelector(".rail-middle")!;
    return Array.from(middle.querySelectorAll(":scope > .rail-tip-host > [data-testid^='nav-']")).map(
      (e) => e.getAttribute("data-testid")!,
    );
  }

  afterEach(() => {
    cleanup();
    while (restore.length) restore.pop()!();
  });

  it("shows every item and no More button when they all fit", () => {
    signIn();
    stubLayout({ stacked: true, pill: 400 });
    render(<NavRail {...base} onOpenDocs={() => {}} />);
    expect(barIds()).toEqual(["nav-history", "nav-scheduler", "nav-docs", "nav-settings", "nav-sign-out"]);
    expect(screen.queryByTestId("nav-more")).toBeNull();
  });

  it("folds what does not fit behind More, sign-out last under a separator", () => {
    signIn();
    // 220 - 60 for the brand = 160: four 40px slots, one of them the More button.
    stubLayout({ stacked: true, pill: 220 });
    render(<NavRail {...base} onOpenDocs={() => {}} />);
    expect(barIds()).toEqual(["nav-history", "nav-scheduler", "nav-settings", "nav-more"]);
    const more = screen.getByTestId("nav-more");
    expect(more).toHaveAttribute("aria-label", "More");
    expect(more).toHaveAttribute("aria-haspopup", "menu");
    expect(more).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByRole("menu")).toBeNull();

    fireEvent.click(more);
    expect(more).toHaveAttribute("aria-expanded", "true");
    const menu = screen.getByRole("menu");
    const rows = Array.from(menu.children).map(
      (e) => e.getAttribute("data-testid") ?? e.getAttribute("role"),
    );
    expect(rows).toEqual(["nav-more-docs", "separator", "nav-more-sign-out"]);
    expect(screen.getByTestId("nav-more-docs")).toHaveAttribute("href", "#/docs");
    expect(screen.getByTestId("nav-more-docs")).toHaveAttribute("role", "menuitem");
    expect(screen.getByTestId("nav-more-docs")).toHaveTextContent("Docs");
    expect(screen.getByTestId("nav-more-sign-out")).toHaveAttribute("role", "menuitem");
  });

  it("with less room settings stays and the rest folds, history never does", () => {
    stubLayout({ stacked: true, pill: 140 });
    render(<NavRail {...base} onOpenDocs={() => {}} />);
    // 80px: two slots, one for More.
    expect(barIds()).toEqual(["nav-history", "nav-more"]);
    fireEvent.click(screen.getByTestId("nav-more"));
    expect(
      Array.from(screen.getByRole("menu").querySelectorAll("[role='menuitem']")).map((e) =>
        e.getAttribute("data-testid"),
      ),
    ).toEqual(["nav-more-docs", "nav-more-scheduler", "nav-more-settings"]);
  });

  it("picking a folded item opens it and closes the menu", () => {
    const onOpenDocs = vi.fn();
    stubLayout({ stacked: true, pill: 180 });
    render(<NavRail {...base} onOpenDocs={onOpenDocs} />);
    fireEvent.click(screen.getByTestId("nav-more"));
    fireEvent.click(screen.getByTestId("nav-more-docs"));
    expect(onOpenDocs).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("menu")).toBeNull();
    expect(screen.getByTestId("nav-more")).toHaveAttribute("aria-expanded", "false");
  });

  it("Escape and a press outside close the menu", () => {
    stubLayout({ stacked: true, pill: 180 });
    render(<NavRail {...base} onOpenDocs={() => {}} />);
    const more = screen.getByTestId("nav-more");
    fireEvent.click(more);
    fireEvent.keyDown(document, { key: "Escape" });
    expect(screen.queryByRole("menu")).toBeNull();
    expect(document.activeElement).toBe(more);
    fireEvent.click(more);
    fireEvent.pointerDown(document.body);
    expect(screen.queryByRole("menu")).toBeNull();
  });

  it("More lights up while a folded panel is open", () => {
    stubLayout({ stacked: true, pill: 180 });
    const { rerender } = render(<NavRail {...base} onOpenDocs={() => {}} docsOpen={false} />);
    expect(screen.getByTestId("nav-more").className).not.toContain("is-active");
    rerender(<NavRail {...base} onOpenDocs={() => {}} docsOpen />);
    expect(screen.getByTestId("nav-more").className).toContain("is-active");
  });

  it("a desktop rail never folds, whatever it measures", () => {
    signIn();
    stubLayout({ stacked: false, pill: 100 });
    render(<NavRail {...base} onOpenDocs={() => {}} />);
    expect(barIds()).toEqual(["nav-history", "nav-scheduler", "nav-docs", "nav-settings", "nav-sign-out"]);
    expect(screen.queryByTestId("nav-more")).toBeNull();
  });
});
