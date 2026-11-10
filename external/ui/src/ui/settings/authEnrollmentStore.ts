import { onEnvironmentSwitch } from "../env/remoteEnv";

type State = { busy: boolean; codes: string[]; error: boolean };
let state: State = { busy: false, codes: [], error: false };
let generation = 0;
const listeners = new Set<() => void>();
export const enrollmentSnapshot = () => state;
export const subscribeEnrollment = (listener: () => void) => {
  listeners.add(listener);
  return () => {
    listeners.delete(listener);
  };
};
function setState(next: State) {
  state = next;
  listeners.forEach((listener) => listener());
}
export function resetEnrollment() {
  generation++;
  setState({ busy: false, codes: [], error: false });
}
onEnvironmentSwitch(resetEnrollment);
export async function verifyEnrollment(
  secret: string,
  code: string,
): Promise<void> {
  if (state.busy) return;
  const current = generation;
  setState({ ...state, busy: true, error: false });
  try {
    const response = await fetch("/coddy/auth/totp/enable", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ secret, code }),
    });
    if (!response.ok) throw new Error("verify");
    const result = (await response.json()) as { codes: string[] };
    if (current === generation)
      setState({ busy: false, error: false, codes: result.codes });
  } catch {
    if (current === generation)
      setState({ ...state, busy: false, error: true });
  }
}
