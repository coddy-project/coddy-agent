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

test.each([
  ["en", "Your message", "Agent reply"],
  ["ru", "Ваше сообщение", "Ответ агента"],
])(
  "messages keep named articles and preserve Markdown heading outline in %s",
  (locale, prompt, reply) => {
    setLocale(locale);
    render(
      <I18nProvider>
        <UserMessage content="Explain the result" />
        <AssistantMessage
          content={"Here is the result.\n\n# Result\n\n## Details"}
        />
      </I18nProvider>,
    );

    expect(screen.getByRole("article", { name: prompt })).toHaveTextContent(
      "Explain the result",
    );
    expect(screen.getByRole("article", { name: reply })).toHaveTextContent(
      "Here is the result.",
    );
    expect(
      screen
        .getAllByRole("heading")
        .map((heading) => [heading.tagName, heading.textContent]),
    ).toEqual([
      ["H1", "Result"],
      ["H2", "Details"],
    ]);
  },
);

test("attachment-only messages retain their accessible article identities", () => {
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
  expect(
    screen.getByRole("article", { name: "Your message" }),
  ).toHaveTextContent("input.txt");
  expect(screen.getByRole("article", { name: "Agent reply" })).toContainElement(
    screen.getByRole("button", { name: "Open generated image result.png" }),
  );
  expect(screen.queryAllByRole("heading")).toHaveLength(0);
});

test("reply article keeps its identity as Markdown streams and completes", () => {
  const { rerender } = render(
    <AssistantMessage content="A partial reply" streaming />,
  );
  const article = screen.getByRole("article", { name: "Agent reply" });
  expect(article).toHaveTextContent("A partial reply");
  const content =
    "A partial reply, now with headings.\n\n# Title\n\n## Section\n\n### Detail\n\n#### Item\n\n##### Note\n\n###### Example";

  for (const streaming of [true, false]) {
    rerender(<AssistantMessage content={content} streaming={streaming} />);
    expect(screen.getByRole("article", { name: "Agent reply" })).toBe(article);
    expect(
      screen.getAllByRole("heading").map((el) => [el.tagName, el.textContent]),
    ).toEqual([
      ["H1", "Title"],
      ["H2", "Section"],
      ["H3", "Detail"],
      ["H4", "Item"],
      ["H5", "Note"],
      ["H6", "Example"],
    ]);
  }
});
