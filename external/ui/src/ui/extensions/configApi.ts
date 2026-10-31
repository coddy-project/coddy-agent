/**
 * The configuration API of an extension: read the live document, write one
 * JSON path of it. A write goes through the same transaction a settings save
 * runs under - a minimal write over the file as it is, comments preserved -
 * and needs the `config.write` manifest scope. An enabled extension holds the
 * operator's risk acceptance, so this is ordinary configuration authority,
 * not secret storage: the courtesy below only keeps one extension from
 * overwriting another's `extension_values`.
 */
export type ExtensionConfigAPI = {
  /** The whole document, or one dot path of it (`providers.0.name`, `models.+`
   * addresses nothing to read). */
  get: (path?: string) => Promise<unknown>;
  /** Writes one dot path: an object key, an array index, or `+` to append.
   * Resolves when the configuration has been written and reloaded. */
  set: (path: string, value: unknown) => Promise<void>;
};

const PATH_SEGMENT = /^[A-Za-z0-9_-]+$/;

export function validateConfigPath(path: string, owner: string): boolean {
  if (typeof path !== "string" || !path || path.length > 512) return false;
  const segments = path.split(".");
  if (segments.length > 32) return false;
  for (const segment of segments) {
    if (segment === "+") continue;
    if (!PATH_SEGMENT.test(segment)) return false;
  }
  return !(
    segments[0] === "extension_values" &&
    segments.length > 1 &&
    segments[1] !== owner
  );
}

/** One dot path of a JSON document; `undefined` when it addresses nothing. */
export function configAtPath(doc: unknown, path?: string): unknown {
  if (path === undefined) return doc;
  let current: unknown = doc;
  for (const segment of path.split(".")) {
    if (Array.isArray(current)) {
      const index = Number(segment);
      if (!Number.isInteger(index)) return undefined;
      current = current[index];
    } else if (current && typeof current === "object") {
      current = (current as Record<string, unknown>)[segment];
    } else {
      return undefined;
    }
  }
  return current;
}

export function extensionConfig(
  owner: string,
  sha256: string,
  signal: AbortSignal,
): ExtensionConfigAPI {
  return {
    async get(path) {
      if (signal.aborted) throw new Error("Extension disposed");
      if (path !== undefined && !validateConfigPath(path, owner))
        throw new Error("Invalid configuration path");
      const response = await fetch("/coddy/config", { signal });
      if (!response.ok)
        throw new Error(`Extension config: HTTP ${response.status}`);
      return configAtPath(await response.json(), path);
    },
    async set(path, value) {
      if (signal.aborted) throw new Error("Extension disposed");
      if (!validateConfigPath(path, owner))
        throw new Error("Invalid configuration path");
      let payload: unknown;
      try {
        payload = JSON.parse(JSON.stringify(value ?? null));
      } catch {
        throw new Error("A configuration value must be one JSON value");
      }
      const response = await fetch(
        `/coddy/extensions/${encodeURIComponent(owner)}/config`,
        {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ sha256, path, value: payload }),
          signal,
        },
      );
      if (!response.ok) {
        let reason = `HTTP ${response.status}`;
        try {
          const body = (await response.json()) as { error?: unknown };
          if (body.error) reason = String(body.error);
        } catch {
          /* Status is enough for non-JSON errors. */
        }
        throw new Error(`Extension config: ${reason}`);
      }
    },
  };
}
