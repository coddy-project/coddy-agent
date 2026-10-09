import { expect, test, vi } from "vitest";
import { readOpening, type OpeningRead } from "./openingRead";

// Send waits for the opened session's own settings, so the read that brings
// them is not given up on a passing failure, and not run again once something
// else has settled the session.

const DELAYS = [0, 5, 5, 5];

function reads(...outcomes: OpeningRead[]) {
  const read = vi.fn(async () => outcomes.shift() ?? "failed");
  return read;
}

test("a read that fails is run again until one says something", async () => {
  const read = reads("failed", "failed", "read");
  const outcome = await readOpening(
    read,
    () => true,
    new AbortController().signal,
    DELAYS,
  );
  expect(outcome).toBe("read");
  expect(read).toHaveBeenCalledTimes(3);
});

test("a 404 is final: the session is not there", async () => {
  const read = reads("missing");
  const outcome = await readOpening(
    read,
    () => true,
    new AbortController().signal,
    DELAYS,
  );
  expect(outcome).toBe("missing");
  expect(read).toHaveBeenCalledTimes(1);
});

// An outage longer than the first tries still ends: the read keeps running,
// at the slower pace, for as long as the visit lasts.
test("a read that keeps failing runs at the slower pace until it gets through", async () => {
  const read = reads("failed", "failed", "failed", "failed", "failed", "read");
  const outcome = await readOpening(
    read,
    () => true,
    new AbortController().signal,
    DELAYS,
    5,
  );
  expect(outcome).toBe("read");
  expect(read).toHaveBeenCalledTimes(6);
});

test("a read that keeps failing stops when the visit ends", async () => {
  const visit = new AbortController();
  let calls = 0;
  const read = vi.fn(async (): Promise<OpeningRead> => {
    calls += 1;
    if (calls === 6) visit.abort();
    return "failed";
  });
  const outcome = await readOpening(read, () => true, visit.signal, DELAYS, 5);
  expect(outcome).toBe("superseded");
  expect(read).toHaveBeenCalledTimes(6);
});

// A later read took this one's place; should that one fail too, the session
// would wait for good.
test("a read another one took over is run again while the session still waits", async () => {
  const read = reads("superseded", "read");
  const outcome = await readOpening(
    read,
    () => true,
    new AbortController().signal,
    DELAYS,
  );
  expect(outcome).toBe("read");
  expect(read).toHaveBeenCalledTimes(2);
});

test("nothing is read again once something else settled the session", async () => {
  let waiting = true;
  const read = vi.fn(async (): Promise<OpeningRead> => {
    waiting = false;
    return "superseded";
  });
  const outcome = await readOpening(
    read,
    () => waiting,
    new AbortController().signal,
    DELAYS,
  );
  expect(outcome).toBe("superseded");
  expect(read).toHaveBeenCalledTimes(1);
});

test("leaving the session ends the pause at once and reads nothing more", async () => {
  const visit = new AbortController();
  const read = vi.fn(async (): Promise<OpeningRead> => {
    setTimeout(() => visit.abort(), 0);
    return "failed";
  });
  const started = Date.now();
  const outcome = await readOpening(
    read,
    () => true,
    visit.signal,
    [0, 60_000],
  );
  expect(outcome).toBe("superseded");
  expect(read).toHaveBeenCalledTimes(1);
  expect(Date.now() - started).toBeLessThan(5_000);
});
