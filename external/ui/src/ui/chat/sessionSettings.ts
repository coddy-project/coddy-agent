/**
 * The settings snapshot of a session (`internal/session/settings.go`): what the
 * composer's selectors mirror. The server publishes it whole, with a version,
 * whenever any surface changes a setting - a command, the permission dialog,
 * the model's own `switch_model`, a console, an editor - on the turn stream
 * and on `GET /coddy/events` (`event: session_settings`), and returns it from
 * `GET /coddy/sessions/{id}/messages` and `PATCH /coddy/sessions/{id}`.
 */

/** A setting changed for a number of turns (`--once`, `--count=N`). */
export type TurnOverride = {
  setting: string;
  value: string;
  /** Turns that have not started yet. */
  turnsLeft: number;
  /** The running turn holds this value. */
  active?: boolean;
};

export type SessionSettings = {
  sessionId: string;
  version: number;
  model: string;
  reasoning: string;
  reasoningChoices: string[];
  mode: string;
  permissionMode: string;
  configuredPermissionMode: string;
  overrides: TurnOverride[];
};

export const PERMISSION_MODES = ["ask", "accept_edits", "bypass"] as const;

function str(v: unknown): string {
  return typeof v === "string" ? v.trim() : "";
}

/** parseSessionSettings reads a snapshot off a JSON value; null when it is not one. */
export function parseSessionSettings(raw: unknown): SessionSettings | null {
  if (!raw || typeof raw !== "object") {
    return null;
  }
  const o = raw as Record<string, unknown>;
  const sessionId = str(o.sessionId);
  if (!sessionId) {
    return null;
  }
  const overrides = Array.isArray(o.overrides)
    ? o.overrides
        .map((r) => r as Record<string, unknown>)
        .filter((r) => r && str(r.setting) && str(r.value))
        .map((r) => ({
          setting: str(r.setting),
          value: str(r.value),
          turnsLeft: typeof r.turnsLeft === "number" ? r.turnsLeft : 0,
          active: r.active === true,
        }))
    : [];
  return {
    sessionId,
    version: typeof o.version === "number" ? o.version : 0,
    model: str(o.model),
    reasoning: str(o.reasoning),
    reasoningChoices: Array.isArray(o.reasoningChoices)
      ? o.reasoningChoices.filter((v): v is string => typeof v === "string")
      : [],
    mode: str(o.mode) || "agent",
    permissionMode: str(o.permissionMode) || "ask",
    configuredPermissionMode: str(o.configuredPermissionMode) || "ask",
    overrides,
  };
}

/** The payload of `event: session_settings`. */
export type SessionSettingsEvent = {
  sessionId: string;
  settings: SessionSettings;
  notice: string;
  source: string;
};

/** sessionSettingsEventOf parses the data of an `event: session_settings` frame. */
export function sessionSettingsEventOf(
  data: string,
): SessionSettingsEvent | null {
  try {
    const parsed = JSON.parse(data) as Record<string, unknown>;
    // The events stream wraps the snapshot; the turn stream carries the ACP
    // update, whose snapshot sits under the same key.
    const settings = parseSessionSettings(parsed.settings);
    if (!settings) {
      return null;
    }
    return {
      sessionId: str(parsed.sessionId) || settings.sessionId,
      settings,
      notice: str(parsed.notice),
      source: str(parsed.source),
    };
  } catch {
    return null;
  }
}

/**
 * isNewerSettings reports whether next should replace what a view holds: the
 * same session and a higher version. The same change reaches a tab down two
 * connections, and a snapshot a request answered can arrive after the event
 * of a later change.
 */
export function isNewerSettings(
  heldVersion: number,
  viewedSessionId: string,
  next: SessionSettings,
): boolean {
  if (!viewedSessionId || next.sessionId !== viewedSessionId) {
    return false;
  }
  return next.version > heldVersion;
}
