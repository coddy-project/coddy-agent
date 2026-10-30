/**
 * The reserved host channel. Today it reports which manifest permissions this
 * build honours; the sessions/messages host API of the specification lands on
 * the same object, so an extension written against it keeps working.
 */
export type ExtensionHostAPI = {
  api: 1;
  /** Names of the manifest permissions this host build honours. */
  capabilities: () => Promise<readonly string[]>;
};

/** Permissions this phase understands. `ui.page` is implied by enablement. */
export const HONORED_PERMISSIONS: readonly string[] = ["ui.page"];

export function extensionHost(): ExtensionHostAPI {
  return {
    api: 1,
    capabilities: async () => HONORED_PERMISSIONS,
  };
}
