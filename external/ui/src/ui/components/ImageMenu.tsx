import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { useT } from "../i18n/I18nProvider";
import { objectUrlBlob } from "../files/objectUrl";

/**
 * The menu a right click (a long press on a touch screen) opens on a picture
 * of the app: Copy image and Save image, the way a desktop app offers them.
 * One listener on the document serves every picture - a file in the Files
 * window, an image in the chat, a diagram, the full-screen viewer, the
 * documentation - so a new picture needs nothing to get it.
 *
 * Only the app's own pictures take it: a `blob:` or `data:` picture, or one of
 * the page's own origin. A picture from another site keeps the browser's own
 * menu, and so does anything under `[data-native-image-menu]`; a picture
 * inside something that answered the right click itself keeps that answer. A picture may
 * name the file it is saved as (`data-image-name`); otherwise its alt text or
 * "image" does, with the extension its type gives.
 *
 * Copy writes a PNG, the one picture type every clipboard takes: a PNG goes as
 * it is, anything else is drawn onto a canvas first. The item is handed a
 * promise of the bytes, so the write starts within the click, which Safari
 * requires. Without a clipboard that takes pictures (an insecure origin, an
 * older browser) the menu offers Save only.
 */

interface OpenMenu {
  x: number;
  y: number;
  img: HTMLImageElement;
}

interface Notice {
  x: number;
  y: number;
  text: string;
}

const EXTENSION_BY_TYPE: Record<string, string> = {
  "image/png": "png",
  "image/jpeg": "jpg",
  "image/gif": "gif",
  "image/webp": "webp",
  "image/avif": "avif",
  "image/svg+xml": "svg",
};

/** Whether a picture is the app's own, which the menu serves. */
export function isOwnImage(img: HTMLImageElement): boolean {
  if (img.closest("[data-native-image-menu]")) return false;
  const src = img.currentSrc || img.getAttribute("src") || "";
  if (!src) return false;
  try {
    const url = new URL(src, window.location.href);
    return (
      url.protocol === "blob:" ||
      url.protocol === "data:" ||
      url.origin === window.location.origin
    );
  } catch {
    return false;
  }
}

/** Whether this browser can put a picture on the clipboard. */
export function canCopyImages(): boolean {
  return (
    typeof ClipboardItem !== "undefined" &&
    typeof navigator !== "undefined" &&
    typeof navigator.clipboard?.write === "function"
  );
}

async function imageBlob(img: HTMLImageElement): Promise<Blob> {
  const src = img.currentSrc || img.src;
  const own = objectUrlBlob(src);
  if (own) return own;
  const res = await fetch(src);
  if (!res.ok) throw new Error(`HTTP ${res.status}`);
  return res.blob();
}

/** The picture as PNG bytes: as it is when it is one, drawn when it is not. */
async function imagePng(img: HTMLImageElement): Promise<Blob> {
  const blob = await imageBlob(img);
  if (blob.type === "image/png") return blob;
  if (!img.complete || img.naturalWidth === 0) await img.decode();
  const box = img.getBoundingClientRect();
  const scale = window.devicePixelRatio || 1;
  const width = img.naturalWidth || Math.round(box.width * scale);
  const height = img.naturalHeight || Math.round(box.height * scale);
  const canvas = document.createElement("canvas");
  canvas.width = width;
  canvas.height = height;
  const context = canvas.getContext("2d");
  if (!context) throw new Error("no canvas");
  context.drawImage(img, 0, 0, width, height);
  return new Promise((resolve, reject) =>
    canvas.toBlob(
      (png) => (png ? resolve(png) : reject(new Error("no PNG"))),
      "image/png",
    ),
  );
}

/** The name a saved picture takes, with the extension of its type. */
export function imageFileName(img: HTMLImageElement, type: string): string {
  const ext = EXTENSION_BY_TYPE[type.split(";")[0]!.trim().toLowerCase()];
  const named = (img.dataset.imageName || "").trim();
  const alt = (img.getAttribute("alt") || "").trim();
  const base =
    named || (/^[^\\/]+\.[a-z0-9]{2,5}$/i.test(alt) ? alt : "") || "image";
  if (!ext || /\.[a-z0-9]{2,5}$/i.test(base)) return base;
  return `${base}.${ext}`;
}

async function saveImage(img: HTMLImageElement): Promise<void> {
  const blob = await imageBlob(img);
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = imageFileName(img, blob.type);
  anchor.rel = "noopener";
  document.body.append(anchor);
  anchor.click();
  anchor.remove();
  // The download has its bytes by then; the address goes with the page anyway.
  window.setTimeout(() => URL.revokeObjectURL(url), 60_000);
}

