import {
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  useSyncExternalStore,
  type KeyboardEvent,
} from "react";
import { createPortal } from "react-dom";

import { downloadToolArtifact, type ToolArtifact } from "../chat/toolArtifacts";
import { ApiImage, ApiImageLightbox } from "../components/ApiImage";
import { Chevron } from "../components/Chevron";
import { useEscapeCloses } from "../components/useEscapeCloses";
import { relativeFilePath } from "../files/api";
import { openWorkspaceFile } from "../files/fileBus";
import {
  checkWorkspaceEntry,
  useWorkspaceFilesScope,
} from "../files/workspaceEntries";
import { fileTypeIcon } from "./fileTypeIcon";
import { useT } from "../i18n/I18nProvider";
import {
  serverSnapshotShellStack,
  serverSnapshotTouchOnly,
  snapshotShellStack,
  snapshotTouchOnly,
  subscribeShellStack,
  subscribeTouchOnly,
} from "../shellBreakpoint";

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

/** How far the Copy submenu stands from the menu, and how far above Copy it starts: the menu's own padding, so its first item is level with Copy. */
const SUBMENU_GAP_PX = 2;
const SUBMENU_INSET_PX = 4;

/**
 * Where the Copy submenu goes, in window coordinates: beside the menu's right
 * edge with its first item level with Copy, on the menu's left when the
 * window has no room on the right, and inside the window either way.
 */
