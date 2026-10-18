import { afterEach, beforeAll, beforeEach, expect, test, vi } from "vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { loadOperatorCommandCard } from "./OperatorCommandCardLazy";
import { ToolCallMessage } from "./ToolCallMessage";
import { setLocale } from "../i18n/i18n";
import type {
  OperatorCommandRequest,
  OperatorCommandState,
} from "../chat/operatorCommand";
import {
  loadOperatorCommandRecords,
  mergeStoredOperatorCommandsIntoTranscript,
  upsertOperatorCommandRequest,
} from "../chat/operatorCommandSessionStore";
import type { TranscriptItem } from "../chat/types";

const SID = "sess_op";
const TC = "call_op_1";
const COMMAND =
  "sudo apt-get install -y ripgrep && rg --version | head -n 1 # a long line that must wrap and never be cut";
const ARGS = JSON.stringify({
  command: COMMAND,
  reason: "Installing packages needs root, which the agent does not have.",
  cwd: "/work/project",
});

const request: OperatorCommandRequest = {
  sessionId: SID,
  requestId: "opc_1",
  toolCallId: TC,
  command: COMMAND,
  reason: "Installing packages needs root, which the agent does not have.",
  cwd: "/work/project",
  shell: "bash",
  shellPath: "/usr/bin/bash",
  host: "devbox",
};

let posts: Array<{ url: string; body: unknown }>;
let answerStatus: number;

beforeEach(() => {
  posts = [];
  answerStatus = 204;
  window.sessionStorage.clear();
  vi.stubGlobal(
    "fetch",
    vi.fn(async (url: string, init?: RequestInit) => {
      posts.push({ url, body: JSON.parse(String(init?.body ?? "null")) });
      return new Response(null, { status: answerStatus });
    }),
  );
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  setLocale("en");
});

// The card is its own chunk; load it once so every render below is
// synchronous, as it is in the app once the first card has appeared.
beforeAll(async () => {
  await loadOperatorCommandCard();
});

function renderCall(
  state: OperatorCommandState | undefined,
  extra: Partial<Parameters<typeof ToolCallMessage>[0]> = {},
) {
  return render(
    <ToolCallMessage
      toolCallId={TC}
      title="operator_command"
      status="in_progress"
      argsText={ARGS}
      sessionId={SID}
      {...(state ? { operatorCommand: state } : {})}
      {...extra}
    />,
  );
}

test("the command is shown whole and read-only, and nothing takes the focus", () => {
  renderCall({ request, requestId: request.requestId });
  const card = screen.getByTestId("operator-command-card");
  expect(card).toBeVisible();
  // The whole command, in the shell block behind its prompt.
  expect(
    card.querySelector(".permission-preview-shell-code")?.textContent,
  ).toBe(COMMAND);
  expect(screen.getByTestId("operator-command-shell")).toHaveTextContent(
    "bash",
  );
  expect(screen.getByTestId("operator-command-host")).toHaveTextContent(
    "devbox",
  );
  expect(screen.getByTestId("operator-command-cwd")).toHaveTextContent(
    "/work/project",
  );
  expect(screen.getByText("Agent's reason")).toBeInTheDocument();
  // Nothing on the card can be edited.
  expect(screen.queryAllByRole("textbox")).toHaveLength(0);
  expect(card.querySelector("input, textarea, [contenteditable]")).toBeNull();
  // Run is offered, and is not where the next key goes.
  const run = screen.getByTestId("operator-command-run");
  expect(run).toHaveTextContent("Run");
  expect(screen.getByTestId("operator-command-decline")).toHaveTextContent(
    "Decline",
  );
  expect(document.activeElement).toBe(document.body);
  expect(run).not.toHaveFocus();
});

