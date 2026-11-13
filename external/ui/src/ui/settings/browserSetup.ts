/**
 * Where Coddy for Browser is published, and the key the extension proves
 * itself with. Pure, so the settings card and its tests share one answer.
 */

export const CHROME_WEB_STORE_URL =
  "https://chromewebstore.google.com/detail/coddy-for-browser/pdppgelbaehcedjpgfonemkmglpjifni";
export const FIREFOX_ADDONS_URL =
  "https://addons.mozilla.org/firefox/addon/coddy-for-browser/";

export type BrowserStore = {
  id: "chrome" | "firefox";
  url: string;
  labelKey: string;
  /** The store of the browser this page runs in; it leads the row. */
  suggested: boolean;
};

/**
 * The two stores, the one that serves this browser first. Firefox is the
 * only engine with a store of its own here; every Chromium browser - Chrome,
 * Yandex Browser, Edge, Brave - installs from the Chrome Web Store, so a
 * user agent that is not Firefox leads with it.
 */
export function browserStoreFor(userAgent: string): BrowserStore[] {
  const firefox = /firefox|fxios/i.test(userAgent);
  const chrome: BrowserStore = {
    id: "chrome",
    url: CHROME_WEB_STORE_URL,
    labelKey: "browserSetup.store.chrome",
    suggested: !firefox,
  };
  const fox: BrowserStore = {
    id: "firefox",
    url: FIREFOX_ADDONS_URL,
    labelKey: "browserSetup.store.firefox",
    suggested: firefox,
  };
  return firefox ? [fox, chrome] : [chrome, fox];
}

/** The alphabet of a generated key: no look-alike pair to mistype. */
const KEY_ALPHABET =
  "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789";
/** The schema asks for at least 32 characters; 48 is the generated length. */
export const EXTENSION_KEY_LENGTH = 48;

/**
 * A key for the extension, drawn from the platform's cryptographic random
 * source. The bytes are rejected rather than folded when they fall past the
 * last whole run of the alphabet, so every character is equally likely.
 */
export function newExtensionKey(
  random: (out: Uint8Array<ArrayBuffer>) => void = (out) => {
    crypto.getRandomValues(out);
  },
): string {
  const limit = 256 - (256 % KEY_ALPHABET.length);
  let key = "";
  const buffer = new Uint8Array(new ArrayBuffer(EXTENSION_KEY_LENGTH));
  while (key.length < EXTENSION_KEY_LENGTH) {
    random(buffer);
    for (const byte of buffer) {
      if (byte >= limit) continue;
      key += KEY_ALPHABET[byte % KEY_ALPHABET.length];
      if (key.length === EXTENSION_KEY_LENGTH) break;
    }
  }
  return key;
}
