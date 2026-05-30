import { expect, test } from "vitest";

import {
  commandAllowed,
  shouldShowRestoredPermissionPrompt,
} from "./toolsPermissionPolicy";

const policy = {
  requirePermissionForCommands: true,
  requirePermissionForWrites: false,
  permissionMasterKey: false,
  commandAllowlist: ["go test"],
};

test("allowlisted run_command does not restore permission prompt", () => {
  expect(
    shouldShowRestoredPermissionPrompt(policy, {
      title: "run_command",
      argsText: '{"command":"go test ./..."}',
    }),
  ).toBe(false);
});

test("non-allowlisted run_command restores permission prompt when enabled", () => {
  expect(
    shouldShowRestoredPermissionPrompt(policy, {
      title: "run_command",
      argsText: '{"command":"rm -rf /"}',
    }),
  ).toBe(true);
});

test("permission off skips restore", () => {
  expect(
    shouldShowRestoredPermissionPrompt(
      { ...policy, requirePermissionForCommands: false },
      { title: "run_command", argsText: '{"command":"rm -rf /"}' },
    ),
  ).toBe(false);
});

test("commandAllowed prefix match", () => {
  expect(commandAllowed(["go test"], "go test ./pkg")).toBe(true);
  expect(commandAllowed(["go test"], "go build")).toBe(false);
});
