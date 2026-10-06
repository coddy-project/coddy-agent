import "@testing-library/jest-dom/vitest";
import { beforeEach, vi } from "vitest";
import { resetPageMemoryForTests } from "./ui/env/pageMemory";
import { forgetWorkingCopies } from "./ui/changes/workingCopy";

// What a page keeps while it is open starts empty for every test, as it does
// for every page.
beforeEach(() => {
  resetPageMemoryForTests();
  forgetWorkingCopies();
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
