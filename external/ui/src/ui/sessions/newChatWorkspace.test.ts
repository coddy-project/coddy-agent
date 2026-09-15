import { describe, expect, it } from "vitest";
import { newChatWorkspaceIsReady } from "./newChatWorkspace";

describe("newChatWorkspaceIsReady", () => {
  const pending = { path: "/srv/one", nonce: 1 };

  it("waits while a conversation is still current", () => {
    // Applying here would move the folder of the session being left, and the
    // new chat would start in the default workspace - both wrong.
    expect(newChatWorkspaceIsReady(pending, "sess_abc")).toBe(false);
  });

  it("applies once no session is current", () => {
    expect(newChatWorkspaceIsReady(pending, "")).toBe(true);
    expect(newChatWorkspaceIsReady(pending, "   ")).toBe(true);
  });

  it("has nothing to apply without a pick", () => {
    expect(newChatWorkspaceIsReady(null, "")).toBe(false);
    expect(newChatWorkspaceIsReady({ path: "  ", nonce: 1 }, "")).toBe(false);
  });
});
