import { expect, test, vi } from "vitest";
import { connectRemote, getEnv, setEnv } from "./remoteEnv";
import { snapshotHealth, startActiveHealthMonitor } from "./activeHealth";

// The monitor asks the active remote again every 30 seconds and on every focus
// of the window. A remote that is down used to go "checking" for each of those
// asks, which took the banner away (it shows only while down) and, on the
// stacked shell, moved the whole page up and back down again. It stays down,
// and says why, until an answer says otherwise.

let hang = false;
// Requests held open by the test, answered in the order it chooses.
let held: Array<(r: Response | Error) => void> = [];
let holding = false;

vi.mock("./remoteEnv", async (importOriginal) => {
  const actual = await importOriginal<typeof import("./remoteEnv")>();
  return {
    ...actual,
    localFetch: () => {
      if (holding) {
        return new Promise<Response>((resolve, reject) => {
          held.push((r) => (r instanceof Error ? reject(r) : resolve(r)));
        });
      }
      return hang
        ? new Promise<Response>(() => {})
        : Promise.reject(new TypeError("Failed to fetch"));
    },
  };
});

test("a remote that is down stays down while it is asked again", async () => {
  setEnv({ mode: "remote", baseUrl: "http://dead.lan:12345", token: "" });
  startActiveHealthMonitor();
  await vi.waitFor(() => expect(snapshotHealth()).toBe("down"));
  hang = true;
  window.dispatchEvent(new Event("focus"));
  await Promise.resolve();
  expect(snapshotHealth()).toBe("down");
});

// Two asks of one remote can be in flight: a timeout of the older one landing
// after the newer one said "up" must not put the remote back down.
test("the newest ask decides, not the one that answers last", async () => {
  hang = false;
  holding = true;
  held = [];
  window.dispatchEvent(new Event("focus"));
  await vi.waitFor(() => expect(held.length).toBe(1));
  window.dispatchEvent(new Event("focus"));
  await vi.waitFor(() => expect(held.length).toBe(2));
  held[1]!(new Response(JSON.stringify({ data: [] }), { status: 200 }));
  await vi.waitFor(() => expect(snapshotHealth()).toBe("up"));
  held[0]!(
    Object.assign(new Error("signal timed out"), { name: "TimeoutError" }),
  );
  await new Promise((r) => setTimeout(r, 0));
  expect(snapshotHealth()).toBe("up");
});

// Choosing the remote the page is on again (to read everything again after it
// came back) starts the app over, and asks again: the verdict it had stays on
// screen until that ask answers, as on an interval ask.
test("the same remote chosen again keeps its verdict while it is asked", async () => {
  holding = false;
  hang = false;
  setEnv({ mode: "remote", baseUrl: "http://dead2.lan:12345", token: "" });
  window.dispatchEvent(new Event("focus"));
  await vi.waitFor(() => expect(snapshotHealth()).toBe("down"));
  hang = true;
  connectRemote("http://dead2.lan:12345", "", "dead2");
  expect(snapshotHealth()).toBe("down");
});

// A token rotated in the configuration reaches the active environment through
// setEnv (configuredRemotes.syncActiveToken): it is asked with at once, and an
// answer to the old token that lands after that is not the one kept.
test("a rotated token is asked with at once, and the old token's answer dropped", async () => {
  hang = false;
  holding = true;
  held = [];
  const env = getEnv();
  if (env.mode !== "remote") throw new Error("expected a remote environment");
  window.dispatchEvent(new Event("focus"));
  await vi.waitFor(() => expect(held.length).toBe(1));
  setEnv({ ...env, token: "rotated" });
  await vi.waitFor(() => expect(held.length).toBe(2));
  held[1]!(new Response(JSON.stringify({ data: [] }), { status: 200 }));
  await vi.waitFor(() => expect(snapshotHealth()).toBe("up"));
  held[0]!(new Response("{}", { status: 401 }));
  await new Promise((r) => setTimeout(r, 0));
  expect(snapshotHealth()).toBe("up");
});
