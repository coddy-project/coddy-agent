/**
 * The short name of a machine: the first label of its host name, the way a
 * person calls it ("pasha-lt" for "pasha-lt.rgs.ru"). An IP address has no
 * such label and is kept whole. Empty for an empty name.
 */
export function shortHostName(host: string): string {
  const h = host.trim().replace(/\.+$/, "");
  if (!h || h.includes(":") || /^[\d.]+$/.test(h)) {
    return h;
  }
  return h.split(".")[0] ?? h;
}