test("Run posts the run action for the request", async () => {
  upsertOperatorCommandRequest(request);
  renderCall({ request, requestId: request.requestId });
  fireEvent.click(screen.getByTestId("operator-command-run"));
  await waitFor(() => expect(posts).toHaveLength(1));
  expect(posts[0]).toEqual({
    url: `/coddy/sessions/${SID}/operator-command`,
    body: { requestId: "opc_1", action: "run" },
  });
  // The answer is kept, so a reload does not offer Run again.
  await waitFor(() =>
    expect(loadOperatorCommandRecords(SID)[0]?.answered).toBe("run"),
  );
  expect(screen.queryByTestId("operator-command-run")).toBeNull();
});

test("Decline posts the decline action", async () => {
  renderCall({ request, requestId: request.requestId });
  fireEvent.click(screen.getByTestId("operator-command-decline"));
  await waitFor(() => expect(posts).toHaveLength(1));
  expect(posts[0]!.body).toEqual({ requestId: "opc_1", action: "decline" });
  await waitFor(() =>
    expect(screen.getByTestId("operator-command-card")).toHaveTextContent(
      "Declined",
    ),
  );
});

test("a running command streams its output and Stop, then Kill, post stop", async () => {
  const { rerender } = renderCall({
    request,
    requestId: "opc_1",
    answered: "run",
    phase: "running",
    output: "Reading package lists...\nBuilding dependency tree...",
  });
  expect(screen.getByTestId("operator-command-output")).toHaveTextContent(
    "Building dependency tree...",
  );
  expect(screen.queryByTestId("operator-command-run")).toBeNull();
  const stop = screen.getByTestId("operator-command-stop");
  expect(stop).toHaveTextContent("Stop");
  fireEvent.click(stop);
  await waitFor(() => expect(posts).toHaveLength(1));
  expect(posts[0]!.body).toEqual({ requestId: "opc_1", action: "stop" });

  // The server reports the stop; the second press kills.
  rerender(
    <ToolCallMessage
      toolCallId={TC}
      title="operator_command"
      status="in_progress"
      argsText={ARGS}
      sessionId={SID}
      operatorCommand={{
        request,
        requestId: "opc_1",
        phase: "stopping",
        output: "Reading package lists...",
      }}
    />,
  );
  expect(screen.getByTestId("operator-command-stop")).toHaveTextContent("Kill");
  fireEvent.click(screen.getByTestId("operator-command-stop"));
  await waitFor(() => expect(posts).toHaveLength(2));
  expect(posts[1]!.body).toEqual({ requestId: "opc_1", action: "stop" });
});

test("an answer another surface gave first shows a note and takes the buttons away", async () => {
  answerStatus = 409;
  renderCall({ request, requestId: request.requestId });
  fireEvent.click(screen.getByTestId("operator-command-run"));
  await waitFor(() =>
    expect(screen.getByTestId("operator-command-note")).toHaveTextContent(
      "Already answered elsewhere",
    ),
  );
  expect(screen.queryByTestId("operator-command-run")).toBeNull();
});

test("an ended request says so", async () => {
  answerStatus = 404;
  renderCall({ request, requestId: request.requestId });
  fireEvent.click(screen.getByTestId("operator-command-decline"));
  await waitFor(() =>
    expect(screen.getByTestId("operator-command-note")).toHaveTextContent(
      "This request has ended",
    ),
  );
});

test("a waiting request kept before a reload shows Run again on the restored row", () => {
  upsertOperatorCommandRequest(request);
  // What a transcript read gives after the reload: the row, no operator state.
  const fromServer: TranscriptItem[] = [
    {
      id: "t1",
      type: "tool_call",
      toolCallId: TC,
      title: "operator_command",
      status: "in_progress",
      argsText: ARGS,
    },
  ];
  const merged = mergeStoredOperatorCommandsIntoTranscript(fromServer, SID);
  const row = merged[0] as Extract<TranscriptItem, { type: "tool_call" }>;
  expect(row.operatorCommand?.request?.requestId).toBe("opc_1");
  renderCall(row.operatorCommand);
  expect(screen.getByTestId("operator-command-run")).toBeVisible();
});

