import React from "react";
import ReactDOM from "react-dom/client";
import "./styles.css";
import { App } from "./ui/App";
import { bootstrapUiThemeFromCookie } from "./ui/theme/uiTheme";
import { installRemoteFetchShim } from "./ui/env/remoteEnv";
import { startActiveHealthMonitor } from "./ui/env/activeHealth";

// Route API calls to the selected remote environment (no-op in local mode). Must run before the
// app issues any fetch so remote sessions/config/streaming all target the chosen backend.
installRemoteFetchShim();
// Begin probing the active environment's reachability (issue #60) so a dead remote is visible.
startActiveHealthMonitor();
bootstrapUiThemeFromCookie();

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
);
