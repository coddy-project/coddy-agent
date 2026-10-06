/**
 * isLoopbackOrigin says whether a serialized origin - the page's own
 * `window.location.origin` - is served from the browser's machine: http or
 * https, a host of `localhost`, a `*.localhost` name, `127.0.0.0/8` or `[::1]`,
 * any port, and nothing else. It is the SPA's twin of the server's test behind
 * `cors.allow_loopback`, used only to choose the hint that names that toggle:
 * the server decides, the hint just says what would admit this page.
 */
export function isLoopbackOrigin(origin: string): boolean {
  let url: URL;
  try {
    url = new URL(origin);
  } catch {
    return false;
  }
  if (url.protocol !== "http:" && url.protocol !== "https:") return false;
  // A serialized origin is scheme, host and an optional port: a path, a
  // query, a fragment, user information or a trailing slash makes it a URL.
  // (The parser's own `origin` cannot be compared against the input, since it
  // drops a default port the input may spell out.)
  if (
    url.username !== "" ||
    url.password !== "" ||
    url.pathname !== "/" ||
    url.search !== "" ||
    url.hash !== "" ||
    origin.endsWith("/")
  ) {
    return false;
  }
  const host = url.hostname.replace(/^\[|\]$/g, "").toLowerCase();
  if (host === "localhost" || host.endsWith(".localhost") || host === "::1") {
    return true;
  }
  return /^127\.\d{1,3}\.\d{1,3}\.\d{1,3}$/.test(host);
}
