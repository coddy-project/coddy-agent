import { environmentKey, getEnv, onEnvironmentSwitch } from "../env/remoteEnv";

type State = { busy: boolean; codes: string[]; error: boolean };
const empty: State = { busy: false, codes: [], error: false };
const states = new Map<string, State>();
const activeKey = () => environmentKey(getEnv());
let generation = 0;
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
export function resetEnrollment() {
  generation++;
  states.clear();
  notify();
}
onEnvironmentSwitch(notify);
export async function verifyEnrollment(
  secret: string,
  code: string,
): Promise<void> {
  const key = activeKey();
  const state = states.get(key) ?? empty;
  if (state.busy) return;
  const current = generation;
  setState(key, { ...state, busy: true, error: false });
  try {
    const response = await fetch("/coddy/auth/totp/enable", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ secret, code }),
    });
    if (!response.ok) throw new Error("verify");
    const result = (await response.json()) as { codes: string[] };
    if (current === generation)
      setState(key, { busy: false, error: false, codes: result.codes });
  } catch {
    if (current === generation)
      setState(key, { ...state, busy: false, error: true });
  }
}
