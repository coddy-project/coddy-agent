import React from "react";
import { render, screen } from "@testing-library/react";
import { expect, test } from "vitest";
import { UsagePill } from "./UsagePill";
import { UsageBanner } from "./UsageBanner";
import type { ProviderUsage } from "./providerUsage";

const now = new Date("2026-09-06T17:47:12Z");

function fixture(): ProviderUsage {
  return {
    provider: "neuraldeep",
    providerType: "neuraldeep",
    plan: "pro",
    keyName: "coddy",
    windows: [
      { id: "session", label: "3h", used: 407, limit: 15000, usedPercent: 2.71, resetsAt: "2026-09-06T17:59:59Z", resetInSec: 777 },
      { id: "week", label: "week", used: 9981, limit: 150000, usedPercent: 6.65, resetsAt: "2026-09-07T00:00:00Z", resetInSec: 22378 },
      { id: "day", label: "day", usedPercent: 0 },
    ],
    wallet: { balanceRub: 1250, spentRub30d: 3470.5 },
    unlimitedModels: ["qwen3.6-35b-a3b"],
  };
}

test("the pill reads the session window and lists the rest in its tooltip", () => {
  render(<UsagePill usage={fixture()} modelId="neuraldeep/qwen3.8-27b" now={now} />);
  const host = screen.getByTestId("composer-usage-pill");
  expect(host.getAttribute("data-tone")).toBe("ok");
  expect(host.querySelector(".composer-usage-pill")?.textContent).toBe("3h 3%");
  const tip = host.querySelector(".composer-usage-tip")?.textContent ?? "";
  expect(tip).toContain("NeuralDeep · pro");
  expect(tip).toContain("week 7%");
  expect(tip).not.toContain("day 0%");
  expect(tip).toContain("Wallet 1 250 ₽ (3 471 ₽ spent in 30 days)");
});

test("the pill hides for another provider and for an unsupported answer", () => {
  const { container } = render(<UsagePill usage={fixture()} modelId="stub/model" now={now} />);
  expect(container.querySelector("[data-testid=composer-usage-pill]")).toBeNull();
  const none = render(<UsagePill usage={{ provider: "neuraldeep", unsupported: true }} modelId="neuraldeep/x" now={now} />);
  expect(none.container.querySelector("[data-testid=composer-usage-pill]")).toBeNull();
});

test("the pill turns to the warning tone at 80 percent and to the error tone on a block", () => {
  const warm = fixture();
  warm.windows![0].usedPercent = 85;
  const warmRender = render(<UsagePill usage={warm} modelId="neuraldeep/qwen3.8-27b" now={now} />);
  expect(
    warmRender.container.querySelector("[data-testid=composer-usage-pill]")?.getAttribute("data-tone"),
  ).toBe("warn");
  const blocked: ProviderUsage = { ...fixture(), blocked: true, blockers: ["session_exhausted"], retryAt: "2026-09-06T17:59:59Z", retryInSec: 767 };
  const { container } = render(<UsagePill usage={blocked} modelId="neuraldeep/qwen3.8-27b" now={now} />);
  const host = container.querySelector("[data-testid=composer-usage-pill]") as HTMLElement;
  expect(host.getAttribute("data-tone")).toBe("error");
  expect(host.querySelector(".composer-usage-pill")?.textContent).toBe("limit reached");
});

test("an unlimited model and a rejected key have their own pills", () => {
  const r1 = render(<UsagePill usage={fixture()} modelId="neuraldeep/qwen3.6-35b-a3b" now={now} />);
  expect(r1.container.querySelector(".composer-usage-pill")?.textContent).toBe("∞ volume");
  const r2 = render(<UsagePill usage={{ ...fixture(), error: "unauthorized" }} modelId="neuraldeep/qwen3.8-27b" now={now} />);
  const pills = r2.container.querySelectorAll(".composer-usage-pill");
  expect(pills[pills.length - 1]?.textContent).toBe("key rejected");
});

test("a narrow shell keeps the pill to the number or one word", () => {
  const r1 = render(<UsagePill usage={fixture()} modelId="neuraldeep/qwen3.8-27b" now={now} compact />);
  expect(r1.container.querySelector(".composer-usage-pill")?.textContent).toBe("3%");
  const blocked: ProviderUsage = { ...fixture(), blocked: true, blockers: ["session_exhausted"], retryAt: "2026-09-06T17:59:59Z", retryInSec: 767 };
  const r2 = render(<UsagePill usage={blocked} modelId="neuraldeep/qwen3.8-27b" now={now} compact />);
  expect(r2.container.querySelector(".composer-usage-pill")?.textContent).toBe("limit");
  const r3 = render(<UsagePill usage={fixture()} modelId="neuraldeep/qwen3.6-35b-a3b" now={now} compact />);
  expect(r3.container.querySelector(".composer-usage-pill")?.textContent).toBe("∞");
  const r4 = render(<UsagePill usage={{ ...fixture(), error: "unauthorized" }} modelId="neuraldeep/qwen3.8-27b" now={now} compact />);
  expect(r4.container.querySelector(".composer-usage-pill")?.textContent).toBe("key");
});

test("the banner appears at the threshold, reads the reset time and dismisses per period", () => {
  const warm = fixture();
  warm.windows![0].usedPercent = 85;
  let dismissed = "";
  const { rerender } = render(
    <UsageBanner usage={warm} modelId="neuraldeep/qwen3.8-27b" now={now} onDismiss={(k) => (dismissed = k)} />,
  );
  const banner = screen.getByTestId("usage-banner");
  expect(banner.getAttribute("data-tone")).toBe("warn");
  expect(banner.textContent).toContain("You've used 85% of your NeuralDeep 3h limit");
  expect(banner.textContent).toContain("resets");
  (banner.querySelector("button") as HTMLButtonElement).click();
  expect(dismissed).toBe("session@2026-09-06T17:59:59Z");
  rerender(
    <UsageBanner usage={warm} modelId="neuraldeep/qwen3.8-27b" now={now} dismissedKey={dismissed} />,
  );
  expect(screen.queryByTestId("usage-banner")).toBeNull();
});

test("the banner says a limit is reached in the error tone and stays quiet below the threshold", () => {
  const blocked: ProviderUsage = { ...fixture(), blocked: true, blockers: ["session_exhausted"], retryAt: "2026-09-06T17:59:59Z", retryInSec: 767 };
  render(<UsageBanner usage={blocked} modelId="neuraldeep/qwen3.8-27b" now={now} />);
  const banner = screen.getByTestId("usage-banner");
  expect(banner.getAttribute("data-tone")).toBe("error");
  expect(banner.textContent).toContain("Usage limit reached · Resets");
  const quiet = render(<UsageBanner usage={fixture()} modelId="neuraldeep/qwen3.8-27b" now={now} />);
  expect(quiet.container.querySelector("[data-testid=usage-banner]")).toBeNull();
});
