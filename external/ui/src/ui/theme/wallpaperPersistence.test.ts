import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { waitFor } from "@testing-library/react";

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

function bitmap() {
  return { width: 1920, height: 1080, close: vi.fn() };
}

function picture(name: string) {
  return new Blob([name], { type: "image/png" });
}

type Operation = {
  kind: "get" | "put" | "delete";
  value?: unknown;
  request: { result?: unknown; onsuccess?: () => void; onerror?: () => void };
  transaction: {
    oncomplete?: () => void;
    onabort?: () => void;
    onerror?: () => void;
    abort: () => void;
    aborted?: boolean;
  };
};

/** Requests and transaction commits are separate events in IndexedDB. */
function controlledDb(abortImmediately = true) {
  let stored: unknown;
  const operations: Operation[] = [];
  const database = {
    close: vi.fn(),
    transaction: () => {
      const transaction: Operation["transaction"] & {
        objectStore?: () => unknown;
      } = {
        abort: vi.fn(() => {
          transaction.aborted = true;
          if (abortImmediately) transaction.onabort?.();
        }),
      };
      const operation = (kind: Operation["kind"], value?: unknown) => {
        const request = {};
        operations.push({ kind, value, request, transaction });
        return request;
      };
      transaction.objectStore = () => ({
        get: () => operation("get"),
        put: (value: unknown) => operation("put", value),
        delete: () => operation("delete"),
      });
      return transaction;
    },
  };
  vi.stubGlobal("indexedDB", {
    open: () => {
      const request: { result: typeof database; onsuccess?: () => void } = {
        result: database,
      };
      queueMicrotask(() => request.onsuccess?.());
      return request;
    },
  });
  const succeed = (operation: Operation, result?: unknown) => {
    operation.request.result = result;
    operation.request.onsuccess?.();
  };
  const commit = (operation: Operation, result?: unknown) => {
    succeed(operation, result);
    if (!operation.transaction.aborted) {
      if (operation.kind === "put") stored = operation.value;
      if (operation.kind === "delete") stored = undefined;
      operation.transaction.oncomplete?.();
    }
  };
  return { operations, succeed, commit, database, stored: () => stored };
}

beforeEach(() => {
  vi.resetModules();
  vi.stubGlobal("indexedDB", undefined);
  vi.stubGlobal(
    "createImageBitmap",
    vi.fn(async () => bitmap()),
  );
  let sequence = 0;
  vi.stubGlobal(
    "URL",
    class extends URL {
      static createObjectURL = vi.fn(() => `blob:wallpaper-${++sequence}`);
      static revokeObjectURL = vi.fn();
    },
  );
});

afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

describe("wallpaper persistence ordering", () => {
  test("a decode finishing after Remove cannot restore the picture", async () => {
    const prefs = await import("./backdropPrefs");
    const decoding = deferred<ReturnType<typeof bitmap>>();
    vi.stubGlobal("createImageBitmap", () => decoding.promise);
    const selection = prefs.setWallpaper(picture("old"));
    await prefs.clearWallpaper();
    decoding.resolve(bitmap());
    await selection;
    expect(prefs.backdropSnapshot().wallpaper).toBeNull();
  });

  test("an earlier decode cannot overwrite a newer selection", async () => {
    const prefs = await import("./backdropPrefs");
    const decoding = deferred<ReturnType<typeof bitmap>>();
    vi.stubGlobal(
      "createImageBitmap",
      vi
        .fn()
        .mockReturnValueOnce(decoding.promise)
        .mockResolvedValueOnce(bitmap()),
    );
    const old = prefs.setWallpaper(picture("old"));
    await prefs.setWallpaper(picture("new"));
    const chosen = prefs.backdropSnapshot().wallpaper;
    decoding.resolve(bitmap());
    await old;
    expect(prefs.backdropSnapshot().wallpaper).toBe(chosen);
    expect(URL.createObjectURL).toHaveBeenCalledTimes(1);
  });

  test("startup storage read cannot replace a picture selected meanwhile", async () => {
    const db = controlledDb();
    const prefs = await import("./backdropPrefs");
    const startup = prefs.loadWallpaper();
    await waitFor(() => expect(db.operations).toHaveLength(1));
    const selected = prefs.setWallpaper(picture("new"));
    await waitFor(() => expect(db.operations).toHaveLength(2));
    db.commit(db.operations[1]!);
    await selected;
    const chosen = prefs.backdropSnapshot().wallpaper;
    db.commit(db.operations[0]!, picture("old"));
    await startup;
    expect(prefs.backdropSnapshot().wallpaper).toBe(chosen);
  });

  test("request success does not report persistence before transaction commit", async () => {
    const db = controlledDb();
    const prefs = await import("./backdropPrefs");
    const selection = prefs.setWallpaper(picture("new"));
    await waitFor(() => expect(db.operations).toHaveLength(1));
    db.succeed(db.operations[0]!);
    await Promise.resolve();
    await Promise.resolve();
    expect(prefs.backdropSnapshot().wallpaper).toBeNull();
    db.operations[0]!.transaction.onabort?.();
    expect((await selection).refusal).toBe("storage");
    expect(prefs.backdropSnapshot().wallpaper).toBeNull();
  });

  test("an aborted delete does not claim that the saved wallpaper was removed", async () => {
    const db = controlledDb();
    const prefs = await import("./backdropPrefs");
    const selected = prefs.setWallpaper(picture("kept"));
    await waitFor(() => expect(db.operations).toHaveLength(1));
    db.commit(db.operations[0]!);
    await selected;
    const chosen = prefs.backdropSnapshot().wallpaper;
    const removal = prefs.clearWallpaper();
    await waitFor(() => expect(db.operations).toHaveLength(2));
    db.succeed(db.operations[1]!);
    db.operations[1]!.transaction.onabort?.();
    const outcome = await removal;
    expect(outcome).toEqual({ refusal: "storage" });
    expect(prefs.backdropSnapshot().wallpaper).toBe(chosen);
  });
});

