/**
 * The stand behind `scripts/composer-caret-check.mjs`.
 *
 * The composer's textarea paints no text of its own: it keeps the caret and the
 * selection, and a mirror under it draws the draft with its chips. Whether the
 * two put a glyph on the same pixel is a layout fact, so jsdom cannot answer it.
 * This page mounts the real Composer against the real stylesheet with no backend
 * behind it, in the chat column's width: `?docked=1` gives the two-row field of a
 * chat that has messages, the default the five-row field of a new chat.
 */
import { useState } from "react";
import ReactDOM from "react-dom/client";
import "./styles.css";
import { Composer } from "./ui/chat/Composer";
import { I18nProvider } from "./ui/i18n/I18nProvider";
import { initLocale } from "./ui/i18n/i18n";
import { UI_LOCALE_DEFAULT } from "./ui/i18n/locales";

initLocale(UI_LOCALE_DEFAULT);

const docked = new URLSearchParams(location.search).get("docked") === "1";

function Fixture() {
  const [value, setValue] = useState("");
  return (
    // The chat column: 14px off either edge on the stacked shell, a centred
    // 920px stripe on the desktop.
    <div
      style={{
        boxSizing: "border-box",
        width: "100%",
        maxWidth: 920,
        margin: "40px auto 0",
        padding: "0 14px",
      }}
    >
      <Composer
        value={value}
        isEmpty={!docked}
        mode="agent"
        modes={["agent", "plan", "ask"]}
        onModeChange={() => {}}
        onChange={setValue}
        onSend={() => {}}
      />
    </div>
  );
}

ReactDOM.createRoot(document.getElementById("root")!).render(
  <I18nProvider>
    <Fixture />
  </I18nProvider>,
);
