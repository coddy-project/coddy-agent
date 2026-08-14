import React from "react";
import ReactDOM from "react-dom/client";
import "./styles.css";
import { App } from "./ui/App";
import { ConfirmProvider } from "./ui/components/useConfirm";
import { bootstrapUiThemeFromCookie } from "./ui/theme/uiTheme";
import { bootstrapUiLocaleFromUrlOrCookie } from "./ui/i18n/uiLocale";
import { initLocale } from "./ui/i18n/i18n";
import { I18nProvider } from "./ui/i18n/I18nProvider";
import { installRemoteFetchShim } from "./ui/env/remoteEnv";
import { startActiveHealthMonitor } from "./ui/env/activeHealth";

// Route API calls to the selected remote environment (no-op in local mode). Must run before the
// app issues any fetch so remote sessions/config/streaming all target the chosen backend.
installRemoteFetchShim();
// Begin probing the active environment's reachability (issue #60) so a dead remote is visible.
startActiveHealthMonitor();
bootstrapUiThemeFromCookie();
initLocale(bootstrapUiLocaleFromUrlOrCookie());

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <I18nProvider>
      <ConfirmProvider>
        <App />
      </ConfirmProvider>
    </I18nProvider>
  </React.StrictMode>,
);
