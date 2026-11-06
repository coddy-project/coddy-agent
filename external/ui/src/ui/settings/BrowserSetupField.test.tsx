import { afterEach, expect, test, vi } from "vitest";
import {
  cleanup,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";

import { BrowserSetupField } from "./BrowserSetupField";
import {
  browserStoreFor,
  CHROME_WEB_STORE_URL,
  EXTENSION_KEY_LENGTH,
  FIREFOX_ADDONS_URL,
  newExtensionKey,
} from "./browserSetup";
import { I18nProvider } from "../i18n/I18nProvider";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

const texts = { primary: "Extension key", primaryDesc: "" };

function setupAnswer(body: unknown): typeof fetch {
  return vi.fn(async () =>
    Promise.resolve(new Response(JSON.stringify(body), { status: 200 })),
  ) as unknown as typeof fetch;
}

test("the store of this browser leads the row and both are offered", () => {
  const chrome = browserStoreFor("Mozilla/5.0 Chrome/140 Safari/537.36");
  expect(chrome.map((s) => s.id)).toEqual(["chrome", "firefox"]);
  expect(chrome[0]!.suggested).toBe(true);
  expect(chrome[0]!.url).toBe(CHROME_WEB_STORE_URL);
  const firefox = browserStoreFor("Mozilla/5.0 Firefox/141.0");
  expect(firefox.map((s) => s.id)).toEqual(["firefox", "chrome"]);
  expect(firefox[0]!.url).toBe(FIREFOX_ADDONS_URL);
  // Every Chromium browser installs from the Chrome Web Store.
  for (const ua of ["YaBrowser/24.1", "Edg/140", "Brave Chrome/140"]) {
    expect(browserStoreFor(ua)[0]!.id).toBe("chrome");
  }
});

test("a generated key is long enough for the schema and free of look-alikes", () => {
  const key = newExtensionKey();
  expect(key).toHaveLength(EXTENSION_KEY_LENGTH);
  expect(key.length).toBeGreaterThanOrEqual(32);
  expect(key).toMatch(/^[a-zA-Z2-9]+$/);
  // The pairs a person retypes wrongly are out of the alphabet.
  expect(key).not.toMatch(/[lIO01]/);
  expect(newExtensionKey()).not.toBe(key);
});

// Regression: the section drew the key as a bare text field - "Not set" and
// nothing else - so a person who opened it had no way to get a key and no
// word about the extension that needs one.
test("the card offers both stores and generates the key the extension needs", async () => {
  const onChange = vi.fn();
  render(
    <I18nProvider>
      <BrowserSetupField
        value=""
        onChange={onChange}
        texts={texts}
        enabled
        fetchImpl={setupAnswer({ key_set: false, connected: [] })}
        userAgent="Mozilla/5.0 Chrome/140 Safari/537.36"
      />
    </I18nProvider>,
  );
  expect(screen.getByTestId("browser-store-chrome")).toHaveAttribute(
    "href",
    CHROME_WEB_STORE_URL,
  );
  expect(screen.getByTestId("browser-store-firefox")).toHaveAttribute(
    "href",
    FIREFOX_ADDONS_URL,
  );
  expect(screen.getByTestId("browser-setup-generate")).toHaveAttribute(
    "title",
    "Generate",
  );
  screen.getByTestId("browser-setup-generate").click();
  expect(onChange).toHaveBeenCalledTimes(1);
  expect(String(onChange.mock.calls[0]![0])).toHaveLength(EXTENSION_KEY_LENGTH);
});

test("the card says what is missing, then that the browser arrived", async () => {
  const { rerender } = render(
    <I18nProvider>
      <BrowserSetupField
        value=""
        onChange={() => {}}
        texts={texts}
        enabled
        fetchImpl={setupAnswer({ key_set: false, connected: [] })}
      />
    </I18nProvider>,
  );
  await waitFor(() =>
    expect(screen.getByTestId("browser-setup-state")).toHaveTextContent(
      "Generate a key",
    ),
  );
  // A key in the form the server has not been given yet: the step is to save.
  rerender(
    <I18nProvider>
      <BrowserSetupField
        value={"k".repeat(48)}
        onChange={() => {}}
        texts={texts}
        enabled
        fetchImpl={setupAnswer({ key_set: false, connected: [] })}
      />
    </I18nProvider>,
  );
  await waitFor(() =>
    expect(screen.getByTestId("browser-setup-state")).toHaveTextContent(
      "Save the settings",
    ),
  );
  rerender(
    <I18nProvider>
      <BrowserSetupField
        value={"k".repeat(48)}
        onChange={() => {}}
        texts={texts}
        enabled
        fetchImpl={setupAnswer({
          key_set: true,
          connected: [
            { id: "b1", name: "Chrome on dev", version: "142.0" },
            { id: "b2", name: "Firefox at home" },
          ],
        })}
      />
    </I18nProvider>,
  );
  await waitFor(() =>
    expect(screen.getByTestId("browser-setup-state")).toHaveTextContent(
      "2 browsers are connected",
    ),
  );
  // The name is what a person types into the extension's options, so the card
  // names the browsers rather than only counting them; a version the
  // extension gave follows its name.
  const rows = within(screen.getByTestId("browser-setup-list")).getAllByRole(
    "listitem",
  );
  expect(rows.map((row) => row.textContent)).toEqual([
    "Chrome on dev142.0",
    "Firefox at home",
  ]);
});

// The key is write-only: a saved one never comes back to the form, so the
// card has to read "the server has a key" from the endpoint or it would send
// the person back to generate one on every visit.
test("a key the server already holds is not asked for again", async () => {
  render(
    <I18nProvider>
      <BrowserSetupField
        value=""
        onChange={() => {}}
        texts={texts}
        enabled
        fetchImpl={setupAnswer({ key_set: true, connected: [] })}
      />
    </I18nProvider>,
  );
  await waitFor(() =>
    expect(screen.getByTestId("browser-setup-state")).toHaveTextContent(
      "Waiting for the extension",
    ),
  );
  expect(screen.getByTestId("browser-setup-key")).toHaveAttribute(
    "placeholder",
    "The server holds a key",
  );
  // The control is an icon now: its word is the tooltip and the name.
  expect(screen.getByTestId("browser-setup-generate")).toHaveAttribute(
    "title",
    "Generate a new one",
  );
});

test("a section that is switched off says so instead of waiting", async () => {
  render(
    <I18nProvider>
      <BrowserSetupField
        value={"k".repeat(48)}
        onChange={() => {}}
        texts={texts}
        enabled={false}
        fetchImpl={setupAnswer({ key_set: true, connected: [] })}
      />
    </I18nProvider>,
  );
  await waitFor(() =>
    expect(screen.getByTestId("browser-setup-state")).toHaveTextContent(
      "switched off",
    ),
  );
});
