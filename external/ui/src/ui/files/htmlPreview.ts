/**
 * The document an HTML file's preview shows: the file's own page, made inert.
 *
 * The boundary is the frame that shows it (`FileHtml`): `sandbox=""` runs no
 * script, submits no form, opens no window, moves no other page and gives the
 * document an origin of its own that is no one's, so nothing in it can reach
 * the web UI or its tokens. This module adds what a sandbox does not stop: a
 * policy at the top of the document lets it load nothing from the network
 * (inline styles and `data:` pictures and fonts still draw), and what could
 * move the frame or fetch past the policy is taken out before it is shown.
 *
 * DOMParser builds an inert document: nothing in it runs or loads while it is
 * read and cleaned here.
 */

const XLINK = "http://www.w3.org/1999/xlink";

const POLICY =
  "default-src 'none'; style-src 'unsafe-inline'; img-src data:; font-src data:";

export function htmlPreviewDocument(source: string): string {
  const doc = new DOMParser().parseFromString(source, "text/html");
  // A refresh moves the frame and a policy of the page's own could only add
  // to ours; a base would point relative links somewhere; a link can prefetch
  // or resolve a host the policy does not cover; scripts never run, and are
  // dropped so the document says so too.
  for (const node of doc.querySelectorAll(
    "script, base, link, meta[http-equiv]",
  )) {
    node.remove();
  }
  // A link out of the page would take the frame to another site; it asks for
  // a new window instead, which the sandbox refuses. A link to a part of the
  // page still moves within it. An inline SVG's link may name its address in
  // xlink:href, which no [href] selector reaches.
  for (const node of doc.querySelectorAll("a, area, form")) {
    if (node.localName === "form") {
      node.setAttribute("target", "_blank");
      continue;
    }
    const href =
      node.getAttribute("href") ?? node.getAttributeNS(XLINK, "href");
    if (href === null) continue;
    if (href.startsWith("#")) node.removeAttribute("target");
    else node.setAttribute("target", "_blank");
  }
  const meta = doc.createElement("meta");
  meta.setAttribute("http-equiv", "Content-Security-Policy");
  meta.setAttribute("content", POLICY);
  doc.head.prepend(meta);
  return doctypeOf(doc) + doc.documentElement.outerHTML;
}

/**
 * The page's own doctype, so it renders in the mode it was written for: one
 * without a doctype stays in quirks mode, as the browser would show it.
 */
function doctypeOf(doc: Document): string {
  const type = doc.doctype;
  if (!type) return "";
  const quoted = (id: string) => (id.includes('"') ? "" : id);
  const publicId = quoted(type.publicId);
  const systemId = quoted(type.systemId);
  let out = "<!DOCTYPE " + (type.name || "html");
  if (publicId) out += ` PUBLIC "${publicId}"`;
  if (systemId) out += `${publicId ? "" : " SYSTEM"} "${systemId}"`;
  return out + ">\n";
}
