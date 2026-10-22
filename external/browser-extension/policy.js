// Access policy lives in the extension, outside pages and the model's control.
export function siteOf(address) {
  const url = new URL(address);
  if (
    !["http:", "https:"].includes(url.protocol) ||
    url.username ||
    url.password
  )
    throw new Error("Only ordinary HTTP(S) pages are supported");
  return url.origin;
}

export function serverURL(address) {
  const url = new URL(address);
  if (
    !["http:", "https:"].includes(url.protocol) ||
    url.username ||
    url.password ||
    url.search ||
    url.hash
  )
    throw new Error(
      "Enter an HTTP(S) server address without credentials or query",
    );
  if (
    url.protocol === "http:" &&
    !["localhost", "127.0.0.1", "[::1]"].includes(url.hostname)
  )
    throw new Error("Use HTTPS for a remote Coddy server");
  if (url.pathname !== "/")
    throw new Error("Enter the browser listener address without a path");
  return url.origin;
}

export function canRun(shared, tabID, currentURL, allowed) {
  if (!shared.includes(tabID))
    throw new Error("Tab is not shared; select it in the extension");
  return allowed.includes(siteOf(currentURL));
}
