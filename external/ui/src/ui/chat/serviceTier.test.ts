import { describe, expect, it } from "vitest";
import {
  pickServiceTier,
  readServiceTierCookie,
  writeServiceTierCookie,
} from "./serviceTier";

describe("service tier preference", () => {
  it("ignores a malformed stored value", () => {
    document.cookie = "coddy_service_tier=%; Path=/";
    expect(readServiceTierCookie()).toBe("");
    writeServiceTierCookie("");
  });
  it("keeps an offered choice and drops an incompatible one", () => {
    expect(pickServiceTier("priority", ["", "default", "priority"])).toBe(
      "priority",
    );
    expect(pickServiceTier("ultrafast", ["", "default"])).toBe("");
    expect(pickServiceTier("priority", [])).toBe("");
  });
  it("remembers an explicit reset as well as Standard", () => {
    writeServiceTierCookie("default");
    expect(readServiceTierCookie()).toBe("default");
    writeServiceTierCookie("");
    expect(readServiceTierCookie()).toBe("");
  });
});
