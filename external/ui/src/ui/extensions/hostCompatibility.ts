export const EXTENSION_FEATURES = Object.freeze([
  "ui.pages",
  "composer.controls",
  "composer.reset",
  "settings.fields",
  "settings.blocks",
  "settings.rename.row",
  "stream.raw",
  "stream.text",
  "sessions.normalized",
  "providers.read",
  "providers.upsert",
  "config.write",
  "theme.tokens",
  "theme.color",
  "experimental.alter",
  "hooks.invoke",
  "network.proxy",
] as const);

export type ExtensionHostInfo = Readonly<{
  sdk_version: 1;
  coddy_version: string;
  features: readonly string[];
}>;

export type ExtensionCompatibilityDeclaration = Readonly<{
  required_features?: readonly string[];
  engines?: Readonly<{ coddy?: string }>;
}>;

export async function fetchExtensionHostInfo(
  request: (input: string, init?: RequestInit) => Promise<Response> = fetch,
): Promise<ExtensionHostInfo> {
  const response = await request("/coddy/info");
  if (!response.ok)
    throw new Error(`Host compatibility: HTTP ${response.status}`);
  const body = (await response.json()) as { version?: unknown };
  if (typeof body.version !== "string" || !body.version.trim()) {
    throw new Error("Host compatibility: the server reported no version");
  }
  return Object.freeze({
    sdk_version: 1 as const,
    coddy_version: body.version,
    features: Object.freeze([...EXTENSION_FEATURES]),
  });
}

export function validateExtensionCompatibility(
  declaration: ExtensionCompatibilityDeclaration,
  host: ExtensionHostInfo,
): void {
  const missing = (declaration.required_features ?? []).filter(
    (feature) => !host.features.includes(feature),
  );
  if (missing.length) {
    throw new Error(
      `Host lacks required extension features: ${missing.join(", ")}`,
    );
  }
  const range = declaration.engines?.coddy;
  if (range !== undefined && !versionInRange(host.coddy_version, range)) {
    throw new Error(
      `Extension requires Coddy ${range}; this host is ${host.coddy_version}`,
    );
  }
}

export function versionInRange(version: string, range: string): boolean {
  const terms = range.trim().split(/\s+/).filter(Boolean);
  if (!terms.length) throw new Error("Invalid engines.coddy range");
  for (const term of terms) {
    const match = /^(>=|<=|>|<|=)?(v?\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?)$/.exec(
      term,
    );
    if (!match) throw new Error(`Invalid engines.coddy range: ${range}`);
    const operator = match[1] ?? "=";
    const comparison = compareVersions(version, match[2]!);
    if (
      (operator === ">=" && comparison < 0) ||
      (operator === "<=" && comparison > 0) ||
      (operator === ">" && comparison <= 0) ||
      (operator === "<" && comparison >= 0) ||
      (operator === "=" && comparison !== 0)
    ) {
      return false;
    }
  }
  return true;
}

export function compareVersions(left: string, right: string): number {
  const a = parseVersion(left);
  const b = parseVersion(right);
  for (let i = 0; i < 3; i++) {
    if (a.core[i] !== b.core[i]) return a.core[i]! < b.core[i]! ? -1 : 1;
  }
  if (a.pre === b.pre) return 0;
  if (!a.pre) return 1;
  if (!b.pre) return -1;
  const aa = a.pre.split(".");
  const bb = b.pre.split(".");
  for (let i = 0; i < Math.max(aa.length, bb.length); i++) {
    if (aa[i] === undefined) return -1;
    if (bb[i] === undefined) return 1;
    if (aa[i] === bb[i]) continue;
    const an = /^\d+$/.test(aa[i]!);
    const bn = /^\d+$/.test(bb[i]!);
    if (an && bn) return Number(aa[i]) < Number(bb[i]) ? -1 : 1;
    if (an !== bn) return an ? -1 : 1;
    return aa[i]! < bb[i]! ? -1 : 1;
  }
  return 0;
}

function parseVersion(value: string) {
  const match =
    /^v?(\d+)\.(\d+)\.(\d+)(?:-([0-9A-Za-z.-]+))?(?:\+[0-9A-Za-z.-]+)?$/.exec(
      value.trim(),
    );
  if (!match) throw new Error(`Invalid Coddy version: ${value}`);
  return {
    core: [Number(match[1]), Number(match[2]), Number(match[3])],
    pre: match[4] ?? "",
  };
}
