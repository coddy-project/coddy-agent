import React from "react";
import { afterEach, expect, test } from "vitest";
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
  expect(screen.getByTestId("nav-history")).toHaveAttribute("href", "#/history");
  expect(screen.getByTestId("nav-scheduler")).toHaveAttribute("href", "#/scheduler");
  expect(screen.getByTestId("nav-settings")).toHaveAttribute("href", "#/settings");
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
