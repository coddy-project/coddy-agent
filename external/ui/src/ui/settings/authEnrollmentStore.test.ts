import { afterEach, expect, test, vi } from "vitest";

const env = vi.hoisted(() => ({ current: "a", listener: () => {} }));
vi.mock("../env/remoteEnv", () => ({
  getEnv: () => ({ mode: "remote", baseUrl: env.current, token: "" }),
  environmentKey: (value: { baseUrl: string }) => value.baseUrl,
  onEnvironmentSwitch: (listener: () => void) => {
    env.listener = listener;
    return () => {};
  },
}));
import {
  enrollmentSnapshot,
  resetEnrollment,
  verifyEnrollment,
} from "./authEnrollmentStore";

function select(name: string) {
  env.current = name;
  env.listener();
}
function deferred() {
  let resolve!: (value: {
    ok: boolean;
    json: () => Promise<{ codes: string[] }>;
  }) => void;
  const promise = new Promise<{
    ok: boolean;
    json: () => Promise<{ codes: string[] }>;
  }>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}
afterEach(() => {
  resetEnrollment();
  vi.unstubAllGlobals();
  env.current = "a";
});

test("retains a deferred verification in its originating environment", async () => {
  const pending = deferred();
  vi.stubGlobal(
    "fetch",
    vi.fn(() => pending.promise),
  );
  const verification = verifyEnrollment("SECRET", "123456");
  select("b");
  expect(enrollmentSnapshot().busy).toBe(false);
  pending.resolve({ ok: true, json: async () => ({ codes: ["A-CODE"] }) });
  await verification;
  expect(enrollmentSnapshot().codes).toEqual([]);
  select("a");
  expect(enrollmentSnapshot().codes).toEqual(["A-CODE"]);
});

test("restores completed codes after switching away and back", async () => {
  const pending = deferred();
  vi.stubGlobal(
    "fetch",
    vi.fn(() => pending.promise),
  );
  const verification = verifyEnrollment("SECRET", "123456");
  pending.resolve({ ok: true, json: async () => ({ codes: ["SAVED-CODE"] }) });
  await verification;
  select("b");
  expect(enrollmentSnapshot().codes).toEqual([]);
  select("a");
  expect(enrollmentSnapshot().codes).toEqual(["SAVED-CODE"]);
});
