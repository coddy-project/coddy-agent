import { expect, test } from "vitest";
import {
  isNewerSettings,
  parseSessionSettings,
  sessionSettingsEventOf,
} from "./sessionSettings";
import { dispatchServerEvent, parseServerEvent } from "./serverEvents";

const snapshot = {
  sessionId: "sess_a",
  version: 7,
  model: "nd/qwen3.8-27b",
  reasoning: "off",
  reasoningChoices: ["low", "medium", "high", "off"],
  mode: "plan",
  permissionMode: "bypass",
  configuredPermissionMode: "ask",
  overrides: [{ setting: "model", value: "nd/gpt-oss-120b", turnsLeft: 2, active: true }],
};

test("a snapshot is read whole, overrides included", () => {
  const snap = parseSessionSettings(snapshot);
  expect(snap).toEqual({ ...snapshot, overrides: [{ ...snapshot.overrides[0] }] });
  expect(parseSessionSettings({ version: 3 })).toBeNull();
  expect(parseSessionSettings(null)).toBeNull();
});

test("both frame shapes carry the snapshot: the events stream and the turn stream", () => {
  const fromEvents = sessionSettingsEventOf(
    JSON.stringify({
      object: "coddy.session_settings",
      sessionId: "sess_a",
      settings: snapshot,
      notice: "Permission mode: bypass for this session",
      source: "permission_dialog",
    }),
  );
  expect(fromEvents?.sessionId).toBe("sess_a");
  expect(fromEvents?.settings.permissionMode).toBe("bypass");
  expect(fromEvents?.notice).toContain("bypass");
  // The turn stream carries the ACP update: the session id sits in the snapshot.
  const fromTurn = sessionSettingsEventOf(
    JSON.stringify({ sessionUpdate: "session_settings", settings: snapshot }),
  );
  expect(fromTurn?.sessionId).toBe("sess_a");
  expect(sessionSettingsEventOf("not json")).toBeNull();
});

test("only a newer snapshot of the viewed session replaces what a tab shows", () => {
  const snap = parseSessionSettings(snapshot)!;
  expect(isNewerSettings(6, "sess_a", snap)).toBe(true);
  expect(isNewerSettings(7, "sess_a", snap)).toBe(false);
  expect(isNewerSettings(9, "sess_a", snap)).toBe(false);
  expect(isNewerSettings(0, "sess_b", snap)).toBe(false);
  expect(isNewerSettings(0, "", snap)).toBe(false);
});

test("the events stream parses and dispatches session_settings", () => {
  const event = parseServerEvent({
    event: "session_settings",
    data: JSON.stringify({ sessionId: "sess_a", settings: snapshot, notice: "", source: "web" }),
  });
  expect(event?.type).toBe("session_settings");
  const seen: string[] = [];
  dispatchServerEvent(
    {
      onTurnStarted: () => {},
      onTurnEnded: () => {},
      onSessionSettings: (e) => seen.push(`${e.sessionId}:${e.settings.version}`),
    },
    event!,
  );
  expect(seen).toEqual(["sess_a:7"]);
});
