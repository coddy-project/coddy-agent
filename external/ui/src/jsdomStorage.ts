// The Web Storage areas of jsdom's window, put on the global object of a
// test.
//
// Node 25 turned its own Web Storage on: globalThis.localStorage and
// sessionStorage are Node's, and localStorage is undefined unless the process
// was started with --localstorage-file. Vitest's jsdom environment copies a
// window property onto the global object only when the global lacks it or the
// property is on a fixed list of its own, which names neither storage, so
// under Node 25 every test read Node's empty slot instead of jsdom's storage
// (issue #609). vitest.setup.ts puts jsdom's back the way the environment puts
// every other window property: read through to the window, replaced by an
// assignment or vi.stubGlobal.
export const JSDOM_STORAGE_KEYS = ["localStorage", "sessionStorage"] as const;

type StorageKey = (typeof JSDOM_STORAGE_KEYS)[number];

export function installJsdomStorage(
  target: object,
  win: Record<StorageKey, Storage>,
): void {
  for (const key of JSDOM_STORAGE_KEYS) {
    let replaced = false;
    let replacement: unknown;
    Object.defineProperty(target, key, {
      configurable: true,
      enumerable: true,
      get: () => (replaced ? replacement : win[key]),
      set: (value: unknown) => {
        replaced = true;
        replacement = value;
      },
    });
  }
}
