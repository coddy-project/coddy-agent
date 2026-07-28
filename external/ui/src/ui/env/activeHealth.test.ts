import { describe, expect, it } from "vitest";
import { probeEnvHealth } from "./activeHealth";

describe("probeEnvHealth", () => {
  it("reports the local environment as up without any network call", async () => {
    expect(await probeEnvHealth({ mode: "local" })).toBe("up");
  });
});
