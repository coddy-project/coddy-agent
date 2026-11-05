import { useCallback, useEffect, useRef, useState } from "react";

import { copyText } from "../clipboard";
import { FieldLabel } from "./FieldHint";
import type { OverrideTexts } from "./settingsSections";
import { useT } from "../i18n/I18nProvider";
import {
  browserStoreFor,
  newExtensionKey,
  type BrowserStore,
} from "./browserSetup";

/** What GET /coddy/browser/setup answers (external/browser/api.go). */
type Setup = { key_set?: boolean; connected?: string[] };

/** How often the card asks the server while it waits for the extension. */
const WAIT_POLL_MS = 2000;
/** And how often once a browser is on the socket: nothing is being set up. */
const IDLE_POLL_MS = 15000;

/**
 * Connecting a browser, in the place of the bare key field it used to be
 * (Settings -> Browser). The field asked for a key and offered no way to get
 * one and no word about the extension that needs it, so a person who opened
 * the section had nothing to do there.
 *
 * The card is the three steps in order: install the extension (the store of
 * the browser this page runs in first, the other beside it), take the address
 * and the key this form generates, and paste them into the extension's
 * options. It then watches the endpoint and says when a browser arrives, so
 * the last step confirms itself instead of leaving the person to guess.
 *
 * The key is generated in the page and saved with the form, like any other
 * setting: there is no route that writes a credential behind the form's back.
 */
export function BrowserSetupField(props: {
  /** browser.token as the form holds it; "" when the schema has none yet. */
  value: string;
  onChange: (next: string) => void;
  /** The name and description the schema gives the key. */
  texts: OverrideTexts;
  /** The section's switch: nothing connects while the module is off. */
  enabled: boolean;
  /** Overridden by the tests; the page's own address otherwise. */
  fetchImpl?: typeof fetch;
  /** The user agent to pick the store from; the browser's otherwise. */
  userAgent?: string;
}) {
  const { t } = useT();
  const [setup, setSetup] = useState<Setup | null>(null);
  const [copied, setCopied] = useState<"key" | "address" | null>(null);
  const fetchImpl = props.fetchImpl;
  const connected = setup?.connected ?? [];
  // The key the form holds is what the extension has to be given; an empty
  // one is the state the section opened in.
  const key = props.value.trim();
  const address = typeof location === "undefined" ? "" : location.origin;

  const read = useCallback(async () => {
    const call = fetchImpl ?? fetch;
    try {
      const response = await call("/coddy/browser/setup");
      if (!response.ok) return;
      setSetup((await response.json()) as Setup);
    } catch {
      // The section is drawn whether or not the endpoint answers: a server
      // that cannot be asked leaves the card on its steps.
    }
  }, [fetchImpl]);

  // While there is no browser the card is waiting for one, so it asks often;
  // once one is there nothing is being set up and it only keeps the state
  // honest. A key that was just saved is what starts the fast read.
  const waiting = props.enabled && key !== "" && connected.length === 0;
  const waitingRef = useRef(waiting);
  waitingRef.current = waiting;
  useEffect(() => {
    let timer = 0;
    let stopped = false;
    const tick = async () => {
      await read();
      if (stopped) return;
      timer = window.setTimeout(
        tick,
        waitingRef.current ? WAIT_POLL_MS : IDLE_POLL_MS,
      );
    };
    void tick();
    return () => {
      stopped = true;
      if (timer) window.clearTimeout(timer);
    };
  }, [read]);

  // Through the app's one copy (ui/clipboard.ts): a page served over plain
  // HTTP from a LAN address has no navigator.clipboard, and connecting a
  // browser to a server on another machine is exactly that page.
  const copy = async (what: "key" | "address", text: string) => {
    if (!(await copyText(text))) return;
    setCopied(what);
    window.setTimeout(() => setCopied(null), 1500);
  };

  const stores = browserStoreFor(
    props.userAgent ??
      (typeof navigator === "undefined" ? "" : navigator.userAgent),
  );

  return (
    <div className="settings-row browser-setup" data-testid="browser-setup">
      <FieldLabel
        label={props.texts.primary}
        description={props.texts.primaryDesc}
      />

      <ol className="browser-setup-steps">
        <li className="browser-setup-step">
          <span className="browser-setup-step-text">
            {t("browserSetup.step.install")}
          </span>
          <span className="browser-setup-stores">
            {stores.map((store) => (
              <a
                key={store.id}
                className={`settings-btn browser-setup-store${store.suggested ? " browser-setup-store--suggested" : ""}`}
                href={store.url}
                target="_blank"
                rel="noreferrer"
                data-testid={`browser-store-${store.id}`}
              >
                <StoreMark id={store.id} />
                {t(store.labelKey)}
              </a>
            ))}
          </span>
        </li>

        <li className="browser-setup-step">
          <span className="browser-setup-step-text">
            {t("browserSetup.step.key")}
          </span>
          <span className="browser-setup-pair">
            <input
              className="settings-input browser-setup-value"
              readOnly
              value={address}
              aria-label={t("browserSetup.addressLabel")}
              data-testid="browser-setup-address"
            />
            <button
              type="button"
              className="settings-btn"
              onClick={() => void copy("address", address)}
            >
              {copied === "address"
                ? t("browserSetup.copied")
                : t("browserSetup.copy")}
            </button>
          </span>
          <span className="browser-setup-pair">
            <input
              className="settings-input browser-setup-value"
              readOnly
              value={key}
              // The key is write-only, so a saved one never comes back to the
              // form: the placeholder then says the server holds one rather
              // than that there is none.
              placeholder={t(
                setup?.key_set === true
                  ? "browserSetup.keyKept"
                  : "browserSetup.keyEmpty",
              )}
              aria-label={props.texts.primary}
              data-testid="browser-setup-key"
            />
            <button
              type="button"
              className="settings-btn"
              onClick={() => props.onChange(newExtensionKey())}
              data-testid="browser-setup-generate"
            >
              {key === "" && setup?.key_set !== true
                ? t("browserSetup.generate")
                : t("browserSetup.regenerate")}
            </button>
            {key === "" ? null : (
              <button
                type="button"
                className="settings-btn"
                onClick={() => void copy("key", key)}
                data-testid="browser-setup-copy-key"
              >
                {copied === "key"
                  ? t("browserSetup.copied")
                  : t("browserSetup.copy")}
              </button>
            )}
          </span>
        </li>

        <li className="browser-setup-step">
          <span className="browser-setup-step-text">
            {t("browserSetup.step.paste")}
          </span>
          <BrowserSetupState
            enabled={props.enabled}
            keySet={key !== ""}
            saved={setup?.key_set === true}
            connected={connected}
          />
        </li>
      </ol>
    </div>
  );
}

