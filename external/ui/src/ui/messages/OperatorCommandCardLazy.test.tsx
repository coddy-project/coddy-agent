import { afterEach, expect, test, vi } from "vitest";
import { act, fireEvent, render, screen } from "@testing-library/react";

import {
  OperatorCommandCardLazy,
  __setOperatorCommandCardImporter,
} from "./OperatorCommandCardLazy";

afterEach(() => {
  __setOperatorCommandCardImporter(null);
});

const ARGS = JSON.stringify({
  command: "make deploy",
  reason: "needs the key",
});

// A chunk that does not load (a network blip, a tab left open across an
// upgrade) must not leave an empty card: the command stays readable, the
// operator can decline it, and the chunk is asked for again.
test("a card whose chunk fails to load shows the command, Decline and Retry", async () => {
  const importer = vi
    .fn()
    .mockRejectedValueOnce(new Error("chunk"))
    .mockImplementation(() => import("./OperatorCommandCard"));
  __setOperatorCommandCardImporter(importer);
  render(
    <OperatorCommandCardLazy
      toolCallId="call_x"
      sessionId="sess_x"
      status="in_progress"
      argsText={ARGS}
      state={{
        requestId: "oc_x",
        request: {
          sessionId: "sess_x",
          requestId: "oc_x",
          toolCallId: "call_x",
          command: "make deploy",
          reason: "needs the key",
          cwd: "/w",
          shell: "bash",
          shellPath: "/bin/bash",
          host: "h",
        },
      }}
    />,
  );
  const fallback = await screen.findByTestId("operator-command-fallback");
  expect(fallback).toHaveTextContent("make deploy");
  expect(screen.getByTestId("operator-command-decline")).toBeVisible();
  await act(async () => {
    fireEvent.click(screen.getByTestId("operator-command-retry"));
  });
  expect(await screen.findByTestId("operator-command-card")).toBeVisible();
  expect(importer).toHaveBeenCalledTimes(2);
});
