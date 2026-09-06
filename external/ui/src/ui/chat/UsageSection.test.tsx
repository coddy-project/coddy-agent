import React from "react";
import { render } from "@testing-library/react";
import { expect, test } from "vitest";
import { setLocale } from "../i18n/i18n";
import { UsageSection } from "./UsageSection";
import type { ProviderUsage, UsageWindow } from "./providerUsage";

const now = new Date("2026-09-06T17:47:12Z");

function fixture(): ProviderUsage {
  return {
    provider: "neuraldeep",
    providerType: "neuraldeep",
    plan: "pro",
    keyName: "coddy",
    fetchedAt: "2026-09-06T17:47:10Z",
    windows: [
      { id: "session", label: "3h", used: 407, limit: 15000, usedPercent: 2.71, resetsAt: "2026-09-06T17:59:59Z", resetInSec: 777 },
      { id: "week", label: "week", used: 9981, limit: 150000, usedPercent: 6.65, resetsAt: "2026-09-07T00:00:00Z", resetInSec: 22378 },
      { id: "day", label: "day", usedPercent: 0 },
    ],
    wallet: { balanceRub: 1250, spentRub30d: 3470.5 },
    unlimitedModels: ["qwen3.6-35b-a3b"],
  };
}

function windowAt(u: ProviderUsage, i: number): UsageWindow {
  const w = u.windows?.[i];
  if (!w) throw new Error(`no window ${i}`);
  return w;
}

test("the section lists the metered windows with their reset time and percent, and the wallet", () => {
  const { container } = render(<UsageSection usage={fixture()} modelId="neuraldeep/qwen3.8-27b" now={now} />);
  const section = container.querySelector("[data-testid=context-usage]") as HTMLElement;
  expect(section.getAttribute("data-kind")).toBe("metered");
  expect(section.querySelector(".context-usage-title")?.textContent).toBe("NeuralDeep · pro");
  const session = section.querySelector("[data-testid=context-usage-row-session]") as HTMLElement;
  expect(session.getAttribute("data-tone")).toBe("ok");
  expect(session.querySelector(".context-usage-label")?.textContent).toBe("3h");
  expect(session.querySelector(".context-usage-meta")?.textContent).toContain("resets");
  expect(session.querySelector(".context-usage-pct")?.textContent).toBe("3%");
  expect(section.querySelector("[data-testid=context-usage-row-week] .context-usage-label")?.textContent).toBe("week");
  expect(section.querySelector("[data-testid=context-usage-row-day]")).toBeNull();
  expect(section.querySelector("[data-testid=context-usage-wallet]")?.textContent).toContain("1 250 ₽");
  expect(section.querySelector("[data-testid=context-usage-note]")).toBeNull();
});

test("the section hides for another provider and an unsupported answer", () => {
  const r1 = render(<UsageSection usage={fixture()} modelId="stub/model" now={now} />);
  expect(r1.container.querySelector("[data-testid=context-usage]")).toBeNull();
  const r2 = render(<UsageSection usage={{ provider: "neuraldeep", unsupported: true }} modelId="neuraldeep/x" now={now} />);
  expect(r2.container.querySelector("[data-testid=context-usage]")).toBeNull();
});

test("a window at 80 percent turns to the warning tone, a block to the error tone with its note", () => {
  const warm = fixture();
  windowAt(warm, 0).usedPercent = 85;
  const r1 = render(<UsageSection usage={warm} modelId="neuraldeep/qwen3.8-27b" now={now} />);
  expect(r1.container.querySelector("[data-testid=context-usage-row-session]")?.getAttribute("data-tone")).toBe("warn");
  const blocked: ProviderUsage = { ...fixture(), blocked: true, blockers: ["session_exhausted"], retryAt: "2026-09-06T17:59:59Z", retryInSec: 767 };
  windowAt(blocked, 0).usedPercent = 100;
  windowAt(blocked, 0).exhausted = true;
  const r2 = render(<UsageSection usage={blocked} modelId="neuraldeep/qwen3.8-27b" now={now} />);
  const note = r2.container.querySelector("[data-testid=context-usage-note]") as HTMLElement;
  expect(note.textContent).toContain("Usage limit reached");
  expect(note.classList.contains("context-usage-note--error")).toBe(true);
  expect(r2.container.querySelector("[data-testid=context-usage-row-session]")?.getAttribute("data-tone")).toBe("error");
  expect(r2.container.querySelector("[data-testid=context-usage-row-week]")?.getAttribute("data-tone")).toBe("ok");
  const wallet: ProviderUsage = { ...fixture(), blocked: true, blockers: ["wallet_empty"] };
  const r3 = render(<UsageSection usage={wallet} modelId="neuraldeep/qwen3.8-27b" now={now} />);
  expect(r3.container.querySelector("[data-testid=context-usage-note]")?.textContent).toContain("wallet is empty");
});

test("an unlimited model, a rejected key and a waiting turn have their notes", () => {
  const r1 = render(<UsageSection usage={fixture()} modelId="neuraldeep/qwen3.6-35b-a3b" now={now} />);
  expect(r1.container.querySelector("[data-testid=context-usage-note]")?.textContent).toContain("bypasses");
  const r2 = render(<UsageSection usage={{ ...fixture(), error: "unauthorized" }} modelId="neuraldeep/qwen3.8-27b" now={now} />);
  expect(r2.container.querySelector("[data-testid=context-usage-note]")?.textContent).toContain("rejected");
  expect(r2.container.querySelector(".context-usage-rows")).toBeNull();
  const waiting: ProviderUsage = { ...fixture(), blocked: true, resuming: true, retryAt: "2026-09-06T17:59:59Z", retryInSec: 767 };
  const r3 = render(<UsageSection usage={waiting} modelId="neuraldeep/qwen3.8-27b" now={now} />);
  const note = r3.container.querySelector("[data-testid=context-usage-note]") as HTMLElement;
  expect(note.textContent).toContain("Auto-resuming at");
  expect(note.classList.contains("context-usage-note--warn")).toBe(true);
});

test("the reader's language names the windows", () => {
  expect(setLocale("ru")).toBe(true);
  try {
    const { container } = render(<UsageSection usage={fixture()} modelId="neuraldeep/qwen3.8-27b" now={now} />);
    expect(container.querySelector("[data-testid=context-usage-row-week] .context-usage-label")?.textContent).toBe("неделя");
    expect(container.querySelector("[data-testid=context-usage-row-session] .context-usage-meta")?.textContent).toContain("сброс");
  } finally {
    setLocale("en");
  }
});
