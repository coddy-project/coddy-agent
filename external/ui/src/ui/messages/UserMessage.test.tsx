import React from "react";
import { afterEach } from "vitest";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { expect, test, vi } from "vitest";
import { UserMessage } from "./UserMessage";

afterEach(() => cleanup());

test("user bubble preserves multiline text without markdown pipeline", () => {
  const yaml = [
    "---",
    "services:",
    "  qbittorrent:",
    "    volumes:",
    "      - /path/to/downloads:/downloads",
  ].join("\n");
  render(<UserMessage content={yaml} />);
  const body = screen.getByTestId("user-message-body");
  expect(body).toHaveTextContent("services:");
  expect(body).toHaveTextContent("/path/to/downloads:/downloads");
  expect(screen.queryByTestId("coddy-skill-span")).toBeNull();
});

test("user bubble does not treat path slashes as skill chips without knownSkillNames", () => {
  render(<UserMessage content="hi /demo there" />);
  expect(screen.getByTestId("user-message-body")).toHaveTextContent(
    "hi /demo there",
  );
  expect(screen.queryByTestId("coddy-skill-span")).toBeNull();
});

test("user bubble renders known skill as chip when knownSkillNames provided", () => {
  const known = new Set(["rpa-gen-rules"]);
  render(<UserMessage content="please /rpa-gen-rules for me" knownSkillNames={known} />);
  const chip = screen.getByTestId("coddy-skill-span");
  expect(chip).toHaveTextContent("/rpa-gen-rules");
  expect(chip).toHaveAttribute("data-skill-name", "rpa-gen-rules");
});

test("user bubble does not chip /name absent from knownSkillNames", () => {
  const known = new Set(["rpa-gen-rules"]);
  render(<UserMessage content="see /unknown-cmd here" knownSkillNames={known} />);
  expect(screen.queryByTestId("coddy-skill-span")).toBeNull();
  expect(screen.getByTestId("user-message-body")).toHaveTextContent(
    "see /unknown-cmd here",
  );
});

test("copy sends raw user text not display-only slash chip source", () => {
  const writeText = vi.fn().mockResolvedValue(undefined);
  Object.defineProperty(globalThis.navigator, "clipboard", {
    value: { writeText },
    configurable: true,
    writable: true,
  });
  render(<UserMessage content="hi /demo there" />);
  const copyBtn = screen.getByTestId("user-message-copy");
  expect(copyBtn).toHaveAttribute("title", "Copy message");
  copyBtn.click();
  expect(writeText).toHaveBeenCalledWith("hi /demo there");
});

test("edit button is absent when onEdit is not provided", () => {
  render(<UserMessage content="hello" />);
  expect(screen.queryByTestId("user-message-edit")).toBeNull();
});

test("edit button is visible when onEdit is provided", () => {
  render(<UserMessage content="hello" onEdit={vi.fn()} />);
  expect(screen.getByTestId("user-message-edit")).toBeInTheDocument();
});

test("edit button calls onEdit with message content and index", () => {
  const onEdit = vi.fn();
  render(<UserMessage content="edit me" onEdit={onEdit} userMsgIndex={2} />);
  screen.getByTestId("user-message-edit").click();
  expect(onEdit).toHaveBeenCalledWith("edit me", 2);
});

test("persisted hydrated attachments render as compact @ paths", () => {
  const blob =
    "read this\n\n" +
    '<coddy_attachment path="note.txt" name="note.txt">\n' +
    "<![CDATA[secret body]]>\n" +
    "</coddy_attachment>";
  render(<UserMessage content={blob} />);
  expect(screen.getByText(/read this/)).toBeInTheDocument();
  expect(screen.getByText(/@note\.txt/)).toBeInTheDocument();
  expect(screen.queryByText(/secret body/)).toBeNull();
});

test("image files with previewUrl render a thumbnail chip; others keep the icon", () => {
  render(
    <UserMessage
      content="look at this"
      files={[
        {
          name: "pasted-1.png",
          mimeType: "image/png",
          sizeBytes: 1024,
          previewUrl: "blob:coddy-user-thumb-1",
        },
        { name: "notes.txt", mimeType: "text/plain", sizeBytes: 4 },
      ]}
    />,
  );
  const thumbs = screen.getAllByTestId("msg-user-file-thumb");
  expect(thumbs).toHaveLength(1);
  expect(thumbs[0]).toHaveAttribute("src", "blob:coddy-user-thumb-1");
  expect(thumbs[0]!.closest(".msg-user-file-chip")).toHaveClass(
    "msg-user-file-chip--image",
  );
  expect(screen.getByText("notes.txt")).toBeInTheDocument();
  // Metadata-only entry (e.g. after reload) renders no thumbnail element.
  expect(
    screen.getByText("notes.txt").closest(".msg-user-file-chip"),
  ).not.toHaveClass("msg-user-file-chip--image");
});

// The sent bubble shows the picture large enough to recognise, and a click
// opens the original rather than the bounded thumbnail beside it.
test("an image in the sent bubble opens the full-size asset enlarged", () => {
  render(
    <UserMessage
      content="look at this"
      files={[
        {
          name: "pasted-1.png",
          mimeType: "image/png",
          sizeBytes: 1024,
          previewUrl: "/coddy/sessions/s1/assets/pasted-1.png/thumbnail",
          url: "/coddy/sessions/s1/assets/pasted-1.png",
        },
      ]}
    />,
  );
  expect(
    screen.getByTestId("msg-user-file-thumb").closest(".msg-user-file-chip"),
  ).toHaveClass("msg-user-file-card");

  fireEvent.click(screen.getByLabelText("Open pasted-1.png enlarged"));
  const shown = document.querySelector(
    ".docs-lightbox-stage img",
  ) as HTMLImageElement | null;
  expect(shown?.getAttribute("src")).toBe("/coddy/sessions/s1/assets/pasted-1.png");

  fireEvent.click(screen.getByTestId("docs-lightbox-close"));
  expect(document.querySelector(".docs-lightbox")).toBeNull();
});

// A message sent before the full-size route existed carries only the preview.
// Opening that is worth more than losing the click.
test("a bubble that predates the full-size url falls back to the preview", () => {
  render(
    <UserMessage
      content="older"
      files={[
        {
          name: "old.png",
          mimeType: "image/png",
          previewUrl: "blob:coddy-user-thumb-2",
        },
      ]}
    />,
  );
  fireEvent.click(screen.getByLabelText("Open old.png enlarged"));
  const shown = document.querySelector(
    ".docs-lightbox-stage img",
  ) as HTMLImageElement | null;
  expect(shown?.getAttribute("src")).toBe("blob:coddy-user-thumb-2");
});

// A page of the documentation the user mentioned is a link in the sent
// bubble: it opens the reader at that page and section.
test("an @coddy: mention in the sent message opens the documentation reader", () => {
  render(
    <UserMessage
      content="@coddy:operate/swarm как настроить рой? and @coddy:features/mentions#completion, not user@example.com"
      knownSkillNames={new Set(["demo"])}
    />,
  );
  const links = Array.from(document.querySelectorAll("a.coddy-doc-mention"));
  expect(links.map((a) => [a.textContent, a.getAttribute("href")])).toEqual([
    ["@coddy:operate/swarm", "#/docs/operate/swarm"],
    ["@coddy:features/mentions#completion", "#/docs/features/mentions#completion"],
  ]);
  expect(screen.getByTestId("user-message-body").textContent).toContain("как настроить рой?");
});
