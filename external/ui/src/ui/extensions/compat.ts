/**
 * Engine tier for the compatibility stylesheet layer. The desktop shells
 * embed the SPA in a JCEF panel whose Chromium trails the browser by years
 * (IntelliJ 2022 ships Chromium 98-104): `:has()` is 105+, `color-mix()` is
 * 111+, so a stylesheet that looks correct in a browser can fall apart there.
 * A legacy engine gets `data-coddy-engine="legacy"` on the root and only the
 * compat styles, of the host and of the extensions, layered over the ordinary
 * stylesheet.
 */
export type EngineTier = "modern" | "legacy";

export type EngineInfo = Readonly<{
  tier: EngineTier;
  /** Chromium major version when the engine says one, else null. */
  chromiumMajor: number | null;
}>;

/** The tier boundary: below 105 there is no `:has()`, and 105-110 has no
 * `color-mix()`; the layer exists for the whole range below 111. */
export const LEGACY_CHROMIUM_MAX = 110;

export function detectEngine(userAgent: string): EngineInfo {
  const chromium = /Chrom(?:e|ium)\/(\d+)/.exec(userAgent);
  const major = chromium ? Number(chromium[1]) : null;
  // Safari/Firefox report no Chromium version; they are kept modern unless
  // they are old enough to lack the same features, which the layer does not
  // claim to cover.
  return Object.freeze({
    tier: major !== null && major <= LEGACY_CHROMIUM_MAX ? "legacy" : "modern",
    chromiumMajor: major,
  });
}

let current: EngineInfo = detectEngine(
  typeof navigator === "undefined" ? "" : navigator.userAgent,
);

export const engineInfo = (): EngineInfo => current;

/** Marks the root once at boot, before the first paint decision. */
export function bootstrapEngineTier() {
  current = detectEngine(
    typeof navigator === "undefined" ? "" : navigator.userAgent,
  );
  if (typeof document === "undefined") return;
  document.documentElement.dataset.coddyEngine = current.tier;
  if (current.chromiumMajor !== null)
    document.documentElement.dataset.coddyChromium = String(
      current.chromiumMajor,
    );
}
