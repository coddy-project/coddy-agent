import React from "react";
import { afterEach } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { expect, test } from "vitest";
import { ThinkingMessage } from "./ThinkingMessage";
import { setLocale } from "../i18n/i18n";

afterEach(() => {
  cleanup();
  setLocale("en");
});

test("completed state uses plain thinking label without spinner", () => {
  const { container } = render(
    <ThinkingMessage
      status="completed"
      content="done reasoning"
      durationMs={12}
    />,
  );
  expect(screen.getByText("thinking")).toBeTruthy();
  expect(screen.queryByText("thinking...")).toBeNull();
  const details = container.querySelector("details");
  expect(details).toBeTruthy();
  expect(details?.getAttribute("open")).toBeNull();
  expect(container.querySelector(".thinking-dur")?.textContent).toBe("12ms");
});

// A turn whose reasoning arrived in one flush has no duration to report, and the
// dash the row used to print read as a failure next to rows showing milliseconds.
// The floor of the same scale says the same thing without looking broken.
// A reasoning block with no measured length - a non-streamed response, or history
// saved without one - used to read 0ms, which looked like a clock that had stopped.
// A length nobody measured is not shown at all.
test("completed without a measured duration shows no duration", () => {
  const { container } = render(
    <ThinkingMessage status="completed" content="x" />,
  );
  expect(container.querySelector(".thinking-dur")).toBeNull();
});

test("in_progress before the clock starts reads the same way", () => {
  const { container } = render(
    <ThinkingMessage status="in_progress" content="x" />,
  );
  expect(container.querySelector(".thinking-dur")).toBeNull();
});

test("in_progress shows thinking ellipsis and elapsed from startedAtMs", () => {
  const t0 = Date.now() - 2000;
  const { container } = render(
    <ThinkingMessage status="in_progress" content="" startedAtMs={t0} />,
  );
  expect(screen.getByText("thinking...")).toBeTruthy();
  const dur = container.querySelector(".thinking-dur")?.textContent ?? "";
  expect(dur).toMatch(/^\d+ms$|^\d/);
});

test("a long reasoning step reads in minutes and seconds, not in fractions of a minute", () => {
  const { container } = render(
    <ThinkingMessage status="completed" content="x" durationMs={102_000} />,
  );
  expect(container.querySelector(".thinking-dur")?.textContent).toBe("1m 42s");
});

// Every reasoning delta changes the row's content, and a closed row used to parse
// its whole Markdown body again for each one - on a long reasoning block that is
// the work that keeps the page busy while nothing on screen changes. The body is
// rendered when the reader opens the row.
test("a closed reasoning row renders its body only once it is opened", async () => {
  const { container } = render(
    <ThinkingMessage
      status="in_progress"
      content="**Weighing** the options"
      startedAtMs={Date.now()}
    />,
  );
  expect(container.querySelector(".thinking-body")).toBeNull();
  const details = container.querySelector("details")!;
  details.open = true;
  details.dispatchEvent(new Event("toggle"));
  expect(await screen.findByText("Weighing")).toBeInTheDocument();
});

// Issue #258: the row says what the reasoning cost and stands for reasoning a
// provider kept to itself.

test("a counted reasoning step shows its tokens after the duration", () => {
  const { container } = render(
    <ThinkingMessage
      status="completed"
      content="x"
      durationMs={1200}
      reasoningTokens={1250}
    />,
  );
  const tokens = container.querySelector(".thinking-tokens");
  expect(tokens?.textContent).toBe("1.2k tokens");
  expect(tokens?.getAttribute("title")).toBe("1,250 reasoning tokens");
  const left = container.querySelector(".thinking-left")!;
  const kids = Array.from(left.children);
  expect(kids.indexOf(tokens!)).toBe(
    kids.indexOf(container.querySelector(".thinking-dur")!) + 1,
  );
});

test("one reasoning token reads in the singular, and none reads nothing", () => {
  const one = render(
    <ThinkingMessage status="completed" content="x" reasoningTokens={1} />,
  );
  expect(one.container.querySelector(".thinking-tokens")?.textContent).toBe(
    "1 token",
  );
  one.unmount();
  const none = render(
    <ThinkingMessage status="completed" content="x" reasoningTokens={0} />,
  );
  expect(none.container.querySelector(".thinking-tokens")).toBeNull();
});

test("reasoning the provider kept to itself is a row with its cost and a note", async () => {
  const { container } = render(
    <ThinkingMessage status="completed" content="" reasoningTokens={640} />,
  );
  expect(screen.getByText("thinking")).toBeTruthy();
  expect(container.querySelector(".thinking-tokens")?.textContent).toBe(
    "640 tokens",
  );
  const details = container.querySelector("details")!;
  details.open = true;
  details.dispatchEvent(new Event("toggle"));
  expect(
    await screen.findByText("The provider did not return the reasoning text."),
  ).toBeInTheDocument();
});

// The Russian label stays a first-person verb like the tool rows (#239, held
// by i18n.test.ts); the count is declined by number.
test("the Russian row declines its tokens by number", () => {
  setLocale("ru");
  const { container } = render(
    <ThinkingMessage
      status="completed"
      content="x"
      durationMs={1200}
      reasoningTokens={3}
    />,
  );
  expect(screen.getByText("размышляю")).toBeTruthy();
  expect(container.querySelector(".thinking-tokens")?.textContent).toBe(
    "3 токена",
  );
});
