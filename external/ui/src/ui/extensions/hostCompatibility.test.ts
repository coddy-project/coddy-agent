import { expect, test, vi } from "vitest";
import {
  EXTENSION_FEATURES,
  fetchExtensionHostInfo,
  validateExtensionCompatibility,
  type ExtensionHostInfo,
} from "./hostCompatibility";

const info: ExtensionHostInfo = {
  sdk_version: 1,
  coddy_version: "1.4.23",
  features: EXTENSION_FEATURES,
};

test("required features and the Coddy engine range are checked together", () => {
  expect(() =>
    validateExtensionCompatibility(
      {
        required_features: ["composer.controls", "stream.text"],
        engines: { coddy: ">=1.4.0 <2.0.0" },
      },
      info,
    ),
  ).not.toThrow();
  expect(() =>
    validateExtensionCompatibility(
      { required_features: ["composer.controls", "future.feature"] },
      info,
    ),
  ).toThrow(/future\.feature/);
  expect(() =>
    validateExtensionCompatibility(
      { engines: { coddy: ">=1.5.0 <2.0.0" } },
      info,
    ),
  ).toThrow(/1\.4\.23/);
  expect(() =>
    validateExtensionCompatibility({ engines: { coddy: "not a range" } }, info),
  ).toThrow(/engines\.coddy/);
});

test("host info exposes the SDK version, server version and stable feature list", async () => {
  const request = vi.fn(async () => Response.json({ version: "1.4.23" }));
  const read = await fetchExtensionHostInfo(request);
  expect(read).toEqual(info);
  expect(read.features).not.toBe(EXTENSION_FEATURES);
  expect(read.features).toEqual(EXTENSION_FEATURES);
});
