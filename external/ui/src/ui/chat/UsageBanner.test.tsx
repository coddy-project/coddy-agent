import React from "react";
import { render, screen } from "@testing-library/react";
import { expect, test } from "vitest";
import { setLocale } from "../i18n/i18n";
import { UsageBanner } from "./UsageBanner";
import type { ProviderUsage, UsageWindow } from "./providerUsage";

const now = new Date("2026-09-06T17:47:12Z");

function fixture(): ProviderUsage {
  return {
    provider: "neuraldeep",
    providerType: "neuraldeep",
    plan: "pro",
    windows: [
      { id: "session", label: "3h", used: 407, limit: 15000, usedPercent: 2.71, resetsAt: "2026-09-06T17:59:59Z", resetInSec: 777 },
      { id: "week", label: "week", used: 9981, limit: 150000, usedPercent: 6.65, resetsAt: "2026-09-07T00:00:00Z", resetInSec: 22378 },
    ],
    wallet: { balanceRub: 1250, spentRub30d: 3470.5 },
  };
}

function windowAt(u: ProviderUsage, i: number): UsageWindow {
  const w = u.windows?.[i];
  if (!w) throw new Error(`no window ${i}`);
  return w;
}

test("the banner appears at the threshold, reads the reset time and dismisses per period", () => {
  const warm = fixture();
  windowAt(warm, 0).usedPercent = 85;
  let dismissed = "";
  const { rerender } = render(
    <UsageBanner usage={warm} modelId="neuraldeep/qwen3.8-27b" now={now} onDismiss={(k) => (dismissed = k)} />,
  );
  const banner = screen.getByTestId("usage-banner");
  expect(banner.getAttribute("data-tone")).toBe("warn");
  expect(banner.textContent).toContain("You've used 85% of your NeuralDeep 3h limit");
  expect(banner.textContent).toContain("resets");
  (banner.querySelector("button") as HTMLButtonElement).click();
  expect(dismissed).toBe("neuraldeep@session@2026-09-06T17:59:59Z");
  rerender(
    <UsageBanner usage={warm} modelId="neuraldeep/qwen3.8-27b" now={now} dismissedKey={dismissed} />,
  );
  expect(screen.queryByTestId("usage-banner")).toBeNull();
});

test("the banner says a limit is reached in the error tone and stays quiet below the threshold", () => {
  const blocked: ProviderUsage = { ...fixture(), blocked: true, blockers: ["session_exhausted"], retryAt: "2026-09-06T17:59:59Z", retryInSec: 767 };
  const { container } = render(<UsageBanner usage={blocked} modelId="neuraldeep/qwen3.8-27b" now={now} />);
  const banner = container.querySelector("[data-testid=usage-banner]") as HTMLElement;
  expect(banner.getAttribute("data-tone")).toBe("error");
  expect(banner.textContent).toContain("Usage limit reached · Resets");
  const quiet = render(<UsageBanner usage={fixture()} modelId="neuraldeep/qwen3.8-27b" now={now} />);
  expect(quiet.container.querySelector("[data-testid=usage-banner]")).toBeNull();
});

test("the banner names the cause of a block that has no reset", () => {
  const wallet: ProviderUsage = { ...fixture(), blocked: true, blockers: ["wallet_empty"] };
  const r1 = render(<UsageBanner usage={wallet} modelId="neuraldeep/qwen3.8-27b" now={now} />);
  expect(r1.container.querySelector("[data-testid=usage-banner]")?.textContent).toContain("The wallet is empty");
  const key: ProviderUsage = { ...fixture(), blocked: true, blockers: ["key_blocked"] };
  const r2 = render(<UsageBanner usage={key} modelId="neuraldeep/qwen3.8-27b" now={now} />);
  expect(r2.container.querySelector("[data-testid=usage-banner]")?.textContent).toContain("The key is blocked");
  const rate: ProviderUsage = { ...fixture(), blocked: true, blockers: ["rpm_exhausted"], retryInSec: 42 };
  const r3 = render(<UsageBanner usage={rate} modelId="neuraldeep/qwen3.8-27b" now={now} />);
  expect(r3.container.querySelector("[data-testid=usage-banner]")?.textContent).toContain("Rate limited");
});

test("the banner says the turn resumes by itself while the agent waits for the reset", () => {
  const waiting: ProviderUsage = { ...fixture(), blocked: true, resuming: true, retryAt: "2026-09-06T17:59:59Z", retryInSec: 767 };
  const { container } = render(<UsageBanner usage={waiting} modelId="neuraldeep/qwen3.8-27b" now={now} />);
  const banner = container.querySelector("[data-testid=usage-banner]") as HTMLElement;
  expect(banner.getAttribute("data-tone")).toBe("warn");
  expect(banner.textContent).toContain("Auto-resuming at");
});

test("the reader's language names the window in the banner", () => {
  expect(setLocale("ru")).toBe(true);
  try {
    const warm = fixture();
    windowAt(warm, 1).usedPercent = 85;
    const { container } = render(<UsageBanner usage={warm} modelId="neuraldeep/qwen3.8-27b" now={now} />);
    expect(container.querySelector("[data-testid=usage-banner]")?.textContent).toContain(
      "Использовано 85% лимита NeuralDeep (неделя)",
    );
  } finally {
    setLocale("en");
  }
});