test("a finished call drops its kept request and reads as its record", () => {
  upsertOperatorCommandRequest(request);
  const result =
    "The operator ran the command in bash on devbox, in /srv/app (exit code 0):\nripgrep 14.1.0";
  const fromServer: TranscriptItem[] = [
    {
      id: "t1",
      type: "tool_call",
      toolCallId: TC,
      title: "operator_command",
      status: "completed",
      argsText: ARGS,
      resultText: result,
    },
  ];
  mergeStoredOperatorCommandsIntoTranscript(fromServer, SID);
  expect(loadOperatorCommandRecords(SID)).toHaveLength(0);

  renderCall(undefined, { status: "completed", resultText: result });
  // A finished call folds like any record; opening it shows how it ended.
  act(() => {
    fireEvent.click(screen.getByLabelText("Tool summary"));
  });
  expect(screen.getByTestId("operator-command-end")).toHaveTextContent(
    "Exit code 0",
  );
  expect(screen.getByTestId("operator-command-output")).toHaveTextContent(
    "ripgrep 14.1.0",
  );
  expect(screen.queryByTestId("operator-command-run")).toBeNull();
  expect(screen.queryByTestId("operator-command-stop")).toBeNull();
  // Where it ran comes back from the result's header after a reload.
  const card = screen.getByTestId("operator-command-card");
  expect(card).toHaveTextContent("bash");
  expect(card).toHaveTextContent("devbox");
  expect(card).toHaveTextContent("/srv/app");
});

test("a stopped and a declined call say how they ended", () => {
  const { rerender } = renderCall(undefined, {
    status: "completed",
    resultText:
      "The operator stopped the command in bash on devbox, in /srv/app (exit code 130). Output so far:\npartial",
  });
  expect(screen.getByTestId("operator-command-end")).toHaveTextContent(
    "Stopped (exit code 130)",
  );
  rerender(
    <ToolCallMessage
      toolCallId={TC}
      title="operator_command"
      status="completed"
      argsText={ARGS}
      resultText="The operator declined to run the command."
    />,
  );
  expect(screen.getByTestId("operator-command-end")).toHaveTextContent(
    "Declined",
  );
});

test("the terminal of a multiplexed run is offered under a disclosure", () => {
  renderCall({
    request: {
      ...request,
      multiplexer: "tmux",
      attachCommand: "tmux attach -t coddy-opc_1",
    },
    requestId: "opc_1",
    answered: "run",
    phase: "running",
  });
  expect(screen.getByTestId("operator-command-multiplexer")).toHaveTextContent(
    "tmux",
  );
  const summary = screen.getByText("Watch it in a terminal");
  expect(summary.closest("details")).not.toHaveAttribute("open");
  expect(
    summary.closest("details")?.querySelector(".permission-preview-shell-code")
      ?.textContent,
  ).toBe("tmux attach -t coddy-opc_1");
});

test("the Russian card names its buttons in Russian", () => {
  setLocale("ru");
  renderCall({ request, requestId: request.requestId });
  expect(screen.getByText("Запустите сами")).toBeInTheDocument();
  expect(screen.getByTestId("operator-command-run")).toHaveTextContent(
    "Запустить",
  );
  expect(screen.getByTestId("operator-command-decline")).toHaveTextContent(
    "Отклонить",
  );
});

test("the card names the shell's executable, not only its kind", () => {
  renderCall({ request, requestId: request.requestId });
  expect(screen.getByTestId("operator-command-shell-path")).toHaveTextContent(
    "/usr/bin/bash",
  );
});

test("a command that outlived a cancelled turn still offers Stop", () => {
  renderCall(
    { request, requestId: request.requestId, phase: "stopping", output: "x" },
    { status: "cancelled" },
  );
  expect(screen.getByTestId("operator-command-stop")).toBeVisible();
});

