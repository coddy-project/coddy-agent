import { afterEach, describe, expect, test, vi } from "vitest";
import {
  act,
  cleanup,
  fireEvent,
  render,
  screen,
} from "@testing-library/react";
import { TypingDotsMessage } from "./TypingDotsMessage";
import { SystemNoticeMessage } from "./SystemNoticeMessage";
import { I18nProvider } from "../i18n/I18nProvider";
import { initLocale, setLocale } from "../i18n/i18n";
import { phoneMaxWidthMediaQuery } from "../shellBreakpoint";
import type { UiLocale } from "../i18n/locales";

function viewport(initialWidth: number) {
  let width = initialWidth;
  const listeners = new Set<(event: MediaQueryListEvent) => void>();
  vi.spyOn(window, "matchMedia").mockImplementation(
    (query) =>
      ({
        get matches() {
          return query === phoneMaxWidthMediaQuery && width <= 599;
        },
        media: query,
        onchange: null,
        addListener: vi.fn(),
        removeListener: vi.fn(),
        dispatchEvent: vi.fn(),
        addEventListener: (
          _type: string,
          listener: (event: MediaQueryListEvent) => void,
        ) => {
          listeners.add(listener);
        },
        removeEventListener: (
          _type: string,
          listener: (event: MediaQueryListEvent) => void,
        ) => {
          listeners.delete(listener);
        },
      }) as MediaQueryList,
  );
  return (nextWidth: number) => {
    width = nextWidth;
    act(() =>
      listeners.forEach((listener) =>
        listener(new Event("change") as MediaQueryListEvent),
      ),
    );
  };
}

afterEach(() => {
  cleanup();
  initLocale("en");
  vi.restoreAllMocks();
});

const states = ["live", "tasks-only", "finished"] as const;

function line(
  state: (typeof states)[number],
  count: number,
  onOpen?: () => void,
) {
  if (state === "finished") {
    return (
      <SystemNoticeMessage
        level="notice"
        message="Generation complete"
        generation={{
          elapsedMs: 645_000,
          outputTokens: 13_200,
          tokensPerSecond: 46,
        }}
        runningTasks={count}
        {...(onOpen ? { onOpenTasks: onOpen } : {})}
      />
    );
  }
  return (
    <TypingDotsMessage
      tasksOnly={state === "tasks-only"}
      statusKind="tool"
      statusKey="status.run"
      turnStartedAtMs={Date.now() - 645_000}
      turnTokens={13_200}
      tokensPerSecond={46}
      runningTasks={count}
      {...(onOpen ? { onOpenTasks: onOpen } : {})}
    />
  );
}

describe("phone task counts", () => {
  test.each(states)(
    "%s uses a compact count and keeps the panel action",
    (state) => {
      viewport(390);
      initLocale("ru");
      const onOpen = vi.fn();
      render(line(state, 1, onOpen));
      const button = screen.getByTestId("typing-dots-turn-tasks");
      expect(button.textContent).toBe("1 задача");
      expect(button).toHaveAccessibleName(
        "Открыть фоновые задачи: Выполняется 1 задача",
      );
      fireEvent.click(button);
      expect(onOpen).toHaveBeenCalledTimes(1);
    },
  );

  const counts: [UiLocale, number, string][] = [
    ["ru", 1, "1 задача"],
    ["ru", 2, "2 задачи"],
    ["ru", 5, "5 задач"],
    ["ru", 11, "11 задач"],
    ["ru", 21, "21 задача"],
    ["ru", 22, "22 задачи"],
    ["en", 1, "1 task"],
    ["en", 2, "2 tasks"],
  ];
  test.each(counts)(
    "%s declines %i as %s without a panel action",
    (locale, count, expected) => {
      viewport(390);
      initLocale(locale);
      render(line("tasks-only", count));
      const segment = screen.getByTestId("typing-dots-turn-tasks");
      expect(segment.tagName).toBe("SPAN");
      expect(segment.textContent).toBe(expected);
    },
  );

  test.each([
    [599, "1 задача"],
    [600, "Выполняется 1 задача"],
    [834, "Выполняется 1 задача"],
    [1280, "Выполняется 1 задача"],
  ])("at %i px the label is %s", (width, expected) => {
    viewport(width);
    initLocale("ru");
    render(line("live", 1));
    expect(screen.getByTestId("typing-dots-turn-tasks").textContent).toBe(
      expected,
    );
  });

  test("follows the phone breakpoint and locale while mounted", () => {
    const resize = viewport(600);
    render(<I18nProvider>{line("live", 1)}</I18nProvider>);
    const segment = screen.getByTestId("typing-dots-turn-tasks");
    expect(segment.textContent).toBe("1 running task");
    resize(599);
    expect(segment.textContent).toBe("1 task");
    act(() => {
      setLocale("ru");
    });
    expect(segment.textContent).toBe("1 задача");
    resize(600);
    expect(segment.textContent).toBe("Выполняется 1 задача");
  });
});
