import { describe, expect, it } from "vitest";
import {
  shouldShowOnboarding,
  type OnboardingStatus,
} from "./onboardingStatus";

const ready: OnboardingStatus = {
  first_run: false,
  has_config: true,
  has_providers: true,
  has_models: true,
  has_agent_model: true,
  has_agent_credentials: true,
  missing_api_keys: [],
};

describe("shouldShowOnboarding", () => {
  it("opens for a first run and for an agent without credentials", () => {
    expect(shouldShowOnboarding({ ...ready, first_run: true })).toBe(true);
    expect(
      shouldShowOnboarding({ ...ready, has_agent_credentials: false }),
    ).toBe(true);
  });

  it("ignores an unused keyless provider and an unavailable status", () => {
    expect(
      shouldShowOnboarding({ ...ready, missing_api_keys: ["spare"] }),
    ).toBe(false);
    expect(shouldShowOnboarding(null)).toBe(false);
  });
});
