import { notificationsShown } from "../pwa/NotificationsSetting";
import { MCP_SECTION_ID, type SettingsTextId } from "./settingsSections";

/** A block a Settings tab draws by hand rather than through SchemaForm. */
export type SettingsLandmark = {
  section: string;
  id: string;
  /** The heading and its (i): a key of SETTINGS_TEXTS. */
  heading: SettingsTextId;
  keyPath?: string;
  /** Generated forms are the reorder boundary between the two slots. */
  place: "before" | "after";
  /** The block's own predicate: false where the tab does not draw it. */
  shown?: (doc: Record<string, unknown>) => boolean;
  /** Present for a block of a list's row form; says whether this row draws it. */
  row?: (row: Record<string, unknown>) => boolean;
};

function landmark(
  section: string,
  id: string,
  heading: SettingsTextId,
  place: "before" | "after",
  more: Partial<Pick<SettingsLandmark, "keyPath" | "shown" | "row">> = {},
): SettingsLandmark {
  return { section, id, heading, place, ...more };
}

export function backupCodesShown(doc: Record<string, unknown>): boolean {
  const http = (doc.httpserver ?? {}) as Record<string, unknown>;
  const login = (http.login ?? {}) as Record<string, unknown>;
  return login.totp_configured === true;
}

export const SETTINGS_LANDMARKS: readonly SettingsLandmark[] = [
  landmark("httpserver", "account", "authAccount", "before"),
  landmark("httpserver", "authenticator", "authAuthenticator", "before"),
  landmark("httpserver", "backupCodes", "authBackupCodes", "before", {
    shown: backupCodesShown,
  }),
  landmark("statistics", "enable", "statisticsEnable", "before"),
  landmark("statistics", "timezone", "statisticsCounters", "before"),
  landmark("statistics", "exchange_rate", "statisticsExchangeRate", "before"),
  landmark("statistics", "prices", "statisticsPrices", "before"),
  landmark("appearance", "theme", "appearanceTheme", "before"),
  landmark("appearance", "simple", "appearanceSimple", "before"),
  landmark("appearance", "wallpaper", "appearanceWallpaper", "before"),
  landmark("appearance", "language", "appearanceLanguage", "before"),
  landmark("appearance", "notifications", "appearanceNotifications", "before", {
    shown: notificationsShown,
  }),
  landmark("sessions_manager", "manage", "sessionsManage", "after"),
  // The four blocks the System tab always draws; the charts and the provider
  // rows depend on the build and have no anchor, and no value of the status
  // document is indexed.
  landmark("status", "installation", "systemInstallation", "before"),
  landmark("status", "subsystems", "systemSubsystems", "before"),
  landmark("status", "disk", "systemDisk", "before"),
  landmark("status", "activity", "systemActivity", "before"),
  landmark(MCP_SECTION_ID, "discovery", "mcpDiscovery", "before", {
    keyPath: "mcp.project_trust",
  }),
  landmark(MCP_SECTION_ID, "servers", "mcpServers", "before"),
  landmark("skills", "auto_discovery", "skillsAutoDiscovery", "before", {
    keyPath: "skills.auto_discovery",
  }),
  landmark("skills", "marketplaces", "skillsMarketplaces", "after"),
  landmark("skills", "installed", "skillsInstalled", "after"),
  landmark("subagents", "catalog", "subagentsCatalog", "after"),
  landmark("providers", "models", "providerModels", "after", {
    row: () => true,
  }),
];
