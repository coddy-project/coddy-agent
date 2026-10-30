import { expect, test } from "vitest";
import { queuedUserMessageItem } from "./queuedUserMessage";

const frame = (text: string) =>
  JSON.stringify({
    sessionUpdate: "user_message_chunk",
    content: { type: "text", text },
  });

test("a queued message read with an image shows the image as a file chip", () => {
  const item = queuedUserMessageItem(
    frame(
      "inspect this\n\n<coddy_session_assets>Uploaded files saved to session assets (read-only). You can read or copy them:\n- /home/u/.coddy/sessions/s/assets/shot.png\n</coddy_session_assets>",
    ),
    "u1",
    "2026-09-26T12:00:00Z",
  );
  expect(item?.content).toContain("inspect this");
  expect(item?.files?.map((f) => f.name)).toEqual(["shot.png"]);
});

test("a message of an image alone still gets a row", () => {
  const item = queuedUserMessageItem(
    frame(
      "\n\n<coddy_session_assets>Uploaded files:\n- /tmp/assets/only.png\n</coddy_session_assets>",
    ),
    "u2",
    "2026-09-26T12:00:00Z",
  );
  expect(item?.files?.map((f) => f.name)).toEqual(["only.png"]);
});

test("a plain message has no chips, and an empty or broken frame no row", () => {
  expect(
    queuedUserMessageItem(frame("check the path"), "u3", "t")?.files,
  ).toBeUndefined();
  expect(queuedUserMessageItem(frame("   "), "u4", "t")).toBeNull();
  expect(queuedUserMessageItem("{not json", "u5", "t")).toBeNull();
});

test("a picture read with a known session opens from the session's assets at once", () => {
  const item = queuedUserMessageItem(
    frame(
      "look\n\n<coddy_session_assets>Uploaded files:\n- /tmp/assets/shot 1.png\n- /tmp/assets/shot_1.png (shot.png)\n</coddy_session_assets>",
    ),
    "u6",
    "t",
    "sess_a",
  );
  expect(item?.files).toEqual([
    {
      name: "shot 1.png",
      mimeType: "application/octet-stream",
      url: "/coddy/sessions/sess_a/assets/shot%201.png",
    },
    {
      name: "shot.png",
      mimeType: "application/octet-stream",
      url: "/coddy/sessions/sess_a/assets/shot_1.png",
    },
  ]);
});
