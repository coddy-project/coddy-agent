import React from "react";
import { afterEach } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import { expect, test } from "vitest";
import { ThinkingMessage } from "./ThinkingMessage";

afterEach(() => cleanup());

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
  const { container } = render(<ThinkingMessage status="in_progress" content="x" />);
  expect(container.querySelector(".thinking-dur")?.textContent).toBe("0ms");
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
