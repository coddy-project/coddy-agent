import {
  environmentKey,
  getEnv,
  onEnvironmentSwitch,
  subscribeEnv,
} from "../env/remoteEnv";
import { snapshotAuth, subscribeAuth } from "../auth/authState";

type State = {
  busy: boolean;
  codes: string[];
  error: false | "verify" | "disable" | "regenerate";
  configured: boolean | null;
  backupGeneration: string;
  pending: boolean;
};
const empty: State = {
  busy: false,
  codes: [],
  error: false,
  configured: null,
  backupGeneration: "",
  pending: false,
};
const states = new Map<string, State>();
const credentials = new Map<string, string>();
const epochs = new Map<string, number>();
const activeKey = () => environmentKey(getEnv());
const listeners = new Set<() => void>();
export const enrollmentSnapshot = () => states.get(activeKey()) ?? empty;
export const subscribeEnrollment = (listener: () => void) => {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
};
function notify() {
  listeners.forEach((listener) => listener());
}
function setState(key: string, next: State) {
  states.set(key, next);
  if (key === activeKey()) notify();
}
function invalidate(key: string) {
  epochs.set(key, (epochs.get(key) ?? 0) + 1);
  states.delete(key);
  if (key === activeKey()) notify();
}
export function resetEnrollment() {
  for (const key of states.keys()) invalidate(key);
  credentials.clear();
}
function observeEnvironment() {
  const env = getEnv();
  const key = environmentKey(env);
  if (env.mode === "remote") {
    const previous = credentials.get(key);
    if (previous !== undefined && previous !== env.token) invalidate(key);
    credentials.set(key, env.token);
  }
  notify();
}
onEnvironmentSwitch(observeEnvironment);
subscribeEnv(observeEnvironment);
let auth = snapshotAuth();
subscribeAuth(() => {
  const next = snapshotAuth();
  if (auth.authenticated && (!next.authenticated || auth.user !== next.user))
    invalidate("local");
  auth = next;
});
export function discardEnrollment() {
  invalidate(activeKey());
}
export function acknowledgeEnrollment() {
  const key = activeKey();
  const state = states.get(key);
  if (state?.pending) setState(key, { ...state, pending: false });
}
export function reconcileEnrollment(
  configured: boolean,
  count: number,
  backupGeneration: string,
) {
  const key = activeKey();
  const state = states.get(key);
  if (!state || state.busy || state.pending || state.configured === null)
    return;
  if (
    state.configured !== configured ||
    state.codes.length !== count ||
    state.backupGeneration !== backupGeneration
  )
    invalidate(key);
}
async function perform(
  path: string,
  body: object | undefined,
  operation: "verify" | "disable" | "regenerate",
) {
  const key = activeKey();
  const env = getEnv();
  if (env.mode === "remote") {
    const previous = credentials.get(key);
    if (previous !== undefined && previous !== env.token) invalidate(key);
    credentials.set(key, env.token);
  }
  const state = states.get(key) ?? empty;
  if (state.busy) return;
  const epoch = epochs.get(key) ?? 0;
  setState(key, { ...state, busy: true, error: false });
  try {
    const response = await fetch(
      path,
      body
        ? {
            method: "POST",
            headers: { "Content-Type": "application/json" },
            body: JSON.stringify(body),
          }
        : { method: "POST" },
    );
    if (!response.ok) throw new Error("enrollment change");
    const result =
      operation === "disable"
        ? { codes: [], backup_codes_generation: "" }
        : ((await response.json()) as {
            codes: string[];
            backup_codes_generation?: string;
          });
    if ((epochs.get(key) ?? 0) === epoch)
      setState(key, {
        busy: false,
        error: false,
        codes: result.codes,
        configured: operation !== "disable",
        backupGeneration: result.backup_codes_generation ?? "",
        pending: true,
      });
  } catch {
    if ((epochs.get(key) ?? 0) === epoch)
      setState(key, { ...state, busy: false, error: operation });
  }
}
export function verifyEnrollment(secret: string, code: string): Promise<void> {
  return perform("/coddy/auth/totp/enable", { secret, code }, "verify");
}
export function changeEnrollment(
  operation: "disable" | "regenerate",
): Promise<void> {
  return perform(
    operation === "disable"
      ? "/coddy/auth/totp/disable"
      : "/coddy/auth/backup-codes/regenerate",
    undefined,
    operation,
  );
}
