import { useEffect, useState } from "react";
import type { CSSProperties, PointerEvent as ReactPointerEvent } from "react";
import { useT } from "../i18n/I18nProvider";
import { Markdown } from "../markdown/Markdown";
import { mediaUrl, readWholeText } from "../files/api";
import { openWorkspaceFile } from "../files/fileBus";
import type { CanvasEdge, CanvasItem } from "./api";
import type { Rect } from "./camera";
import {
  canvasColor,
  edgeShape,
  isImageFile,
  isNoteFile,
  safeLinkUrl,
} from "./jsonCanvas";

/** The most of a note file a card renders. */
const MAX_NOTE_CHARS = 20000;

/** Note texts and picture addresses, per file and version of the linked
 *  canvas, for the life of the page: a board read every few seconds does not
 *  read every note again. */
const noteCache = new Map<string, Promise<string>>();
const imageCache = new Map<string, Promise<string>>();

function cached(
  cache: Map<string, Promise<string>>,
  key: string,
  read: () => Promise<string>,
): Promise<string> {
  let p = cache.get(key);
  if (!p) {
    p = read();
    p.catch(() => cache.delete(key));
    cache.set(key, p);
    if (cache.size > 200) cache.delete(cache.keys().next().value!);
  }
  return p;
}

/** Forgets the cached notes and pictures (tests only). */
export function resetCanvasNodeCachesForTests(): void {
  noteCache.clear();
  imageCache.clear();
}

function useFileResource(
  kind: "note" | "image",
  sessionId: string,
  path: string,
  version: string,
): { value: string; failed: boolean } {
  const [state, setState] = useState({ key: "", value: "", failed: false });
  const key = `${sessionId}\n${path}\n${version}`;
  useEffect(() => {
    if (!path) return undefined;
    let live = true;
    const p =
      kind === "note"
        ? cached(noteCache, key, () =>
            readWholeText(sessionId, path, "", MAX_NOTE_CHARS),
          )
        : cached(imageCache, key, () => mediaUrl(sessionId, path));
    p.then(
      (value) => live && setState({ key, value, failed: false }),
      () => live && setState({ key, value: "", failed: true }),
    );
    return () => {
      live = false;
    };
  }, [kind, sessionId, path, key]);
  return state.key === key
    ? { value: state.value, failed: state.failed }
    : { value: "", failed: false };
}

function baseName(path: string): string {
  return path.split("/").pop() || path;
}

function folderOf(path: string): string {
  const i = path.lastIndexOf("/");
  return i > 0 ? path.slice(0, i) : "";
}

export interface CanvasNodeProps {
  sessionId: string;
  item: CanvasItem;
  /** Where the node stands on the screen, a drag in progress included. */
  screen: Rect;
  scale: number;
  /** The linked file's stamp: a new one reads the notes again. */
  version: string;
  dragging: boolean;
  onPointerDown: (item: CanvasItem, e: ReactPointerEvent<HTMLElement>) => void;
  onPointerMove: (e: ReactPointerEvent<HTMLElement>) => void;
  onPointerUp: (e: ReactPointerEvent<HTMLElement>) => void;
}

/**
 * A node of a linked .canvas file that is not a live frame, drawn the way
 * Obsidian draws it: a text card rendered as Markdown, a note or a picture of
 * the workspace, a file card, a link card, a group with its label. The body
 * stands at the node's own size, scaled by the camera like a frame.
 */
export function CanvasNode(props: CanvasNodeProps) {
  const { item, screen, scale } = props;
  const color = canvasColor(item.color);
  const style = {
    left: screen.x,
    top: screen.y,
    width: screen.width,
    height: screen.height,
    ...(color ? { "--canvas-node-color": color } : {}),
  } as CSSProperties;
  const handlers = {
    onPointerDown: (e: ReactPointerEvent<HTMLElement>) =>
      props.onPointerDown(item, e),
    onPointerMove: props.onPointerMove,
    onPointerUp: props.onPointerUp,
    onPointerCancel: props.onPointerUp,
  };
  if (item.type === "group") {
    return (
      <div
        className={
          "canvas-group" +
          (color ? " is-colored" : "") +
          (props.dragging ? " is-dragging" : "")
        }
        style={style}
        data-testid="canvas-group"
        data-node-id={item.id}
        {...handlers}
      >
        {item.label ? (
          <span className="canvas-group-label">{item.label}</span>
        ) : null}
      </div>
    );
  }
  return (
    <div
      className={
        `canvas-node canvas-node--${item.type}` +
        (color ? " is-colored" : "") +
        (props.dragging ? " is-dragging" : "")
      }
      style={style}
      data-testid="canvas-node"
      data-node-id={item.id}
      data-node-type={item.type}
      {...handlers}
    >
      <div
        className="canvas-node-body"
        style={{
          width: item.width,
          height: item.height,
          transform: `scale(${scale})`,
        }}
      >
        <NodeBody
          sessionId={props.sessionId}
          item={item}
          version={props.version}
        />
      </div>
    </div>
  );
}

