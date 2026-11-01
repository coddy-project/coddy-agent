import {
  parseAppHash,
  setHistoryHash,
  setSchedulerListHash,
  setSessionHashInLocation,
  setSettingsHash,
} from "../scheduler/hashRoute";

/**
 * The navigation API of an extension: what the address bar already knows.
 * Opening a screen is a hash change - the host's rail screens answer it the
 * way they answer a click - and `currentRoute` reports where the app is.
 */
export type NavScreen = "home" | "history" | "settings" | "scheduler";
export type NavRoute = Readonly<{
  branch: string;
  sessionId: string | null;
}>;
export type ExtensionNavAPI = {
  currentRoute: () => NavRoute;
  /** Opens the conversation in the chat screen. */
  openSession: (sessionId: string) => void;
  /** Opens one of the rail screens: `home`, `history`, `settings`,
   * `scheduler`. */
  openScreen: (screen: NavScreen) => void;
};

export function extensionNav(): ExtensionNavAPI {
  return {
    currentRoute() {
      const route = parseAppHash();
      return Object.freeze({
        branch: route.branch,
        sessionId: route.branch === "session" ? route.sessionId : null,
      });
    },
    openSession(sessionId) {
      if (!/^[A-Za-z0-9_-]+$/.test(sessionId))
        throw new Error("Invalid session id");
      setSessionHashInLocation(sessionId);
    },
    openScreen(screen) {
      switch (screen) {
        case "home":
          setSessionHashInLocation("");
          return;
        case "history":
          setHistoryHash();
          return;
        case "settings":
          setSettingsHash();
          return;
        case "scheduler":
          setSchedulerListHash();
          return;
        default:
          throw new Error("Unknown screen: " + String(screen));
      }
    },
  };
}
