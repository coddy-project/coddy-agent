import React from "react";
import { afterEach, describe, expect, it, test, vi } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import { NavRail } from "./NavRail";
import { OpenRailScreen } from "./railEscape.fakes";
import { resetAuthStateForTests, setAuthState } from "../auth/authState";
import { ExtensionsProvider } from "../extensions/ExtensionsProvider";

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

test("the name opens the start screen and has no tooltip, the plus has one", () => {
  const onNewChat = vi.fn();
  render(
    <NavRail
      onNewChat={onNewChat}
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
  const home = screen.getByTestId("nav-home");
  expect(home.parentElement?.querySelector(".rail-tip")).toBeNull();
  fireEvent.click(home);
  expect(onNewChat).toHaveBeenCalledTimes(1);
  const plus = screen.getByTestId("nav-new-chat");
  expect(plus.parentElement?.querySelector(".rail-tip")?.textContent).toBe(
    "New Chat",
  );
});

describe("the plus beside the name", () => {
  const rail = (over: Partial<Parameters<typeof NavRail>[0]>) => (
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
      {...over}
    />
  );

  it("is a labelled row at the head of the wide rail's list, as History is", () => {
    render(rail({ canWidenRail: true, railLabelsWide: true }));
    const plus = screen.getByTestId("nav-new-chat");
    expect(plus).toHaveTextContent("New Chat");
    expect(plus.closest(".rail-header-wide")).toBeNull();
    expect(plus).toHaveClass("rail-nav-hit-wide");
    expect(
      plus.closest(".rail-tip-host")?.querySelector(".rail-tip"),
    ).toBeNull();
    const items = [
      ...document.querySelectorAll(".rail-middle [data-testid^='nav-']"),
    ].map((e) => e.getAttribute("data-testid"));
    expect(items[0]).toBe("nav-new-chat");
    expect(items[1]).toBe("nav-history");
  });

  it("is an icon alone with a tooltip where the rail has no labels", () => {
    render(rail({ canWidenRail: true }));
    const plus = screen.getByTestId("nav-new-chat");
    expect(plus.textContent).toBe("");
    expect(plus.parentElement?.querySelector(".rail-tip")?.textContent).toBe(
      "New Chat",
    );
  });

  it.each([
    ["the top bar and the narrow rail", { canWidenRail: false }],
    ["a narrow rail that can widen", { canWidenRail: true }],
    ["the wide rail", { canWidenRail: true, railLabelsWide: true }],
  ])("starts a new chat from %s", (_where, over) => {
    const onNewChat = vi.fn();
    render(rail({ ...over, onNewChat }));
    const plus = screen.getByTestId("nav-new-chat");
    expect(plus).toHaveAccessibleName("New Chat");
    expect(plus.getAttribute("href")).toBe(
      screen.getByTestId("nav-home").getAttribute("href"),
    );
    // Right after the name, ahead of every other item of the rail.
    const items = [...document.querySelectorAll("[data-testid^='nav-']")].map(
      (e) => e.getAttribute("data-testid"),
    );
    expect(items.indexOf("nav-new-chat")).toBe(items.indexOf("nav-home") + 1);
    fireEvent.click(plus);
    expect(onNewChat).toHaveBeenCalledTimes(1);
  });
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

test("nav shows localized active counts on History and Scheduler icons", () => {
  render(
    <NavRail
      onNewChat={() => {}}
      onOpenHistory={() => {}}
      historyOpen={false}
      historyActiveCount={2}
      onOpenScheduler={() => {}}
      schedulerOpen={false}
      schedulerActiveCount={3}
      onOpenSettings={() => {}}
      settingsOpen={false}
      canWidenRail={false}
      railLabelsWide={false}
      onToggleRailLabels={() => {}}
    />,
  );

  expect(screen.getByTestId("nav-history-active-count")).toHaveTextContent("2");
  expect(screen.getByTestId("nav-scheduler-active-count")).toHaveTextContent(
    "3",
  );
  expect(screen.getByTestId("nav-history")).toHaveAccessibleName(
    "History, 2 active sessions",
  );
  expect(screen.getByTestId("nav-scheduler")).toHaveAccessibleName(
    "Scheduler jobs, 3 active runs",
  );
});

test("wide rail keeps active counts over the History and Scheduler icons", () => {
  render(
    <NavRail
      onNewChat={() => {}}
      onOpenHistory={() => {}}
      historyOpen={false}
      historyActiveCount={2}
      onOpenScheduler={() => {}}
      schedulerOpen={false}
      schedulerActiveCount={3}
      onOpenSettings={() => {}}
      settingsOpen={false}
      canWidenRail
      railLabelsWide
      onToggleRailLabels={() => {}}
    />,
  );

  // The count rides on the icon, not on the row: the label beside it never
  // pushes it away (railActiveCountCss.test.ts holds the placement).
  for (const id of ["nav-history", "nav-scheduler"]) {
    const count = screen.getByTestId(`${id}-active-count`);
    const icon = count.parentElement!;
    expect(icon).toHaveClass("rail-nav-icon");
    expect(icon.querySelector("svg")).toBeTruthy();
    expect(icon.parentElement).toBe(screen.getByTestId(id));
    expect(
      screen.getByTestId(id).querySelector(".rail-nav-label"),
    ).toBeTruthy();
  }
});

test("narrow rail keeps active counts on the icons too", () => {
  render(
    <NavRail
      onNewChat={() => {}}
      onOpenHistory={() => {}}
      historyOpen={false}
      historyActiveCount={2}
      onOpenScheduler={() => {}}
      schedulerOpen={false}
      schedulerActiveCount={3}
      onOpenSettings={() => {}}
      settingsOpen={false}
      canWidenRail
      railLabelsWide={false}
      onToggleRailLabels={() => {}}
    />,
  );
  for (const id of ["nav-history", "nav-scheduler"]) {
    expect(screen.getByTestId(`${id}-active-count`).parentElement).toHaveClass(
      "rail-nav-icon",
    );
  }
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

  // Statistics is a screen of the rail like the documentation and the
  // settings, and it stands between them; the brand stays a link home.
  it("offers statistics between the documentation and the settings", () => {
    const onNewChat = vi.fn();
    const onOpenStatistics = vi.fn();
    const onOpenDocs = vi.fn();
    render(
      <NavRail
        {...base}
        onNewChat={onNewChat}
        onOpenDocs={onOpenDocs}
        onOpenStatistics={onOpenStatistics}
      />,
    );
    const order = Array.from(
      document.querySelectorAll("[data-testid^='nav-']"),
    ).map((e) => e.getAttribute("data-testid"));
    expect(order.indexOf("nav-statistics")).toBe(order.indexOf("nav-docs") + 1);
    expect(order.indexOf("nav-settings")).toBe(
      order.indexOf("nav-statistics") + 1,
    );
    const item = screen.getByTestId("nav-statistics");
    expect(item).toHaveAttribute("href", "#/statistics");
    fireEvent.click(item);
    expect(onOpenStatistics).toHaveBeenCalledTimes(1);
    fireEvent.click(screen.getByTestId("nav-home"));
    expect(onNewChat).toHaveBeenCalledTimes(1);
  });

  // The rail splits at the spacer: what belongs to this session above it,
  // what belongs to the installation below. Swarm is the fleet, so it sits
  // with Settings.
  it("puts swarm at the foot of the rail, above settings and the environment", () => {
    render(<NavRail {...base} showSwarm showScheduler />);
    const order = Array.from(
      document.querySelectorAll("[data-testid^='nav-']"),
    ).map((e) => e.getAttribute("data-testid"));
    expect(order).toEqual([
      "nav-home",
      "nav-new-chat",
      "nav-history",
      "nav-scheduler",
      "nav-swarm",
      "nav-settings",
      "nav-environment",
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
    render(
      <NavRail {...base} showSwarm onOpenDocs={onOpenDocs} docsOpen={false} />,
    );
    const order = Array.from(
      document.querySelectorAll("[data-testid^='nav-']"),
    ).map((e) => e.getAttribute("data-testid"));
    expect(order.slice(-4)).toEqual([
      "nav-swarm",
      "nav-docs",
      "nav-settings",
      "nav-environment",
    ]);
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

// On a phone and a tablet the top bar is a fixed row: History, the Scheduler,
// the environment and, on a relay, the swarm, then the More button that holds
// everything else (navOverflow.ts). jsdom does no layout and nothing is
// measured any more, so the stacked shell alone is stubbed through matchMedia.
// The order the row paints in is held by navBarOrderCss.test.ts.
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

  function stubStacked(stacked: boolean) {
    vi.stubGlobal("matchMedia", (query: string) => ({
      matches: query.includes("max-width") ? stacked : false,
      media: query,
      addEventListener: () => {},
      removeEventListener: () => {},
      addListener: () => {},
      removeListener: () => {},
      dispatchEvent: () => false,
    }));
    restore.push(() => vi.unstubAllGlobals());
  }

  function signIn() {
    setAuthState({
      loginRequired: true,
      authRequired: true,
      oidcEnabled: false,
      authenticated: true,
      user: "demo",
      secondFactor: "",
      telegramRefused: false,
      telegramProblem: "",
      loaded: true,
    });
    restore.push(() => resetAuthStateForTests());
  }

  function barIds(): string[] {
    const middle = document.querySelector(".rail-middle")!;
    return Array.from(
      middle.querySelectorAll(
        ":scope > .rail-tip-host > [data-testid^='nav-']",
      ),
    ).map((e) => e.getAttribute("data-testid")!);
  }

  afterEach(() => {
    cleanup();
    while (restore.length) restore.pop()!();
  });

  it("keeps history, the scheduler and the environment in the bar, the rest behind More", () => {
    signIn();
    stubStacked(true);
    render(<NavRail {...base} onOpenDocs={() => {}} />);
    expect(barIds()).toEqual([
      "nav-history",
      "nav-scheduler",
      "nav-environment",
      "nav-more",
    ]);
    expect(screen.queryByTestId("nav-docs")).toBeNull();
    expect(screen.queryByTestId("nav-settings")).toBeNull();
    expect(screen.queryByTestId("nav-sign-out")).toBeNull();
  });

  it("adds the swarm to the bar on a relay, beside the environment", () => {
    stubStacked(true);
    render(<NavRail {...base} showSwarm onOpenDocs={() => {}} />);
    expect(barIds()).toEqual([
      "nav-history",
      "nav-scheduler",
      "nav-swarm",
      "nav-environment",
      "nav-more",
    ]);
  });

  it("holds the installation's surfaces, sign-out last under a separator", async () => {
    signIn();
    stubStacked(true);
    render(
      <ExtensionsProvider>
        <NavRail {...base} onOpenDocs={() => {}} onOpenStatistics={() => {}} />
      </ExtensionsProvider>,
    );
    const more = await screen.findByTestId("nav-more");
    expect(more).toHaveAttribute("aria-label", "More");
    expect(more).toHaveAttribute("aria-haspopup", "menu");
    expect(more).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByRole("menu")).toBeNull();

    fireEvent.click(more);
    expect(more).toHaveAttribute("aria-expanded", "true");
    const menu = screen.getByRole("menu");
    expect(
      Array.from(menu.children).map(
        (e) => e.getAttribute("data-testid") ?? e.getAttribute("role"),
      ),
    ).toEqual([
      "nav-extensions",
      "nav-more-docs",
      "nav-more-statistics",
      "nav-more-settings",
      "separator",
      "nav-more-sign-out",
    ]);
    expect(screen.getByTestId("nav-more-docs")).toHaveAttribute(
      "href",
      "#/docs",
    );
    expect(screen.getByTestId("nav-more-docs")).toHaveAttribute(
      "role",
      "menuitem",
    );
    expect(screen.getByTestId("nav-more-docs")).toHaveTextContent("Docs");
    expect(screen.getByTestId("nav-more-sign-out")).toHaveAttribute(
      "role",
      "menuitem",
    );
  });

  it("shows no sign-out row when nobody is signed in", () => {
    stubStacked(true);
    render(<NavRail {...base} onOpenDocs={() => {}} />);
    fireEvent.click(screen.getByTestId("nav-more"));
    expect(screen.queryByTestId("nav-more-sign-out")).toBeNull();
  });

  it("keeps the scheduler's active count on its bar item", () => {
    stubStacked(true);
    render(
      <NavRail {...base} schedulerActiveCount={2} onOpenDocs={() => {}} />,
    );
    const count = screen.getByTestId("nav-scheduler-active-count");
    expect(count).toHaveTextContent("2");
    expect(count.parentElement).toHaveClass("rail-nav-icon");
    expect(screen.getByTestId("nav-scheduler")).toHaveAccessibleName(
      "Scheduler jobs, 2 active runs",
    );
  });

  it("picking a folded item opens it and closes the menu", () => {
    const onOpenDocs = vi.fn();
    stubStacked(true);
    render(<NavRail {...base} onOpenDocs={onOpenDocs} />);
    fireEvent.click(screen.getByTestId("nav-more"));
    fireEvent.click(screen.getByTestId("nav-more-docs"));
    expect(onOpenDocs).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("menu")).toBeNull();
    expect(screen.getByTestId("nav-more")).toHaveAttribute(
      "aria-expanded",
      "false",
    );
  });

  it("Escape and a press outside close the menu", () => {
    stubStacked(true);
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

  it("Escape folds the menu and leaves the screen open under it for the next one", () => {
    stubStacked(true);
    const closeDocs = vi.fn();
    render(
      <>
        <OpenRailScreen id="docs" onClose={closeDocs} />
        <NavRail {...base} onOpenDocs={() => {}} docsOpen />
      </>,
    );
    fireEvent.click(screen.getByTestId("nav-more"));
    fireEvent.keyDown(document.body, { key: "Escape" });
    expect(screen.queryByRole("menu")).toBeNull();
    expect(closeDocs).not.toHaveBeenCalled();
    fireEvent.keyDown(document.body, { key: "Escape" });
    expect(closeDocs).toHaveBeenCalledTimes(1);
  });

  it("More lights up while a folded panel is open", () => {
    stubStacked(true);
    const { rerender } = render(
      <NavRail {...base} onOpenDocs={() => {}} settingsOpen={false} />,
    );
    expect(screen.getByTestId("nav-more").className).not.toContain("is-active");
    rerender(<NavRail {...base} onOpenDocs={() => {}} settingsOpen />);
    expect(screen.getByTestId("nav-more").className).toContain("is-active");
  });

  it("a desktop rail never folds", () => {
    signIn();
    stubStacked(false);
    render(<NavRail {...base} onOpenDocs={() => {}} />);
    expect(barIds()).toEqual([
      "nav-history",
      "nav-scheduler",
      "nav-docs",
      "nav-settings",
      "nav-environment",
      "nav-sign-out",
    ]);
    expect(screen.queryByTestId("nav-more")).toBeNull();
  });
});
