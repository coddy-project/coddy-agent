// First: it reads Telegram's launch parameters out of the address before
// anything else can see or rewrite the fragment (ui/telegram/launch.ts).
import "./ui/telegram/capture";
import React from "react";
import ReactDOM from "react-dom/client";
import "./styles.css";
import { App } from "./ui/App";
import { ConfirmProvider } from "./ui/components/useConfirm";
import { ImageMenuHost } from "./ui/components/ImageMenu";
import { bootstrapUiThemeFromCookie } from "./ui/theme/uiTheme";
import { bootstrapUiLocaleFromUrlOrCookie } from "./ui/i18n/uiLocale";
import { initLocale } from "./ui/i18n/i18n";
import { I18nProvider } from "./ui/i18n/I18nProvider";
import { installRemoteFetchShim } from "./ui/env/remoteEnv";
import { AuthGate } from "./ui/auth/AuthGate";
import { EnvScope } from "./ui/env/EnvScope";
import { startActiveHealthMonitor } from "./ui/env/activeHealth";
import { initTelegramMiniApp } from "./ui/telegram/miniApp";
import { registerServiceWorker } from "./ui/pwa/serviceWorker";
import { followThemeColor } from "./ui/pwa/themeColor";

// Route API calls to the selected remote environment (no-op in local mode). Must run before the
// app issues any fetch so remote sessions/config/streaming all target the chosen backend.
installRemoteFetchShim();
// Begin probing the active environment's reachability (issue #60) so a dead remote is visible.
startActiveHealthMonitor();
bootstrapUiThemeFromCookie();
// After the theme: inside Telegram the theme follows Telegram's until the user
// picks one. Outside Telegram this does nothing.
initTelegramMiniApp();
initLocale(bootstrapUiLocaleFromUrlOrCookie());
// The installed app's title bar takes the theme's colour (ui/pwa/themeColor.ts).
followThemeColor();

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <I18nProvider>
      <ConfirmProvider>
        <AuthGate>
          <EnvScope>
            <App />
          </EnvScope>
        </AuthGate>
        <ImageMenuHost />
      </ConfirmProvider>
    </I18nProvider>
  </React.StrictMode>,
);

// Installable app and notifications (ui/pwa/): the service worker caches
// nothing, so it is registered once the page is up and never stands between
// the page and the server.
registerServiceWorker();
