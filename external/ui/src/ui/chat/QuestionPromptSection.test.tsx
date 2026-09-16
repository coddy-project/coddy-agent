import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, test, vi } from "vitest";
import { QuestionPromptSection } from "./QuestionPromptSection";
import type { CoddyQuestionPayload } from "./questionTypes";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const payload: CoddyQuestionPayload = {
  sessionId: "sess_x",
  requestId: "q_1",
  questions: [
    {
      question: "Which scheduler did you mean?",
      options: [{ label: "Todo plan" }, { label: "Background tasks" }],
      custom: true,
    },
  ],
};

function renderPrompt(onResolved: (r: unknown) => void) {
  const fetchMock = vi.fn(async () => new Response("{}", { status: 200 }));
  vi.stubGlobal("fetch", fetchMock);
  render(
    <QuestionPromptSection
      itemId="qp_1"
      payload={payload}
      onResolved={onResolved as never}
    />,
  );
  return fetchMock;
}

// The Continue button advertises the Return key, so typing a custom answer and
// pressing Return has to submit it. It used to swallow the key and do nothing.
test("Return in the custom answer field submits the answer", async () => {
  const resolved = vi.fn();
  renderPrompt(resolved);

  const other = screen.getByTestId("question-other-0") as HTMLInputElement;
  fireEvent.focus(other);
  fireEvent.change(other, { target: { value: "через скил configure-coddy" } });
  fireEvent.keyDown(other, { key: "Enter" });

  await waitFor(() => expect(resolved).toHaveBeenCalledTimes(1));
  expect(resolved.mock.calls[0]?.[0]).toMatchObject({
    skipped: false,
    answers: [["через скил configure-coddy"]],
  });
});

// The same key works for a plain option pick, with nothing else focused.
test("Return submits a picked option", async () => {
  const resolved = vi.fn();
  renderPrompt(resolved);

  fireEvent.click(screen.getByText("Todo plan"));
  fireEvent.keyDown(document.body, { key: "Enter" });

  await waitFor(() => expect(resolved).toHaveBeenCalledTimes(1));
  expect(resolved.mock.calls[0]?.[0]).toMatchObject({
    skipped: false,
    answers: [["Todo plan"]],
  });
});

// Nothing picked yet: Return must not send an empty answer.
test("Return does nothing while the answer is incomplete", async () => {
  const resolved = vi.fn();
  renderPrompt(resolved);

  fireEvent.keyDown(document.body, { key: "Enter" });
  await Promise.resolve();

  expect(resolved).not.toHaveBeenCalled();
});

// The composer stays usable while a gate is open, so Return typed there sends the
// message and must not also answer the question behind it.
test("Return typed in the composer leaves the question alone", async () => {
  const resolved = vi.fn();
  renderPrompt(resolved);
  fireEvent.click(screen.getByText("Todo plan"));

  const composer = document.createElement("textarea");
  document.body.appendChild(composer);
  const ev = new KeyboardEvent("keydown", {
    key: "Enter",
    bubbles: true,
    cancelable: true,
  });
  composer.dispatchEvent(ev);
  await Promise.resolve();

  expect(resolved).not.toHaveBeenCalled();
  expect(ev.defaultPrevented).toBe(false);
  composer.remove();
});

// Escape keeps skipping the question.
test("Escape skips the question", async () => {
  const resolved = vi.fn();
  renderPrompt(resolved);

  fireEvent.keyDown(window, { key: "Escape" });

  await waitFor(() => expect(resolved).toHaveBeenCalledTimes(1));
  expect(resolved.mock.calls[0]?.[0]).toMatchObject({ skipped: true });
});

// Cross-review: a control reached with the keyboard keeps its own Return, inside
// the card as much as outside it.
test("Return on the focused Skip button skips instead of sending", async () => {
  const resolved = vi.fn();
  renderPrompt(resolved);
  fireEvent.click(screen.getByText("Todo plan"));

  const skip = screen.getByTestId("question-skip");
  const ev = new KeyboardEvent("keydown", {
    key: "Enter",
    bubbles: true,
    cancelable: true,
  });
  skip.dispatchEvent(ev);
  await Promise.resolve();

  // The card left the key alone, so the button's own activation still runs it.
  expect(ev.defaultPrevented).toBe(false);
  expect(resolved).not.toHaveBeenCalled();
  fireEvent.click(skip);
  await waitFor(() => expect(resolved).toHaveBeenCalledTimes(1));
  expect(resolved.mock.calls[0]?.[0]).toMatchObject({ skipped: true });
});

// An answer that is not ready yet leaves Return to the rest of the page.
test("Return is not taken off the page while the answer is incomplete", async () => {
  renderPrompt(vi.fn());

  const ev = new KeyboardEvent("keydown", {
    key: "Enter",
    bubbles: true,
    cancelable: true,
  });
  document.body.dispatchEvent(ev);
  await Promise.resolve();

  expect(ev.defaultPrevented).toBe(false);
});

// An answered card is a record of one exchange: the question, and under it what
// the reader said. It used to carry a summary line on its head and the same two
// lines again inside a foldout - the same thing twice, behind a control that only
// ever revealed what was already on screen.
test("an answered card states the question once and the answer under it", () => {
  const { container } = render(
    <QuestionPromptSection
      itemId="qp_1"
      payload={payload}
      resolved={{
        skipped: false,
        answers: [["через скил configure-coddy"]],
        summaryLine:
          "Which scheduler did you mean? через скил configure-coddy",
      }}
      onResolved={() => {}}
    />,
  );

  expect(container.querySelector("details")).toBeNull();
  expect(container.querySelector("summary")).toBeNull();
  expect(container.querySelector(".question-prompt-summary-line")).toBeNull();

  expect(container.querySelectorAll(".question-prompt-resolved-q")).toHaveLength(1);
  expect(
    container.querySelector(".question-prompt-resolved-q")?.textContent,
  ).toBe("Which scheduler did you mean?");
  expect(
    container.querySelector(".question-prompt-resolved-a")?.textContent,
  ).toBe("через скил configure-coddy");

  // The options stay on the tool row that recorded the offer, not here.
  expect(container.querySelector(".question-tool-offer")).toBeNull();
  expect(container.textContent).not.toContain("Todo plan");
});

test("a skipped card says so and names the missing answer", () => {
  const { container } = render(
    <QuestionPromptSection
      itemId="qp_2"
      payload={payload}
      resolved={{ skipped: true, answers: [[]], summaryLine: "Skipped" }}
      onResolved={() => {}}
    />,
  );

  expect(screen.getByText("Skipped")).toBeInTheDocument();
  expect(
    container.querySelector(".question-prompt-resolved-a")?.textContent,
  ).toBe("(no answer)");
});