/** The last line of the card: what the endpoint says right now. */
function BrowserSetupState(props: {
  enabled: boolean;
  keySet: boolean;
  saved: boolean;
  connected: string[];
}) {
  const { t, tp } = useT();
  if (props.connected.length > 0) {
    return (
      <span
        className="browser-setup-state browser-setup-state--ready"
        data-testid="browser-setup-state"
      >
        {tp("browserSetup.connected", props.connected.length, {
          names: props.connected.join(", "),
        })}
      </span>
    );
  }
  // The key is write-only, so the form holds one only while it is new: what
  // the server says it has is the other half of the answer. A key in the form
  // the server has not got yet is one step from done, and a key only the
  // server has is one the extension still needs.
  const message = !props.enabled
    ? "browserSetup.stateOff"
    : !props.keySet && !props.saved
      ? "browserSetup.stateNoKey"
      : props.keySet && !props.saved
        ? "browserSetup.stateUnsaved"
        : "browserSetup.stateWaiting";
  return (
    <span className="browser-setup-state" data-testid="browser-setup-state">
      {t(message)}
    </span>
  );
}

/** The store's own mark, so the two buttons are told apart at a glance. */
function StoreMark({ id }: { id: BrowserStore["id"] }) {
  if (id === "firefox") {
    return (
      <svg
        className="browser-setup-store-mark"
        viewBox="0 0 16 16"
        width="15"
        height="15"
        aria-hidden="true"
      >
        <circle cx="8" cy="8.6" r="6.1" fill="#ff7139" />
        <path
          d="M8 2.6c1.1 1 1.3 2.2 1 3-.4 1-1.6 1.2-2.2 2-.5.7-.4 1.7.3 2.3.8.7 2 .6 2.7-.2.6-.7.6-1.6.2-2.2.9.4 1.5 1.4 1.5 2.5A3.9 3.9 0 0 1 8 13.7 4.6 4.6 0 0 1 3.4 9c0-2.4 1.3-3.6 2.4-4.4.9-.6 1.6-1.2 2.2-2Z"
          fill="#ffbd4f"
        />
      </svg>
    );
  }
  return (
    <svg
      className="browser-setup-store-mark"
      viewBox="0 0 16 16"
      width="15"
      height="15"
      aria-hidden="true"
    >
      <circle
        cx="8"
        cy="8"
        r="6.4"
        fill="none"
        stroke="#4285f4"
        strokeWidth="3"
      />
      <circle cx="8" cy="8" r="2.6" fill="#4285f4" />
    </svg>
  );
}
