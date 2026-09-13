import { useEffect, useSyncExternalStore } from "react";
import { getEnv } from "../env/remoteEnv";
import {
  installAuthUnauthorizedWatch,
  refreshAuthState,
  snapshotAuth,
  subscribeAuth,
} from "./authState";
import { SignInScreen } from "./SignInScreen";

/**
 * AuthGate decides between the sign-in screen and the app.
 *
 * It asks the server once on boot and keeps listening, so a session that ends
 * while the page is open - expired, rotated, signed out in another tab - brings
 * the form back instead of leaving empty lists over a row of 401s.
 *
 * A remote environment is not gated here: it is reached cross-origin with a
 * bearer token the user gave in the environment selector, and a cookie from
 * this origin would never travel with those calls anyway.
 */
export function AuthGate(props: { children: React.ReactNode }) {
  const state = useSyncExternalStore(subscribeAuth, snapshotAuth, snapshotAuth);
  const local = getEnv().mode === "local";

  useEffect(() => {
    if (!local) {
      return undefined;
    }
    void refreshAuthState();
    return installAuthUnauthorizedWatch();
  }, [local]);

  if (!local) {
    return <>{props.children}</>;
  }
  if (!state.loaded) {
    // One round trip, not a spinner: rendering the app and then replacing it
    // with a sign-in screen reads as a glitch, and a spinner for 20 ms reads as
    // a slow page.
    return <div className="auth-boot" aria-hidden />;
  }
  if (state.loginRequired && !state.authenticated) {
    return <SignInScreen />;
  }
  return <>{props.children}</>;
}