// The command on the card is the one the server holds and will run - the
// request's - never the call's arguments, should the two ever differ.
test("the card shows the request's command over the arguments'", () => {
  const held = { ...request, command: "echo the-command-that-runs" };
  renderCall({ request: held, requestId: held.requestId });
  const card = screen.getByTestId("operator-command-card");
  expect(
    card.querySelector(".permission-preview-shell-code")?.textContent,
  ).toBe("echo the-command-that-runs");
  expect(card).not.toHaveTextContent("apt-get");
});

test("a Stop the server answers with 404 retires the live card", async () => {
  answerStatus = 404;
  renderCall({
    request,
    requestId: "opc_1",
    answered: "run",
    phase: "stopping",
    output: "still going",
  });
  fireEvent.click(screen.getByTestId("operator-command-stop"));
  await waitFor(() =>
    expect(screen.getByTestId("operator-command-note")).toHaveTextContent(
      "This request has ended",
    ),
  );
  expect(screen.queryByTestId("operator-command-stop")).toBeNull();
  expect(screen.getByTestId("operator-command-card")).not.toHaveTextContent(
    "Stopping…",
  );
});

// A turn cancelled while its command ran ends the call cancelled, or failed
// with the cancel's error: the run itself still has its exit code and output.
test("a command stopped by a cancelled turn ends with its own exit code and output", () => {
  for (const [status, resultText] of [
    ["cancelled", ""],
    ["failed", "error: context canceled"],
  ] as const) {
    const { unmount } = renderCall(
      {
        request,
        requestId: "opc_1",
        phase: "stopped",
        exitCode: 130,
        output: "interrupted at step 3",
      },
      { status, resultText },
    );
    expect(screen.getByTestId("operator-command-end")).toHaveTextContent(
      "Stopped (exit code 130)",
    );
    expect(screen.getByTestId("operator-command-output")).toHaveTextContent(
      "interrupted at step 3",
    );
    expect(screen.getByTestId("operator-command-card")).not.toHaveTextContent(
      "Did not run",
    );
    unmount();
  }
});

test("an end without an exit code claims no success", () => {
  renderCall({ request, requestId: "opc_1", phase: "ended", output: "done" });
  const end = screen.getByTestId("operator-command-end");
  expect(end).not.toHaveTextContent("Exit code 0");
  expect(end.className).not.toContain("operator-command-state--ok");
});

test("the attach command is offered only while the command runs", () => {
  const muxed = {
    ...request,
    multiplexer: "tmux",
    attachCommand: "tmux -L coddy attach -t '=coddy-x:=oc-y'",
  };
  const { unmount } = renderCall({ request: muxed, requestId: "opc_1" });
  expect(screen.queryByText("Watch it in a terminal")).toBeNull();
  unmount();
  renderCall({
    request: muxed,
    requestId: "opc_1",
    answered: "run",
    phase: "running",
  });
  expect(screen.getByText("Watch it in a terminal")).toBeInTheDocument();
});

test("a row that turns live after it mounted opens once", () => {
  const props = {
    toolCallId: TC,
    title: "operator_command",
    argsText: ARGS,
    sessionId: SID,
  };
  const { rerender } = render(
    <ToolCallMessage {...props} status="cancelled" />,
  );
  const details = screen.getByTestId(`tool-details-${TC}`);
  expect(details).not.toHaveAttribute("open");
  rerender(
    <ToolCallMessage
      {...props}
      status="cancelled"
      operatorCommand={{
        request,
        requestId: "opc_1",
        phase: "stopping",
        output: "still going",
      }}
    />,
  );
  expect(details).toHaveAttribute("open");
  expect(screen.getByTestId("operator-command-stop")).toBeVisible();
});

test("a Stop for a request the server no longer has forgets the kept request", async () => {
  answerStatus = 404;
  upsertOperatorCommandRequest(request);
  renderCall(
    { request, requestId: "opc_1", phase: "stopping" },
    { status: "cancelled" },
  );
  fireEvent.click(screen.getByTestId("operator-command-stop"));
  await waitFor(() => expect(loadOperatorCommandRecords(SID)).toHaveLength(0));
});
