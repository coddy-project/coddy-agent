import { expect, test } from "vitest";

import {
  artifactMarkersForAssistant,
  groupAssistantArtifactTokens,
  tokenizeAssistantArtifacts,
} from "./inlineArtifacts";
import type { TranscriptItem } from "./types";

const artifact = {
  id: "artifact-1",
  name: "chart.png",
  sha256: "a".repeat(64),
  size: 2048,
  url: "/coddy/sessions/s1/artifacts/artifact-1",
  previewUrl: "/coddy/sessions/s1/artifacts/artifact-1/preview",
  sourcePath: "/work/project/out/chart.png",
};

test("only verified artifacts in the current turn turn coddy_file markers into cards", () => {
  const items: TranscriptItem[] = [
    { id: "u", type: "user_message", content: "make a chart" },
    {
      id: "tool",
      type: "tool_call",
      toolCallId: "share",
      title: "share_file",
      status: "completed",
      artifacts: [artifact],
    },
    {
      id: "answer",
      type: "assistant_message",
      content:
        'Done.\n\n<coddy_file id="artifact-1"/>\n\n<coddy_file id="invented"/>',
    },
  ];

  const markers = artifactMarkersForAssistant(items, 2);
  expect([...markers.keys()]).toEqual(["artifact-1"]);
  const answer = items[2]!;
  if (answer.type !== "assistant_message")
    throw new Error("expected assistant answer");
  expect(tokenizeAssistantArtifacts(answer.content, markers)).toEqual([
    { type: "markdown", text: "Done.\n\n" },
    { type: "artifact", artifact },
    { type: "markdown", text: '\n\n<coddy_file id="invented"/>' },
  ]);
});

test("a share_file artifact remains a detached fallback until an answer actually uses its marker", () => {
  const shared: TranscriptItem = {
    id: "tool",
    type: "tool_call",
    toolCallId: "share",
    title: "share_file",
    status: "completed",
    artifacts: [artifact],
  };
  const plainAnswer: TranscriptItem = {
    id: "answer",
    type: "assistant_message",
    content: "The file is ready.",
  };
  const markedAnswer: TranscriptItem = {
    id: "answer-marked",
    type: "assistant_message",
    content: '<coddy_file id="artifact-1"/>',
  };

  expect(artifactMarkersForAssistant([shared, plainAnswer], 1).size).toBe(1);
  expect(
    artifactMarkersForAssistant([shared, markedAnswer], 1).has("artifact-1"),
  ).toBe(true);
});

test("groups adjacent verified markers while preserving surrounding Markdown", () => {
  const second = { ...artifact, id: "artifact-2", name: "report.pdf" };
  const tokens = tokenizeAssistantArtifacts(
    'Files:\n\n<coddy_file id="artifact-1"/>\n <coddy_file id="artifact-2"/>\n\nRead the report.',
    new Map([
      [artifact.id, artifact],
      [second.id, second],
    ]),
  );

  expect(groupAssistantArtifactTokens(tokens)).toEqual([
    { type: "markdown", text: "Files:\n\n" },
    { type: "artifacts", artifacts: [artifact, second] },
    { type: "markdown", text: "\n\nRead the report." },
  ]);
});
