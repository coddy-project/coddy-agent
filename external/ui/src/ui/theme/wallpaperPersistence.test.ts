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

test("gallery identity commits with the blob and survives reload", async () => {
  const db = controlledDb();
  let prefs = await import("./backdropPrefs");
  const gallery = { scope: "https://node.test/mount", name: "a.png" };
  const blob = picture("selected");
  const selection = prefs.setWallpaper(blob, undefined, gallery);
  await waitFor(() => expect(db.operations).toHaveLength(1));
  expect(prefs.backdropSnapshot()).not.toHaveProperty("gallery");
  db.commit(db.operations[0]!);
  await selection;
  expect(prefs.backdropSnapshot()).toHaveProperty("gallery", gallery);
  expect(db.stored()).toEqual({ blob, gallery });
  vi.resetModules();
  prefs = await import("./backdropPrefs");
  const startup = prefs.loadWallpaper();
  await waitFor(() => expect(db.operations).toHaveLength(2));
  db.commit(db.operations[1]!, db.stored());
  await startup;
  expect(prefs.backdropSnapshot()).toHaveProperty("gallery", gallery);
  expect(prefs.backdropSnapshot().wallpaper).not.toBeNull();
});

test("legacy stored blobs remain private and are never uploaded", async () => {
  const db = controlledDb();
  const fetch = vi.fn();
  vi.stubGlobal("fetch", fetch);
  const prefs = await import("./backdropPrefs");
  const startup = prefs.loadWallpaper();
  await waitFor(() => expect(db.operations).toHaveLength(1));
  db.commit(db.operations[0]!, picture("private"));
  await startup;
  expect(prefs.backdropSnapshot().wallpaper).not.toBeNull();
  expect(prefs.backdropSnapshot()).not.toHaveProperty("gallery");
  expect(fetch).not.toHaveBeenCalled();
});

test("persisted gallery identity cannot contain URL credentials or query tokens", async () => {
  const db = controlledDb();
  const prefs = await import("./backdropPrefs");
  const blob = picture("selected");
  const selection = prefs.setWallpaper(blob, undefined, {
    scope: "https://user:password@node.test/mount/?token=secret#private",
    name: "a.png",
  });
  await waitFor(() => expect(db.operations).toHaveLength(1));
  db.commit(db.operations[0]!);
  await selection;
  expect(db.stored()).toEqual({
    blob,
    gallery: { scope: "https://node.test/mount", name: "a.png" },
  });
});

test("concurrent startup callers share one IndexedDB read", async () => {
  const db = controlledDb();
  const prefs = await import("./backdropPrefs");
  const first = prefs.loadWallpaper();
  const second = prefs.loadWallpaper();
  await waitFor(() => expect(db.operations.length).toBeGreaterThan(0));
  expect(db.operations).toHaveLength(1);
  db.commit(db.operations[0]!, picture("saved"));
  await Promise.all([first, second]);
  expect(prefs.backdropSnapshot().wallpaper).not.toBeNull();
});

test.each(["saved", "empty", "failed"])(
  "shared delayed startup resolves the %s storage state before an automatic choice",
  async (outcome) => {
    const db = controlledDb();
    const prefs = await import("./backdropPrefs");
    const initial = prefs.loadWallpaper();
    const automatic = (async () => {
      await prefs.loadWallpaper();
      if (
        !prefs.backdropSnapshot().wallpaper &&
        !prefs.backdropSnapshot().storageError
      ) {
        await prefs.setWallpaper(picture("uploaded"));
      }
    })();
    await waitFor(() => expect(db.operations).toHaveLength(1));
    if (outcome === "failed") db.operations[0]!.transaction.onabort?.();
    else
      db.commit(
        db.operations[0]!,
        outcome === "saved" ? picture("saved") : undefined,
      );
    await initial;
    if (outcome === "empty") {
      await waitFor(() => expect(db.operations).toHaveLength(2));
      db.commit(db.operations[1]!);
    }
    await automatic;
    expect(
      db.operations.filter((operation) => operation.kind === "get"),
    ).toHaveLength(1);
    expect(
      db.operations.filter((operation) => operation.kind === "put"),
    ).toHaveLength(outcome === "empty" ? 1 : 0);
    expect(Boolean(prefs.backdropSnapshot().storageError)).toBe(
      outcome === "failed",
    );
    expect(Boolean(prefs.backdropSnapshot().wallpaper)).toBe(
      outcome !== "failed",
    );
  },
);

test("a deleted saved wallpaper is cleared after its pending startup restore", async () => {
  const db = controlledDb();
  const prefs = await import("./backdropPrefs");
  const gallery = { scope: "https://node.test", name: "a.png" };
  const startup = prefs.loadWallpaper();
  const revision = prefs.wallpaperChoiceRevision();
  const removal = prefs.clearDeletedWallpaper(gallery, revision);
  await waitFor(() => expect(db.operations).toHaveLength(1));
  db.commit(db.operations[0]!, { blob: picture("saved"), gallery });
  await startup;
  await waitFor(() => expect(db.operations).toHaveLength(2));
  expect(db.operations[1]!.kind).toBe("delete");
  db.commit(db.operations[1]!);
  await removal;
  expect(db.stored()).toBeUndefined();
  expect(prefs.backdropSnapshot().wallpaper).toBeNull();
});

test.each(["before", "during"])(
  "failed selection %s bootstrap preserves the saved picture",
  async (when) => {
    const db = controlledDb();
    const prefs = await import("./backdropPrefs");
    let startup: Promise<void>;
    if (when === "before") {
      expect(
        (await prefs.setWallpaper(new Blob(["bad"], { type: "text/plain" })))
          .refusal,
      ).toBe("not-image");
      startup = prefs.loadWallpaper();
    } else {
      startup = prefs.loadWallpaper();
      await waitFor(() => expect(db.operations).toHaveLength(1));
      expect(
        (await prefs.setWallpaper(new Blob(["bad"], { type: "text/plain" })))
          .refusal,
      ).toBe("not-image");
    }
    await waitFor(() => expect(db.operations).toHaveLength(1));
    db.commit(db.operations[0]!, picture("saved"));
    await startup;
    expect(prefs.backdropSnapshot().wallpaper).not.toBeNull();
  },
);
