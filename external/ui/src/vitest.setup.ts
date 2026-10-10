import "@testing-library/jest-dom/vitest";
import { beforeEach, vi } from "vitest";
import { resetPageMemoryForTests } from "./ui/env/pageMemory";
import { resetStorageMonitorForTest } from "./ui/env/storageStatus";
import { forgetWorkingCopies } from "./ui/changes/workingCopy";
import {
  CODDY_WORKSPACE_DIR_COOKIE,
  CODDY_WORKTREE_COOKIE,
} from "./ui/chat/workspaceCookies";

// What a page keeps while it is open starts empty for every test, as it does
// for every page; so does what the browser remembers for the start screen (a
// folder picked in one test must not open the next one's start screen).
beforeEach(() => {
  resetPageMemoryForTests();
  // What the page knows of the server's disk starts unknown for every test.
  resetStorageMonitorForTest();
  forgetWorkingCopies();
  for (const name of [CODDY_WORKSPACE_DIR_COOKIE, CODDY_WORKTREE_COOKIE]) {
    document.cookie = `${name}=; Path=/; Max-Age=0`;
  }
});

Object.defineProperty(window, "matchMedia", {
  writable: true,
  configurable: true,
  value: vi.fn().mockImplementation((query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: vi.fn(),
    removeListener: vi.fn(),
    addEventListener: vi.fn(),
    removeEventListener: vi.fn(),
    dispatchEvent: vi.fn(),
  })),
});

globalThis.ResizeObserver ??= class ResizeObserver {
  disconnect() {}
  observe() {}
  unobserve() {}
};
