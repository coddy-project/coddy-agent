import { useState } from "react";
import { afterEach, expect, test } from "vitest";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { ToolCallMessage } from "./ToolCallMessage";
import { setLocale } from "../i18n/i18n";

afterEach(() => {
  cleanup();
  setLocale("en");
});

function show(title: string, args: object, result: string) {
  render(
    <ToolCallMessage
      toolCallId={title}
      title={title}
      status="completed"
      argsText={JSON.stringify(args)}
      resultText={result}
    />,
  );
  fireEvent.click(screen.getByLabelText("Tool summary"));
  return screen.getByTestId("structured-tool-card");
}

test("switch_model names the choice and lifetime", () => {
  const card = show(
    "switch_model",
    { model: "fast/model", reasoning: "high", scope: "session" },
    "model changed",
  );
  expect(screen.getByText("switching the model")).toBeInTheDocument();
  expect(within(card).getByText("fast/model")).toBeInTheDocument();
  expect(within(card).getByText("high")).toBeInTheDocument();
  expect(within(card).getByText("For this conversation")).toBeInTheDocument();
  expect(within(card).queryByText(/"model"/)).toBeNull();
});

test("http_request separates response status, headers and body and masks credentials", () => {
  const card = show(
    "http_request",
    {
      method: "POST",
      url: "https://example.test/items",
      headers: { Authorization: "Bearer secret", Accept: "application/json" },
      json: { a: 1 },
    },
    'HTTP/1.1 201 Created\nContent-Type: application/json\nSet-Cookie: token=secret\n\n{"ok":true}',
  );
  expect(
    within(card).getByText("POST https://example.test/items"),
  ).toBeInTheDocument();
  expect(within(card).getByText("201 Created")).toBeInTheDocument();
  expect(within(card).getByText("JSON · 7 bytes")).toBeInTheDocument();
  expect(
    within(card).getByText(/Accept: application\/json/),
  ).toBeInTheDocument();
  expect(card.textContent).not.toContain("secret");
  expect(within(card).getByText('{"ok":true}')).toBeInTheDocument();
});

test("background task calls present the task id and output", () => {
  const card = show(
    "background_wait",
    { task_id: "bg-42", timeout_seconds: 10 },
    "bg-42 [running] build (elapsed 10s)\nStill running after 10s.",
  );
  expect(within(card).getByText("bg-42")).toBeInTheDocument();
  expect(within(card).getByText("running")).toBeInTheDocument();
  expect(within(card).getByText(/Still running after 10s/)).toBeInTheDocument();
});

test("background_list separates each task's id, state and detail", () => {
  const card = show(
    "background_list",
    {},
    "bg-1 [running] build (elapsed 4s)\nbg-2 [completed] tests (elapsed 9s, exit 0)",
  );
  expect(within(card).getAllByRole("listitem")).toHaveLength(2);
  expect(within(card).getByText("bg-1")).toBeInTheDocument();
  expect(within(card).getByText("completed")).toBeInTheDocument();
});

test("preview_server exposes its address as a link", () => {
  const card = show(
    "preview_server",
    { path: "site" },
    "Preview server started: http://127.0.0.1:5000/\nServing site as background task bg-2.",
  );
  const link = within(card).getByRole("link", {
    name: "http://127.0.0.1:5000/",
  });
  expect(link).toHaveAttribute("href", "http://127.0.0.1:5000/");
  expect(within(card).getByText("bg-2")).toBeInTheDocument();
});

test("documentation search displays references as rows", () => {
  const card = show(
    "coddy_docs_search",
    { query: "hooks", limit: 8 },
    'Coddy dev documentation: 1 sections for "hooks", best first.\n\n1. features/hooks  (Hooks > Trust)\n   Approve project hooks',
  );
  expect(within(card).getByText("Hooks > Trust")).toBeInTheDocument();
  expect(within(card).getByText("features/hooks")).toBeInTheDocument();
});

test("plan list, read and write have plan-specific bodies", () => {
  let card = show(
    "plan_list",
    {},
    '[{"slug":"launch","name":"Launch plan","updatedAt":"2026-09-23T12:00:00Z"}]',
  );
  expect(within(card).getByText("Launch plan")).toBeInTheDocument();
  cleanup();
  card = show(
    "plan_read",
    { slug: "launch" },
    "---\nname: Launch plan\n---\n# Steps\n\n- Ship it",
  );
  expect(within(card).getByText("Steps")).toBeInTheDocument();
  expect(card.textContent).not.toContain("name: Launch plan");
  cleanup();
  card = show(
    "plan_write",
    { slug: "launch", content: "---\nname: Launch plan\n---\n# Steps" },
    'wrote design plan "launch" (42 bytes)',
  );
  expect(within(card).getByText("launch")).toBeInTheDocument();
  expect(card.textContent).not.toContain('"content"');
});

test("malformed arguments retain the generic preview", () => {
  render(
    <ToolCallMessage
      toolCallId="bad"
      title="plan_read"
      status="completed"
      argsText='{"slug":'
      resultText="bad data"
    />,
  );
  fireEvent.click(screen.getByLabelText("Tool summary"));
  expect(screen.queryByTestId("structured-tool-card")).toBeNull();
  expect(screen.getByText("bad data")).toBeInTheDocument();
});

test("model change labels are localized", () => {
  setLocale("ru");
  render(
    <ToolCallMessage
      toolCallId="model-ru"
      title="switch_model"
      status="completed"
      argsText='{"model":"fast/model","scope":"turn"}'
      resultText="ok"
    />,
  );
  fireEvent.click(screen.getByLabelText("Сводка инструмента"));
  expect(screen.getByText("переключаю модель")).toBeInTheDocument();
  expect(screen.getByText("На этот ход")).toBeInTheDocument();
});

test("a truncated structured result keeps More and Less in a capped viewport", async () => {
  function Harness() {
    const [full, setFull] = useState("");
    return (
      <ToolCallMessage
        toolCallId="long-plan"
        title="plan_read"
        status="completed"
        argsText='{"slug":"launch"}'
        resultText="# Steps\n..."
        fullResultText={full}
        resultWasTruncated
        onFetchToolCallFull={async () =>
          setFull(`# Steps\n${"- Task\n".repeat(80)}`)
        }
      />
    );
  }
  render(<Harness />);
  fireEvent.click(screen.getByLabelText("Tool summary"));
  expect(screen.getByTestId("structured-tool-card").parentElement).toHaveClass(
    "tool-result-viewport--clip",
  );
  fireEvent.click(screen.getByTestId("tool-result-more"));
  await waitFor(() =>
    expect(screen.getByTestId("tool-result-less")).toBeInTheDocument(),
  );
  expect(screen.getByTestId("structured-tool-card").parentElement).toHaveClass(
    "tool-result-viewport--scroll",
  );
  fireEvent.click(screen.getByTestId("tool-result-less"));
  expect(screen.getByTestId("structured-tool-card").parentElement).toHaveClass(
    "tool-result-viewport--clip",
  );
});
