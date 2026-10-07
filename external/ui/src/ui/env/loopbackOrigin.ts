// The shape of a serialized origin: http or https, a host and an optional port,
// and nothing else - no path, query, fragment, user information, trailing slash
// or empty port. The host is an IPv6 literal in brackets or a name written with
// the characters of a host name: letters, digits, hyphen, underscore and dot.
// A name that is not loopback is refused below; the class only keeps out what a
// parser would clean up or tolerate in the input.
const ORIGIN_SHAPE =
  /^https?:\/\/(\[[0-9a-f:.]+\]|[a-z0-9._-]+)(?::([0-9]+))?$/i;

const OCTET = "(?:25[0-5]|2[0-4]\\d|1\\d\\d|[1-9]?\\d)";
// 127.0.0.0/8 in dotted form, every octet canonical: no short form, no leading zeros.
const IPV4_LOOPBACK = new RegExp(`^127\\.${OCTET}\\.${OCTET}\\.${OCTET}$`);

function isIPv6Loopback(bracketed: string): boolean {
  // ::1 in any spelling of it; the parser writes them all as [::1]. An
  // IPv4-mapped address ([::ffff:127.0.0.1]) is a different address and stays out.
  try {
    return new URL(`http://${bracketed}`).hostname === "[::1]";
  } catch {
    return false;
  }
}

/**
 * isLoopbackOrigin says whether a serialized origin - the page's own
 * `window.location.origin` - is served from the browser's machine: http or
 * https, a host of `localhost`, a `*.localhost` name, `127.0.0.0/8` in dotted
 * form or `::1`, any port, and nothing else. It is the SPA's twin of the
 * server's test behind `cors.allow_loopback` (`isLoopbackOrigin` in
 * internal/config/http.go), used only to choose the hint that names that
 * toggle: the server decides, the hint just says what would admit this page.
 * Both are held to internal/config/testdata/loopback_origin_cases.json.
 *
 * The server judges the text it was sent, so this reads the text too. A URL
 * parser cleans its input up - it drops a tab, folds full-width letters, reads
 * `127.1` as 127.0.0.1, takes `http:\\localhost` for `http://localhost` - and
 * would call loopback what the server refuses.
 */
export function isLoopbackOrigin(origin: string): boolean {
  const m = ORIGIN_SHAPE.exec(origin);
  const rawHost = m?.[1];
  if (rawHost === undefined) return false;
  const port = m?.[2];
  if (port !== undefined) {
    const n = Number(port);
    if (n < 1 || n > 65535) return false;
  }
  const host = rawHost.toLowerCase();
  if (host === "localhost" || host.endsWith(".localhost")) return true;
  if (host.startsWith("[")) return isIPv6Loopback(host);
  return IPV4_LOOPBACK.test(host);
}
