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
  // query (an empty `?` leaves `search` empty, so the character itself is
  // checked), a fragment, user information, a trailing slash or port 0 makes
  // it a URL rather than an origin. (The parser's own `origin` cannot be
  // compared against the input, since it drops a default port the input may
  // spell out.)
  if (
    url.username !== "" ||
    url.password !== "" ||
    url.pathname !== "/" ||
    url.search !== "" ||
    url.hash !== "" ||
    url.port === "0" ||
    origin.endsWith("/") ||
    origin.includes("?") ||
    origin.includes("#")
  ) {
    return false;
  }
  const host = url.hostname.replace(/^\[|\]$/g, "").toLowerCase();
  if (host === "localhost" || host.endsWith(".localhost") || host === "::1") {
    return true;
  }
  return /^127\.\d{1,3}\.\d{1,3}\.\d{1,3}$/.test(host);
}
