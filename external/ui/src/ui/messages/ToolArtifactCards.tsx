import { useLayoutEffect, useRef, useState, type KeyboardEvent } from "react";
import { createPortal } from "react-dom";

import { downloadToolArtifact, type ToolArtifact } from "../chat/toolArtifacts";
import { ApiImage, ApiImageLightbox } from "../components/ApiImage";
import { useEscapeCloses } from "../components/useEscapeCloses";
import { fileTypeIcon } from "./fileTypeIcon";
import { useT } from "../i18n/I18nProvider";
import { remoteApiRequest } from "../env/remoteEnv";

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  if (n < 1024 * 1024 * 1024) return `${(n / (1024 * 1024)).toFixed(1)} MB`;
  return `${(n / (1024 * 1024 * 1024)).toFixed(1)} GB`;
}

const isImage = (artifact: ToolArtifact) =>
  Boolean(artifact.previewUrl) ||
  /\.(png|jpe?g|gif|webp|bmp|svg)$/i.test(artifact.name);

function copy(text: string) {
  void navigator.clipboard?.writeText(text);
}

/** The gap between the trigger and its menu, and the least distance of the menu from the window's edges. */
const MENU_GAP_PX = 6;
const MENU_EDGE_PX = 8;

type Size = { width: number; height: number };
type Edges = Pick<DOMRect, "top" | "right" | "bottom">;

/**
 * Where the actions menu goes, in window coordinates: under the trigger with
 * their right edges together, above it when the window's foot leaves no room
 * below, and inside the window either way, so a card at a phone's left edge
 * does not push its menu off the screen.
 */
export function placeArtifactMenu(
  anchor: Edges,
  menu: Size,
  view: Size,
): { left: number; top: number } {
  const left = Math.max(
    MENU_EDGE_PX,
    Math.min(anchor.right - menu.width, view.width - MENU_EDGE_PX - menu.width),
  );
  const below = anchor.bottom + MENU_GAP_PX;
  const above = anchor.top - MENU_GAP_PX - menu.height;
  const fitsBelow = below + menu.height <= view.height - MENU_EDGE_PX;
  if (!fitsBelow && above >= MENU_EDGE_PX) return { left, top: above };
  return {
    left,
    top: Math.max(
      MENU_EDGE_PX,
      Math.min(below, view.height - MENU_EDGE_PX - menu.height),
    ),
  };
}

/** The trigger's three dots, centred in their viewBox so the box alone places them. */
function IconActions() {
  return (
    <svg
      width="15"
      height="15"
      viewBox="0 0 24 24"
      fill="currentColor"
      aria-hidden="true"
    >
      <circle cx="12" cy="5" r="1.7" />
      <circle cx="12" cy="12" r="1.7" />
      <circle cx="12" cy="19" r="1.7" />
    </svg>
  );
}

/**
 * Whether the trigger of an open menu can still be seen: its centre inside the
 * window, and nothing on top there but the trigger, or the menu itself in a
 * window too short to keep the two apart. The transcript scrolls under the
 * chat's sticky title and the docked composer without leaving the window, and
 * a menu that only watched the window's edges stayed open over the composer.
 */
function triggerShows(trigger: HTMLElement, menu: HTMLElement): boolean {
  const box = trigger.getBoundingClientRect();
  const x = box.left + box.width / 2;
  const y = box.top + box.height / 2;
  if (x < 0 || y < 0 || x > window.innerWidth || y > window.innerHeight)
    return false;
  // jsdom has no hit testing; a browser always has.
  const hit =
    typeof document.elementFromPoint === "function"
      ? document.elementFromPoint(x, y)
      : null;
  return !hit || trigger.contains(hit) || menu.contains(hit);
}

const enabledItems = (menu: HTMLElement | null) =>
  menu
    ? [
        ...menu.querySelectorAll<HTMLButtonElement>(
          '[role="menuitem"]:not(:disabled)',
        ),
      ]
    : [];

export function ToolArtifactCards(props: {
  artifacts: readonly ToolArtifact[];
  inline?: boolean;
  onMention?: (path: string) => void;
}) {
  const { t } = useT();
  if (props.artifacts.length === 0) return null;
  return (
    <section
      className={props.inline ? "inline-artifacts" : "tool-artifacts"}
      aria-label={t("messages.toolArtifacts")}
    >
      {props.artifacts.map((artifact) => (
        <ArtifactCard
          key={artifact.id}
          artifact={artifact}
          {...(props.inline !== undefined ? { inline: props.inline } : {})}
          {...(props.onMention ? { onMention: props.onMention } : {})}
        />
      ))}
    </section>
  );
}

