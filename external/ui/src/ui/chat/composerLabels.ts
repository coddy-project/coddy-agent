type TFn = (key: string, params?: Record<string, string | number>) => string;

export function fmtBytes(n: number, t: TFn): string {
  if (n < 1024) return t("composer.bytesB", { n });
  if (n < 1024 * 1024) {
    return t("composer.bytesKB", { n: (n / 1024).toFixed(1) });
  }
  return t("composer.bytesMB", { n: (n / (1024 * 1024)).toFixed(1) });
}

export function clamp01(x: number): number {
  if (!Number.isFinite(x)) return 0;
  if (x < 0) return 0;
  if (x > 1) return 1;
  return x;
}

export function fmtInt(n: number | undefined): string {
  if (typeof n !== "number" || !Number.isFinite(n)) return "0";
  return Math.max(0, Math.trunc(n)).toString();
}

/** Short label for **`models[].model`** ids (Coddy profile IDs use displayModeLabel elsewhere). */
export function displayLlmId(id: string, fallback: string = "Model"): string {
  const m = id || "";
  const i = m.lastIndexOf("/");
  if (i >= 0 && i < m.length - 1) {
    return m.slice(i + 1);
  }
  return m || fallback;
}

/** Label for a Coddy profile mode id ("agent"/"plan" localized; other ids shortened). */
export function displayModeLabel(id: string, t: TFn): string {
  const m = id || "agent";
  if (m === "plan") {
    return t("composer.modePlan");
  }
  if (m === "agent") {
    return t("composer.modeAgent");
  }
  if (m === "ask") {
    return t("composer.modeAsk");
  }
  const i = m.lastIndexOf("/");
  if (i >= 0 && i < m.length - 1) {
    return m.slice(i + 1);
  }
  return m;
}
