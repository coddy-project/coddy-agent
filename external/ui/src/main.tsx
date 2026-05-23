import React from "react";
import ReactDOM from "react-dom/client";
import "./styles.css";
import { App } from "./ui/App";
import { bootstrapUiThemeFromCookie } from "./ui/theme/uiTheme";

bootstrapUiThemeFromCookie();

ReactDOM.createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>,
);