export function ArtifactCard(props: {
  artifact: ToolArtifact;
  inline?: boolean;
  onMention?: (path: string) => void;
}) {
  const { t } = useT();
  const [failed, setFailed] = useState(false);
  const [downloading, setDownloading] = useState(false);
  const [menu, setMenu] = useState(false);
  const [lightbox, setLightbox] = useState(false);
  const [place, setPlace] = useState<{ left: number; top: number } | null>(
    null,
  );
  const menuRef = useRef<HTMLDivElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  const artifact = props.artifact;
  const image = isImage(artifact);
  const unavailable = !artifact.url || failed;
  const extension = artifact.name.includes(".")
    ? artifact.name.split(".").pop()!.toUpperCase()
    : "FILE";
  const relative =
    artifact.relativePath || artifact.sourcePath || artifact.name;
  const typeLabel = fileTypeIcon("", artifact.name).label;

  // The menu is rendered into the document, not into the card: the card is
  // `overflow: hidden` (it clips a thumbnail to its corners) and cut the menu
  // off, so a click on the trigger drew nothing. Placed from the trigger before
  // the first paint, it follows the trigger while the page scrolls and closes
  // once the trigger cannot be seen any more.
  useLayoutEffect(() => {
    if (!menu) return undefined;
    const place = () => {
      const trigger = triggerRef.current;
      const panel = menuRef.current;
      if (!trigger || !panel) return;
      setPlace(
        placeArtifactMenu(
          trigger.getBoundingClientRect(),
          { width: panel.offsetWidth, height: panel.offsetHeight },
          { width: window.innerWidth, height: window.innerHeight },
        ),
      );
    };
    const follow = () => {
      const trigger = triggerRef.current;
      const panel = menuRef.current;
      if (trigger && panel && !triggerShows(trigger, panel)) {
        setMenu(false);
        return;
      }
      place();
    };
    place();
    enabledItems(menuRef.current)[0]?.focus({ preventScroll: true });
    const press = (event: MouseEvent) => {
      const target = event.target as Node;
      // The trigger toggles the menu on its own click; closing it here first
      // made that click open it again.
      if (
        !menuRef.current?.contains(target) &&
        !triggerRef.current?.contains(target)
      )
        setMenu(false);
    };
    document.addEventListener("mousedown", press);
    window.addEventListener("scroll", follow, true);
    window.addEventListener("resize", follow);
    return () => {
      document.removeEventListener("mousedown", press);
      window.removeEventListener("scroll", follow, true);
      window.removeEventListener("resize", follow);
      setPlace(null);
    };
  }, [menu]);

  useEscapeCloses(menu, () => {
    setMenu(false);
    triggerRef.current?.focus({ preventScroll: true });
  });

  const menuKeys = (event: KeyboardEvent<HTMLDivElement>) => {
    const items = enabledItems(menuRef.current);
    const at = items.indexOf(document.activeElement as HTMLButtonElement);
    const step =
      event.key === "ArrowDown" ? 1 : event.key === "ArrowUp" ? -1 : 0;
    if (step !== 0 && items.length > 0) {
      event.preventDefault();
      const next =
        at < 0
          ? step > 0
            ? 0
            : items.length - 1
          : (at + step + items.length) % items.length;
      items[next]?.focus();
    } else if (event.key === "Home" || event.key === "End") {
      event.preventDefault();
      items[event.key === "Home" ? 0 : items.length - 1]?.focus();
    } else if (event.key === "Tab") {
      // Leave the menu where the trigger is, not at the end of the document.
      setMenu(false);
      triggerRef.current?.focus({ preventScroll: true });
    }
  };

  const reveal = async () => {
    if (!artifact.revealUrl) return;
    const request = remoteApiRequest(artifact.revealUrl);
    const response = await fetch(
      request?.url || artifact.revealUrl,
      request?.init,
    );
    if (!response.ok) throw new Error(`reveal failed (${response.status})`);
  };
  const action = (fn: () => void | Promise<void>) => {
    setMenu(false);
    // Back to the trigger before the action runs, so an action that moves the
    // focus itself (a mention focuses the composer) keeps it.
    triggerRef.current?.focus({ preventScroll: true });
    void Promise.resolve(fn()).catch(() => setFailed(true));
  };
  return (
    <article
      className={["tool-artifact-card", props.inline && "inline-artifact-card"]
        .filter(Boolean)
        .join(" ")}
      data-testid={`${props.inline ? "inline" : "tool"}-artifact-card-${artifact.id}`}
      title={artifact.sourcePath || artifact.name}
      onContextMenu={(event) => {
        event.preventDefault();
        setMenu(true);
      }}
    >
      {image && artifact.previewUrl ? (
        <button
          type="button"
          className="inline-artifact-image"
          onClick={() => setLightbox(true)}
          aria-label={t("messages.openArtifactImage", {
            fileName: artifact.name,
          })}
        >
          <ApiImage
            className="inline-artifact-thumb"
            src={artifact.previewUrl}
            alt=""
            data-testid="inline-artifact-thumb"
          />
        </button>
      ) : null}
      <span className="inline-artifact-extension" aria-hidden="true">
        {extension}
      </span>
      <span className="tool-artifact-info">
        <span className="tool-artifact-name" title={artifact.name}>
          {artifact.name}
        </span>
        <span
          className={
            unavailable
              ? "tool-artifact-meta tool-artifact-meta--error"
              : "tool-artifact-meta"
          }
        >
          {unavailable
            ? t("messages.artifactUnavailable")
            : props.inline
              ? formatBytes(artifact.size)
              : `${typeLabel} · ${formatBytes(artifact.size)}`}
        </span>
      </span>
      <button
        ref={triggerRef}
        type="button"
        className="inline-artifact-menu-trigger"
        aria-label={t("messages.artifactActions", { fileName: artifact.name })}
        aria-haspopup="menu"
        aria-expanded={menu}
        onClick={() => setMenu((open) => !open)}
      >
        <IconActions />
      </button>
      {menu
        ? createPortal(
            <div
              ref={menuRef}
              className="inline-artifact-menu"
              role="menu"
              aria-label={artifact.name}
              // Unplaced only until the layout effect measures it, before any
              // paint. Transparent rather than hidden: a hidden item cannot
              // take the focus that effect gives it.
              style={place ?? { left: 0, top: 0, opacity: 0 }}
              onKeyDown={menuKeys}
            >
              <button
                role="menuitem"
                type="button"
                disabled={!artifact.sourcePath && !artifact.relativePath}
                onClick={() => action(() => props.onMention?.(relative))}
              >
                {t("messages.artifactMention")}
              </button>
              <button
                role="menuitem"
                type="button"
                onClick={() => action(() => copy(artifact.name))}
              >
                {t("messages.artifactCopyName")}
              </button>
              <button
                role="menuitem"
                type="button"
                onClick={() => action(() => copy(relative))}
              >
                {t("messages.artifactCopyRelative")}
              </button>
              <button
                role="menuitem"
                type="button"
                disabled={!artifact.sourcePath}
                onClick={() => action(() => copy(artifact.sourcePath!))}
              >
                {t("messages.artifactCopyAbsolute")}
              </button>
              <button
                role="menuitem"
                type="button"
                disabled={unavailable || downloading}
                onClick={() =>
                  action(async () => {
                    setDownloading(true);
                    try {
                      await downloadToolArtifact(artifact);
                    } finally {
                      setDownloading(false);
                    }
                  })
                }
              >
                {downloading
                  ? t("messages.artifactDownloading")
                  : t("messages.downloadArtifactButton")}
              </button>
              <button
                role="menuitem"
                type="button"
                disabled={!artifact.revealUrl}
                title={
                  !artifact.revealUrl
                    ? t("messages.artifactRevealUnavailable")
                    : undefined
                }
                onClick={() => action(reveal)}
              >
                {t("messages.artifactReveal")}
              </button>
            </div>,
            document.body,
          )
        : null}
      {lightbox && artifact.previewUrl ? (
        <ApiImageLightbox
          src={artifact.previewUrl}
          alt={artifact.name}
          onClose={() => setLightbox(false)}
        />
      ) : null}
    </article>
  );
}
