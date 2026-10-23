import React from "react";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { AssistantMessage } from "./AssistantMessage";
import { formatUtcToLocalFullDetail } from "./formatMessageTime";

afterEach(() => cleanup());

// An answer is prose alone: no copy control and no minute under it, finished
// or not. The row they made appeared when a turn ended, right above the todo
// checklist, which then jumped down by its height. When the answer was
// written is in the tooltip of the answer.
test("an answer carries no copy control and no time, its time is in a tooltip", () => {
  const createdAtUtc = "2026-01-01T10:15:30.000Z";
  const { container } = render(
    <AssistantMessage
      content="# Title"
      streaming={false}
      createdAtUtc={createdAtUtc}
    />,
  );
  expect(screen.queryByRole("button", { name: "Copy message" })).toBeNull();
  expect(container.querySelector("time")).toBeNull();
  expect(container.querySelector(".msg-assistant")).toHaveAttribute(
    "title",
    formatUtcToLocalFullDetail(createdAtUtc),
  );
});

test("an answer without a time has no tooltip", () => {
  const { container } = render(<AssistantMessage content="Hi" streaming />);
  expect(container.querySelector(".msg-assistant")).not.toHaveAttribute(
    "title",
  );
});

test("renders only a verified file marker as an inline artifact card with actions", () => {
  const artifact = {
    id: "artifact-1",
    name: "report.pdf",
    sha256: "a".repeat(64),
    size: 1024,
    url: "/coddy/sessions/s1/artifacts/artifact-1",
    relativePath: "out/report.pdf",
  };
  render(
    <AssistantMessage
      content={
        'Ready.\n\n<coddy_file id="artifact-1"/>\n\n<coddy_file id="invented"/>'
      }
      artifacts={new Map([[artifact.id, artifact]])}
    />,
  );
  expect(screen.getByTestId("inline-artifact-card-artifact-1")).toBeVisible();
  expect(screen.getByText('<coddy_file id="invented"/>')).toBeVisible();
  fireEvent.click(
    screen.getByRole("button", { name: "Actions for report.pdf" }),
  );
  expect(screen.getByRole("menu")).toBeVisible();
  expect(
    screen.getByRole("menuitem", { name: "Mention in chat" }),
  ).toBeEnabled();
});

test("groups adjacent shared files and opens every image in the shared lightbox", () => {
  const report = {
    id: "report",
    name: "release-report.pdf",
    sha256: "a".repeat(64),
    size: 1024,
    url: "/coddy/sessions/s1/artifacts/report",
  };
  const chart = {
    id: "chart",
    name: "release-chart.png",
    sha256: "b".repeat(64),
    size: 2048,
    url: "/coddy/sessions/s1/artifacts/chart",
    previewUrl: "/coddy/sessions/s1/artifacts/chart/preview",
  };
  const cover = {
    id: "cover",
    name: "release-cover.webp",
    sha256: "c".repeat(64),
    size: 4096,
    url: "/coddy/sessions/s1/artifacts/cover",
    previewUrl: "/coddy/sessions/s1/artifacts/cover/preview",
  };
  render(
    <AssistantMessage
      content={
        'Prepared files:\n\n<coddy_file id="report"/>\n<coddy_file id="chart"/>\n<coddy_file id="cover"/>\n\nOpen either image for a preview.'
      }
      artifacts={
        new Map([
          [report.id, report],
          [chart.id, chart],
          [cover.id, cover],
        ])
      }
    />,
  );

  const groups = screen.getAllByRole("region", { name: "Shared files" });
  expect(groups).toHaveLength(1);
  expect(within(groups[0]!).getAllByRole("article")).toHaveLength(3);
  fireEvent.click(
    within(groups[0]!).getByRole("button", { name: "Open release-cover.webp" }),
  );
  expect(screen.getByRole("dialog")).toHaveTextContent("release-cover.webp");
});