function NodeBody(props: {
  sessionId: string;
  item: CanvasItem;
  version: string;
}) {
  const { item } = props;
  switch (item.type) {
    case "text":
      return (
        <div className="canvas-node-text">
          <Markdown text={item.text || ""} />
        </div>
      );
    case "link":
      return <LinkCard url={item.url} />;
    default:
      return (
        <FileNode
          sessionId={props.sessionId}
          path={item.file || ""}
          note={item.note}
          version={props.version}
        />
      );
  }
}

function LinkCard(props: { url?: string | undefined }) {
  const { t } = useT();
  const href = safeLinkUrl(props.url);
  let host = props.url || "";
  try {
    if (href) host = new URL(href).host;
  } catch {
    /* the address as written */
  }
  return (
    <div className="canvas-node-card">
      <span className="canvas-node-kind">{t("canvas.node.link")}</span>
      <span className="canvas-node-name">{host}</span>
      {href ? (
        <a
          className="canvas-node-url"
          href={href}
          target="_blank"
          rel="noopener noreferrer"
        >
          {props.url}
        </a>
      ) : (
        <span className="canvas-node-url">{props.url}</span>
      )}
    </div>
  );
}

function FileNode(props: {
  sessionId: string;
  path: string;
  note?: string | undefined;
  version: string;
}) {
  const { t } = useT();
  const image = !props.note && isImageFile(props.path);
  const note = !props.note && isNoteFile(props.path);
  const picture = useFileResource(
    "image",
    props.sessionId,
    image ? props.path : "",
    props.version,
  );
  const text = useFileResource(
    "note",
    props.sessionId,
    note ? props.path : "",
    props.version,
  );
  if (image && picture.value) {
    return (
      <img
        className="canvas-node-image"
        src={picture.value}
        alt={baseName(props.path)}
        draggable={false}
        data-image-name={baseName(props.path)}
      />
    );
  }
  if (note && text.value) {
    return (
      <div className="canvas-node-text canvas-node-note">
        <p className="canvas-node-note-name">{baseName(props.path)}</p>
        <Markdown text={text.value} />
      </div>
    );
  }
  return (
    <div className="canvas-node-card">
      <span className="canvas-node-kind">{t("canvas.node.file")}</span>
      <span className="canvas-node-name" title={props.path}>
        {baseName(props.path)}
      </span>
      {folderOf(props.path) ? (
        <span className="canvas-node-folder">{folderOf(props.path)}</span>
      ) : null}
      {props.note ? (
        <span className="canvas-node-warning" data-testid="canvas-node-note">
          {props.note}
        </span>
      ) : null}
      {picture.failed || text.failed ? (
        <span className="canvas-node-warning">
          {t("canvas.node.unreadable")}
        </span>
      ) : null}
      {props.path ? (
        <button
          type="button"
          className="canvas-node-open"
          onClick={() => openWorkspaceFile(props.path)}
        >
          {t("canvas.node.openInFiles")}
        </button>
      ) : null}
    </div>
  );
}

/** The edges of a linked file, under the nodes, with their labels. */
export function CanvasEdges(props: {
  edges: CanvasEdge[];
  rects: ReadonlyMap<string, Rect>;
  scale: number;
}) {
  const shapes = props.edges.flatMap((edge) => {
    const shape = edgeShape(edge, props.rects, props.scale);
    return shape ? [{ edge, shape, color: canvasColor(edge.color) }] : [];
  });
  if (shapes.length === 0) return null;
  return (
    <>
      <svg
        className="canvas-edges"
        aria-hidden="true"
        data-testid="canvas-edges"
      >
        {shapes.map(({ edge, shape, color }) => (
          <g
            key={edge.id}
            data-edge-id={edge.id}
            style={color ? { color } : undefined}
            className={color ? "is-colored" : undefined}
          >
            <path className="canvas-edge" d={shape.d} />
            {shape.arrows.map((points, i) => (
              <polygon key={i} className="canvas-edge-arrow" points={points} />
            ))}
          </g>
        ))}
      </svg>
      {shapes.map(({ edge, shape }) =>
        edge.label ? (
          <span
            key={edge.id}
            className="canvas-edge-label"
            style={{ left: shape.mid.x, top: shape.mid.y }}
          >
            {edge.label}
          </span>
        ) : null,
      )}
    </>
  );
}