export function ImageMenuHost() {
  const { t } = useT();
  const [menu, setMenu] = useState<OpenMenu | null>(null);
  const [notice, setNotice] = useState<Notice | null>(null);
  const [place, setPlace] = useState<{ left: number; top: number } | null>(
    null,
  );
  const menuRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    const onContextMenu = (e: MouseEvent) => {
      // A picture inside something with a menu of its own (a shared file's
      // card) keeps that menu.
      if (e.defaultPrevented) return;
      const target = e.target instanceof Element ? e.target : null;
      const img = target?.closest("img");
      if (!img || !isOwnImage(img)) return;
      e.preventDefault();
      setNotice(null);
      setPlace(null);
      setMenu({ x: e.clientX, y: e.clientY, img });
    };
    document.addEventListener("contextmenu", onContextMenu);
    return () => document.removeEventListener("contextmenu", onContextMenu);
  }, []);

  // Beside the pointer, inside the window: flipped to the left or above when
  // it would run past an edge.
  useLayoutEffect(() => {
    if (!menu || !menuRef.current) return;
    const box = menuRef.current.getBoundingClientRect();
    const margin = 8;
    const left =
      menu.x + box.width + margin > window.innerWidth
        ? Math.max(margin, menu.x - box.width)
        : menu.x;
    const top =
      menu.y + box.height + margin > window.innerHeight
        ? Math.max(margin, menu.y - box.height)
        : menu.y;
    setPlace({ left, top });
    menuRef.current.querySelector<HTMLElement>('[role="menuitem"]')?.focus({
      preventScroll: true,
    });
  }, [menu]);

  // A press beside it, a scroll, a resize, the page losing focus or Escape
  // put it away. Escape is heard on the window before anything on the
  // document, so it takes the menu and not the viewer under it.
  useEffect(() => {
    if (!menu) return undefined;
    const close = () => setMenu(null);
    const onPointerDown = (e: Event) => {
      if (!(e.target instanceof Node) || !menuRef.current?.contains(e.target))
        close();
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape" || e.isComposing) return;
      e.preventDefault();
      e.stopPropagation();
      close();
    };
    document.addEventListener("pointerdown", onPointerDown, true);
    window.addEventListener("keydown", onKey, true);
    window.addEventListener("resize", close);
    window.addEventListener("blur", close);
    document.addEventListener("scroll", close, true);
    return () => {
      document.removeEventListener("pointerdown", onPointerDown, true);
      window.removeEventListener("keydown", onKey, true);
      window.removeEventListener("resize", close);
      window.removeEventListener("blur", close);
      document.removeEventListener("scroll", close, true);
    };
  }, [menu]);

  useEffect(() => {
    if (!notice) return undefined;
    const timer = window.setTimeout(() => setNotice(null), 1600);
    return () => window.clearTimeout(timer);
  }, [notice]);

  const say = (at: OpenMenu, text: string) =>
    setNotice({ x: at.x, y: at.y, text });

  const copy = () => {
    if (!menu) return;
    const at = menu;
    setMenu(null);
    try {
      const item = new ClipboardItem({ "image/png": imagePng(at.img) });
      navigator.clipboard.write([item]).then(
        () => say(at, t("image.copied")),
        () => say(at, t("image.copyFailed")),
      );
    } catch {
      say(at, t("image.copyFailed"));
    }
  };

  const save = () => {
    if (!menu) return;
    const at = menu;
    setMenu(null);
    saveImage(at.img).catch(() => say(at, t("image.saveFailed")));
  };

  return (
    <>
      {menu
        ? createPortal(
            <div
              ref={menuRef}
              className="mode-menu mode-menu--portal image-menu"
              role="menu"
              aria-label={t("image.menu")}
              style={
                place
                  ? { left: place.left, top: place.top }
                  : { left: menu.x, top: menu.y, visibility: "hidden" }
              }
            >
              {canCopyImages() ? (
                <button
                  type="button"
                  role="menuitem"
                  className="mode-item image-menu-item"
                  onClick={copy}
                >
                  {t("image.copy")}
                </button>
              ) : null}
              <button
                type="button"
                role="menuitem"
                className="mode-item image-menu-item"
                onClick={save}
              >
                {t("image.save")}
              </button>
            </div>,
            document.body,
          )
        : null}
      {notice
        ? createPortal(
            <div
              role="status"
              className="image-menu-notice"
              style={{ left: notice.x, top: notice.y }}
            >
              {notice.text}
            </div>,
            document.body,
          )
        : null}
    </>
  );
}