test("new mutations wait for cancelled transactions to end", async () => {
  const db = controlledDb(false);
  const prefs = await import("./backdropPrefs");
  const old = prefs.setWallpaper(picture("old"));
  await waitFor(() => expect(db.operations).toHaveLength(1));
  const chosen = picture("new");
  const next = prefs.setWallpaper(chosen);
  await waitFor(() =>
    expect(db.operations[0]!.transaction.abort).toHaveBeenCalled(),
  );
  expect(db.operations).toHaveLength(1);
  db.operations[0]!.transaction.onabort?.();
  await old;
  await waitFor(() => expect(db.operations).toHaveLength(2));
  db.commit(db.operations[1]!);
  await next;
  db.commit(db.operations[0]!);
  expect(db.stored()).toBe(chosen);
  expect(URL.createObjectURL).toHaveBeenCalledTimes(1);
  expect(db.database.close).toHaveBeenCalledTimes(2);
});

test("cancellation after request success aborts persistence without another write", async () => {
  const db = controlledDb();
  const prefs = await import("./backdropPrefs");
  const kept = picture("kept");
  const first = prefs.setWallpaper(kept);
  await waitFor(() => expect(db.operations).toHaveLength(1));
  db.commit(db.operations[0]!);
  await first;
  const chosen = prefs.backdropSnapshot().wallpaper;
  const abort = new AbortController();
  const pending = prefs.setWallpaper(picture("cancelled"), abort.signal);
  await waitFor(() => expect(db.operations).toHaveLength(2));
  db.succeed(db.operations[1]!);
  abort.abort();
  await pending;
  db.commit(db.operations[1]!);
  expect(db.operations[1]!.transaction.abort).toHaveBeenCalled();
  expect(db.stored()).toBe(kept);
  expect(prefs.backdropSnapshot().wallpaper).toBe(chosen);
  expect(db.database.close).toHaveBeenCalledTimes(2);
});

test("an open failure preserves the picture and reports storage failure", async () => {
  const prefs = await import("./backdropPrefs");
  await prefs.setWallpaper(picture("kept"));
  const chosen = prefs.backdropSnapshot().wallpaper;
  vi.stubGlobal("indexedDB", {
    open: () => {
      throw new Error("denied");
    },
  });
  expect(await prefs.setWallpaper(picture("new"))).toEqual({
    refusal: "storage",
  });
  expect(prefs.backdropSnapshot().wallpaper).toBe(chosen);
});

test("a failed startup read is observable without rejecting", async () => {
  const db = controlledDb();
  const prefs = await import("./backdropPrefs");
  const startup = prefs.loadWallpaper();
  await waitFor(() => expect(db.operations).toHaveLength(1));
  db.operations[0]!.transaction.onabort?.();
  await startup;
  expect(prefs.backdropSnapshot()).toHaveProperty(
    "storageError",
    expect.any(Error),
  );
  expect(db.database.close).toHaveBeenCalledOnce();
});

test("blocked opens fail and close a connection arriving later", async () => {
  const close = vi.fn();
  const request: {
    result: { close: typeof close };
    onblocked?: () => void;
    onsuccess?: () => void;
  } = { result: { close } };
  vi.stubGlobal("indexedDB", {
    open: () => {
      queueMicrotask(() => request.onblocked?.());
      return request;
    },
  });
  const prefs = await import("./backdropPrefs");
  expect(await prefs.setWallpaper(picture("blocked"))).toEqual({
    refusal: "storage",
  });
  request.onsuccess?.();
  expect(close).toHaveBeenCalledOnce();
});

test("a startup read begun during a selection cannot restore an older picture", async () => {
  const db = controlledDb();
  const prefs = await import("./backdropPrefs");
  const selected = prefs.setWallpaper(picture("new"));
  await waitFor(() => expect(db.operations).toHaveLength(1));
  const startup = prefs.loadWallpaper();
  await Promise.resolve();
  await Promise.resolve();
  db.commit(db.operations[0]!);
  await selected;
  const chosen = prefs.backdropSnapshot().wallpaper;
  if (db.operations[1]) db.commit(db.operations[1], picture("old"));
  await startup;
  expect(prefs.backdropSnapshot().wallpaper).toBe(chosen);
});

test("a saved high-resolution picture is restored without a pixel limit", async () => {
  const db = controlledDb();
  const prefs = await import("./backdropPrefs");
  vi.stubGlobal("createImageBitmap", async () => ({
    width: 8192,
    height: 4096,
    close: vi.fn(),
  }));
  const startup = prefs.loadWallpaper();
  await waitFor(() => expect(db.operations).toHaveLength(1));
  db.commit(db.operations[0]!, picture("legacy"));
  await startup;
  expect(prefs.backdropSnapshot().wallpaper).toBe("blob:wallpaper-1");
  expect(prefs.backdropSnapshot().storageError).toBeUndefined();
  expect(URL.createObjectURL).toHaveBeenCalledOnce();
});