export function placeArtifactSubmenu(
  item: Pick<DOMRect, "top" | "bottom">,
  menu: Pick<DOMRect, "left" | "right">,
  sub: Size,
  view: Size,
): { left: number; top: number } {
  const right = menu.right + SUBMENU_GAP_PX;
  const left =
    right + sub.width <= view.width - MENU_EDGE_PX
      ? right
      : Math.max(MENU_EDGE_PX, menu.left - SUBMENU_GAP_PX - sub.width);
  const top = Math.max(
    MENU_EDGE_PX,
    Math.min(
      item.top - SUBMENU_INSET_PX,
      view.height - MENU_EDGE_PX - sub.height,
    ),
  );
  return { left, top };
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
  const scope = useWorkspaceFilesScope();
  // The chat on screen now, and whether the card still is: an open that waited
  // on its check opens nothing once the reader has left the chat.
  const scopeRef = useRef(scope);
  scopeRef.current = scope;
  const mountedRef = useRef(true);
  useEffect(() => {
    mountedRef.current = true;
    return () => {
      mountedRef.current = false;
    };
  }, []);
  // A download that failed: the copy is gone, and Download is disabled.
  const [failed, setFailed] = useState(false);
  // The source was found gone when the card was about to open it, after the
  // listing said it was there.
  const [foundDeleted, setFoundDeleted] = useState(false);
  const [downloading, setDownloading] = useState(false);
  const [menu, setMenu] = useState(false);
  // The Copy submenu, open beside the menu.
  const [sub, setSub] = useState(false);
  const [lightbox, setLightbox] = useState(false);
  const [place, setPlace] = useState<{ left: number; top: number } | null>(
    null,
  );
  const [subPlace, setSubPlace] = useState<{
    left: number;
    top: number;
  } | null>(null);
  const menuRef = useRef<HTMLDivElement>(null);
  const subRef = useRef<HTMLDivElement>(null);
  const copyRef = useRef<HTMLButtonElement>(null);
  const triggerRef = useRef<HTMLButtonElement>(null);
  // The submenu was opened from the keyboard, so its first item takes the focus.
  const subFocusRef = useRef(false);
  // A phone, a tablet or a touch screen has no pointer to slide into a
  // submenu: the menu is one list there, as the stacked shell folds its other
  // menus.
  const stacked = useSyncExternalStore(
    subscribeShellStack,
    snapshotShellStack,
    serverSnapshotShellStack,
  );
  const touchOnly = useSyncExternalStore(
    subscribeTouchOnly,
    snapshotTouchOnly,
    serverSnapshotTouchOnly,
  );
  const flat = stacked || touchOnly;
  const artifact = props.artifact;
  const image = isImage(artifact);
  const unavailable = !artifact.url || failed;
  // The card opens the file it was copied from in the Files window, never the
  // copy: an older server says nothing of the source, and the window then
  // tells for itself whether the file is there.
  const deleted = foundDeleted || artifact.sourceState === "deleted";
  const openable =
    !deleted &&
    !!artifact.relativePath &&
    (artifact.sourceState === undefined || artifact.sourceState === "present");
  const openHint = deleted
    ? t("messages.artifactSourceDeletedHint")
    : !openable
      ? t("messages.artifactSourceElsewhere")
      : undefined;
  // A mention puts the path in the composer as @path, which attaches the file
  // only while it is in the chat's workspace.
  const mentionable =
    !deleted &&
    artifact.sourceState !== "unavailable" &&
    (!!artifact.sourcePath || !!artifact.relativePath);
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
        // The focused item goes with the menu: hand the focus back, or the
        // next Tab starts over from the top of the page.
        if (panel.contains(document.activeElement))
          trigger.focus({ preventScroll: true });
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
        !subRef.current?.contains(target) &&
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

  // A closed menu takes its submenu with it.
  useEffect(() => {
    if (!menu) setSub(false);
  }, [menu]);

  // The submenu is portalled too, and placed from Copy and the menu before
  // its first paint, again whenever the menu moves.
  useLayoutEffect(() => {
    if (!sub) return undefined;
    const item = copyRef.current;
    const panel = menuRef.current;
    const subPanel = subRef.current;
    if (!item || !panel || !subPanel) return undefined;
    setSubPlace(
      placeArtifactSubmenu(
        item.getBoundingClientRect(),
        panel.getBoundingClientRect(),
        { width: subPanel.offsetWidth, height: subPanel.offsetHeight },
        { width: window.innerWidth, height: window.innerHeight },
      ),
    );
    if (subFocusRef.current) {
      subFocusRef.current = false;
      enabledItems(subRef.current)[0]?.focus({ preventScroll: true });
    }
    return undefined;
  }, [sub, place]);
  useEffect(() => {
    if (!sub) setSubPlace(null);
  }, [sub]);

  const openSub = (focus: boolean) => {
    subFocusRef.current = focus;
    setSub(true);
  };
  const closeSub = () => {
    setSub(false);
    copyRef.current?.focus({ preventScroll: true });
  };

  // Escape takes one step: out of the submenu first, then the menu.
  useEscapeCloses(menu, () => {
    if (sub) {
      closeSub();
      return;
    }
    setMenu(false);
    triggerRef.current?.focus({ preventScroll: true });
  });

  const listKeys = (
    event: KeyboardEvent<HTMLDivElement>,
    panel: HTMLDivElement | null,
  ) => {
    const items = enabledItems(panel);
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
  const menuKeys = (event: KeyboardEvent<HTMLDivElement>) =>
    listKeys(event, menuRef.current);
  const subKeys = (event: KeyboardEvent<HTMLDivElement>) => {
    if (event.key === "ArrowLeft") {
      event.preventDefault();
      closeSub();
      return;
    }
    listKeys(event, subRef.current);
  };

  // Inside a chat the file is checked first, so a source deleted since the
  // listing turns the card into a deleted one instead of opening a tab that
  // finds nothing. The window resolves the path against the chat's workspace.
  const openSource = async () => {
    const path = artifact.relativePath;
    if (!openable || !path) return;
    const rel = relativeFilePath(path, "", "");
    if (scope && rel) {
      const kind = await checkWorkspaceEntry(scope, rel, { fresh: true });
      const now = scopeRef.current;
      if (
        !mountedRef.current ||
        now?.sessionId !== scope.sessionId ||
        now?.workspacePath !== scope.workspacePath
      )
        return;
      // A server that could not be asked leaves it to the window to tell.
      if (kind !== undefined && kind !== "file") {
        setFoundDeleted(true);
        return;
      }
    }
    openWorkspaceFile(rel || path);
  };
  const action = (fn: () => void | Promise<void>, failure?: "download") => {
    setMenu(false);
    setSub(false);
    // Back to the trigger before the action runs, so an action that moves the
    // focus itself (a mention focuses the composer) keeps it.
    triggerRef.current?.focus({ preventScroll: true });
    void Promise.resolve(fn()).catch(() => {
      if (failure) setFailed(true);
    });
  };
  // The ways to copy, in the order a desktop app lists them: under Copy's
  // submenu on a desktop, under a Copy heading in the one list of a phone.
  const copyItems = [
    {
      key: "absolute",
      label: t("messages.artifactCopyAbsolute"),
      value: artifact.sourcePath,
    },
    {
      key: "relative",
      label: t("messages.artifactCopyRelative"),
      value: relative,
    },
    {
      key: "name",
      label: t("messages.artifactCopyName"),
      value: artifact.name,
    },
  ].map((item) => (
    <button
      key={item.key}
      role="menuitem"
      type="button"
      disabled={!item.value}
      onClick={() => action(() => copy(item.value!))}
    >
      {item.label}
    </button>
  ));

  return (
    <article
      className={[
        "tool-artifact-card",
        props.inline && "inline-artifact-card",
        deleted && "tool-artifact-card--deleted",
      ]
        .filter(Boolean)
        .join(" ")}
      data-testid={`${props.inline ? "inline" : "tool"}-artifact-card-${artifact.id}`}
      title={deleted ? openHint : artifact.sourcePath || artifact.name}
      onContextMenu={(event) => {
        event.preventDefault();
        setMenu(true);
      }}
    >
      {/* The whole card opens the file, a picture's thumbnail too; only a
          picture whose source is gone opens its copy in the lightbox. The
          trigger stands over both. */}
      {openable && !(image && artifact.previewUrl) ? (
        <button
          type="button"
          className="tool-artifact-open"
          aria-label={t("messages.artifactOpenFile", {
            fileName: artifact.name,
          })}
          onClick={() => void openSource()}
        />
      ) : null}
      {image && artifact.previewUrl ? (
        <button
          type="button"
          className={
            openable
              ? "inline-artifact-image inline-artifact-image--open"
              : "inline-artifact-image"
          }
          onClick={() => (openable ? void openSource() : setLightbox(true))}
          aria-label={
            openable
              ? t("messages.artifactOpenFile", { fileName: artifact.name })
              : t("messages.openArtifactImage", { fileName: artifact.name })
          }
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
              : deleted
                ? "tool-artifact-meta tool-artifact-meta--deleted"
                : "tool-artifact-meta"
          }
        >
          {unavailable
            ? t("messages.artifactUnavailable")
            : deleted
              ? t("messages.artifactSourceDeleted")
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
                disabled={!mentionable}
                title={mentionable ? undefined : openHint}
                onMouseEnter={() => setSub(false)}
                onClick={() => action(() => props.onMention?.(relative))}
              >
                {t("messages.artifactMention")}
              </button>
              {flat ? (
                <>
                  <div className="inline-artifact-menu-sep" role="separator" />
                  <div
                    className="inline-artifact-menu-group"
                    role="group"
                    aria-label={t("messages.artifactCopy")}
                  >
                    <div
                      className="inline-artifact-menu-label"
                      aria-hidden="true"
                    >
                      {t("messages.artifactCopy")}
                    </div>
                    {copyItems}
                  </div>
                  <div className="inline-artifact-menu-sep" role="separator" />
                </>
              ) : (
                <button
                  ref={copyRef}
                  role="menuitem"
                  type="button"
                  className="inline-artifact-menu-sub"
                  aria-haspopup="menu"
                  aria-expanded={sub}
                  onMouseEnter={() => openSub(false)}
                  onClick={() => (sub ? setSub(false) : openSub(false))}
                  onKeyDown={(event) => {
                    if (
                      event.key === "ArrowRight" ||
                      event.key === "Enter" ||
                      event.key === " "
                    ) {
                      event.preventDefault();
                      openSub(true);
                    }
                  }}
                >
                  <span>{t("messages.artifactCopy")}</span>
                  <Chevron className="inline-artifact-menu-chevron" />
                </button>
              )}
              <button
                role="menuitem"
                type="button"
                disabled={unavailable || downloading}
                onMouseEnter={() => setSub(false)}
                onClick={() =>
                  action(async () => {
                    setDownloading(true);
                    try {
                      await downloadToolArtifact(artifact);
                    } finally {
                      setDownloading(false);
                    }
                  }, "download")
                }
              >
                {downloading
                  ? t("messages.artifactDownloading")
                  : t("messages.downloadArtifactButton")}
              </button>
            </div>,
            document.body,
          )
        : null}
      {menu && sub && !flat
        ? createPortal(
            <div
              ref={subRef}
              className="inline-artifact-menu inline-artifact-menu--sub"
              role="menu"
              aria-label={t("messages.artifactCopy")}
              style={subPlace ?? { left: 0, top: 0, opacity: 0 }}
              onKeyDown={subKeys}
            >
              {copyItems}
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
