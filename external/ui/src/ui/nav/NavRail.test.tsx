import React from "react";
import { afterEach, describe, expect, it, test } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { NavRail } from "./NavRail";

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
    expect(
      spacer.compareDocumentPosition(swarm) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
  });
});
