import { afterEach, expect, test, vi } from "vitest";
import { setEnv } from "../env/remoteEnv";
import { setAuthState, snapshotAuth } from "../auth/authState";
import {
  enrollmentSnapshot,
  resetEnrollment,
  verifyEnrollment,
} from "./authEnrollmentStore";

function request() {
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
  vi.stubGlobal(
    "fetch",
    vi.fn(() => promise),
  );
  return { resolve, promise };
}
afterEach(() => {
  resetEnrollment();
  vi.unstubAllGlobals();
  setEnv({ mode: "local" });
});

test("sign-out clears codes and invalidates pending verification", async () => {
  setEnv({ mode: "local" });
  setAuthState({
    ...snapshotAuth(),
    loaded: true,
    authenticated: true,
    user: "operator",
  });
  const pending = request();
  const work = verifyEnrollment("SECRET", "123456");
  setAuthState({ ...snapshotAuth(), authenticated: false, user: "" });
  pending.resolve({ ok: true, json: async () => ({ codes: ["PRIVATE"] }) });
  await work;
  expect(enrollmentSnapshot().codes).toEqual([]);
});

test("a same-server token change invalidates the previous pending result", async () => {
  setEnv({ mode: "remote", baseUrl: "https://test.invalid", token: "old" });
  const pending = request();
  const work = verifyEnrollment("SECRET", "123456");
  setEnv({ mode: "remote", baseUrl: "https://test.invalid", token: "new" });
  pending.resolve({ ok: true, json: async () => ({ codes: ["PRIVATE"] }) });
  await work;
  expect(enrollmentSnapshot().codes).toEqual([]);
});

test("sign-out clears a completed result", async () => {
  setEnv({ mode: "local" });
  setAuthState({
    ...snapshotAuth(),
    loaded: true,
    authenticated: true,
    user: "operator",
  });
  const pending = request();
  const work = verifyEnrollment("SECRET", "123456");
  pending.resolve({ ok: true, json: async () => ({ codes: ["PRIVATE"] }) });
  await work;
  setAuthState({ ...snapshotAuth(), authenticated: false, user: "" });
  expect(enrollmentSnapshot().codes).toEqual([]);
});

test("a same-server token change clears a completed result", async () => {
  setEnv({ mode: "remote", baseUrl: "https://test.invalid", token: "old" });
  const pending = request();
  const work = verifyEnrollment("SECRET", "123456");
  pending.resolve({ ok: true, json: async () => ({ codes: ["PRIVATE"] }) });
  await work;
  setEnv({ mode: "remote", baseUrl: "https://test.invalid", token: "new" });
  expect(enrollmentSnapshot().codes).toEqual([]);
});
