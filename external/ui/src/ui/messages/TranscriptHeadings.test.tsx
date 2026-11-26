import React from "react";
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, test } from "vitest";
import { I18nProvider } from "../i18n/I18nProvider";
import { setLocale } from "../i18n/i18n";
import { AssistantMessage } from "./AssistantMessage";
import { UserMessage } from "./UserMessage";

afterEach(() => {
  cleanup();
  setLocale("en");
});

function headingBefore(name: string, content: HTMLElement) {
  const heading = screen.getByRole("heading", { level: 2, name });
  expect(heading.tagName).toBe("H2");
  expect(heading).toHaveClass("sr-only");
  expect(heading.compareDocumentPosition(content)).toBe(
    Node.DOCUMENT_POSITION_FOLLOWING,
  );
  return heading;
}

test.each([
  ["en", "Your message", "Agent reply"],
  ["ru", "Ваше сообщение", "Ответ агента"],
])(
  "prompt and reply headings precede their content in %s",
  (locale, prompt, reply) => {
    setLocale(locale);
    render(
      <I18nProvider>
        <UserMessage content="Explain the result" />
        <AssistantMessage content="Here is the result." />
      </I18nProvider>,
    );

    const promptHeading = headingBefore(
      prompt,
      screen.getByTestId("user-message-body"),
    );
    const replyHeading = headingBefore(
      reply,
      screen.getByText("Here is the result."),
    );
    expect(screen.getAllByRole("heading")).toEqual([
      promptHeading,
      replyHeading,
    ]);
  },
);

test("attachment-only messages start with their respective headings", () => {
  render(
    <>
      <UserMessage
        content=""
        files={[{ name: "input.txt", mimeType: "text/plain" }]}
      />
      <AssistantMessage
        content=""
        files={[
          {
            name: "result.png",
            mimeType: "image/png",
            url: "/coddy/sessions/s/assets/result.png",
          },
        ]}
      />
    </>,
  );

  expect(screen.queryByTestId("user-message-body")).not.toBeInTheDocument();
  headingBefore("Your message", screen.getByText("input.txt"));
  headingBefore(
    "Agent reply",
    screen.getByRole("button", { name: "Open generated image result.png" }),
  );
});

test("reply heading keeps its identity as Markdown streams and completes", () => {
  const { rerender } = render(
    <AssistantMessage content="A partial reply" streaming />,
  );
  const heading = headingBefore(
    "Agent reply",
    screen.getByText("A partial reply"),
  );
  const content =
    "A partial reply, now with headings.\n\n# Title\n\n## Section\n\n### Detail\n\n#### Item\n\n##### Note\n\n###### Example";

  for (const streaming of [true, false]) {
    rerender(<AssistantMessage content={content} streaming={streaming} />);
    expect(
      headingBefore(
        "Agent reply",
        screen.getByText("A partial reply, now with headings."),
      ),
    ).toBe(heading);
    expect(
      screen.getAllByRole("heading").map((el) => [el.tagName, el.textContent]),
    ).toEqual([
      ["H2", "Agent reply"],
      ["H1", "Title"],
      ["H2", "Section"],
      ["H3", "Detail"],
      ["H4", "Item"],
      ["H5", "Note"],
      ["H6", "Example"],
    ]);
  }
});
