import {
  createContext,
  useContext,
  useEffect,
  useRef,
  useState,
  useSyncExternalStore,
  type ReactNode,
} from "react";
import { ImageLightbox } from "../components/ImageLightbox";
import { useT } from "../i18n/I18nProvider";
import { CodeBlockCopyButton } from "../messages/CodeBlockCopyButton";
import { readAppliedUiTheme, subscribeAppliedUiTheme } from "../theme/uiTheme";
import {
  cachedPicture,
  renderPicture,
  svgDataUrl,
  type PictureKind,
  type RenderedPicture,
} from "./pictureRender";

/**
 * How long a diagram's text has to stay unchanged before it is drawn again.
 * A streamed answer grows a fence token by token; drawing every prefix would
 * run Mermaid dozens of times on text that does not parse yet.
 */
export const STREAM_SETTLE_MS = 300;

const SERVER_SNAPSHOT = () => "dark" as const;

/**
 * Whether the Markdown around a block is a reply still being streamed. A fence
 * that is half written does not parse, and that is not worth an error line: the
 * block keeps its last picture (or its pending state) until the reply ends, and
 * only then names a diagram that still does not parse.
 */
export const MarkdownStreamingContext = createContext(false);

type FigureError = { kind: "render" | "load"; message: string };

/** Offers a text as a file to save. */
export function downloadText(name: string, text: string, type: string) {
  const url = URL.createObjectURL(new Blob([text], { type }));
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  a.rel = "noopener";
  document.body.appendChild(a);
  a.click();
  a.remove();
  window.setTimeout(() => URL.revokeObjectURL(url), 0);
}

/** Angle brackets: the source behind a picture. */
function SourceGlyph() {
  return (
    <svg
      width="14"
      height="14"
      viewBox="0 0 16 16"
      fill="none"
      aria-hidden
      className="md-copy__glyph"
    >
      <path
        d="M5.5 4L1.5 8l4 4M10.5 4l4 4-4 4"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

/**
 * The switch to a figure's source: one icon button, off while the picture is
 * shown, pressed while the source is. A figure whose picture cannot be drawn
 * shows its source with the button pressed and disabled.
 */
export function SourceToggle(props: {
  showingSource: boolean;
  onToggle: () => void;
  disabled?: boolean;
}) {
  const { t } = useT();
  const label = props.showingSource
    ? t("markdown.figure.showPicture")
    : t("markdown.figure.showSource");
  return (
    <button
      type="button"
      className="md-copy md-figure-source-toggle"
      aria-pressed={props.showingSource}
      disabled={props.disabled}
      title={label}
      aria-label={label}
      data-testid="md-figure-source-toggle"
      onClick={props.onToggle}
    >
      <SourceGlyph />
    </button>
  );
}

/** A file format to save a figure as, named by its extension. */
function FormatButton(props: {
  ext: string;
  title: string;
  testId: string;
  onClick: () => void;
}) {
  return (
    <button
      type="button"
      className="md-copy md-figure-format"
      title={props.title}
      aria-label={props.title}
      data-testid={props.testId}
      onClick={props.onClick}
    >
      <span aria-hidden>{props.ext}</span>
    </button>
  );
}

/**
 * A figure's source: the text in a box that scrolls once it is long, with the
 * copy button in the box's top right corner, as on a code block.
 */
export function FigureSource(props: {
  source: string;
  children?: ReactNode;
  copyTestId: string;
}) {
  return (
    <div className="md-figure-source">
      <CodeBlockCopyButton
        textToCopy={props.source}
        dataTestId={props.copyTestId}
      />
      <pre className="md-figure-code">
        {props.children ?? <code>{props.source}</code>}
      </pre>
    </div>
  );
}

/**
 * A fenced Mermaid or SVG block: drawn as a picture by default, with the
 * source a click away to read, copy or download.
 */
export function DiagramBlock(props: {
  kind: PictureKind;
  source: string;
  children?: ReactNode;
}) {
  const { t } = useT();
  const { kind, source } = props;
  const theme = useSyncExternalStore(
    subscribeAppliedUiTheme,
    readAppliedUiTheme,
    SERVER_SNAPSHOT,
  );
  const [view, setView] = useState<"picture" | "code">("picture");
  const [picture, setPicture] = useState<RenderedPicture | null>(
    () => cachedPicture(kind, source, theme) ?? null,
  );
  const [error, setError] = useState<FigureError | null>(null);
  const [zoomed, setZoomed] = useState(false);
  const drawnOnce = useRef(false);
  const streaming = useContext(MarkdownStreamingContext);

  useEffect(() => {
    let live = true;
    const hit = cachedPicture(kind, source, theme);
    if (hit) {
      drawnOnce.current = true;
      setPicture(hit);
      setError(null);
      return;
    }
    // The first draw of a block is immediate; later ones wait for the text to settle.
    const delay = drawnOnce.current ? STREAM_SETTLE_MS : 0;
    drawnOnce.current = true;
    const timer = window.setTimeout(() => {
      renderPicture(kind, source, theme).then(
        (next) => {
          if (!live) return;
          setPicture(next);
          setError(null);
        },
        (err: unknown) => {
          if (!live) return;
          const failure = describeError(err);
          // A half-written fence: wait for more text or for the reply to end.
          if (streaming && failure.kind === "render") return;
          setPicture(null);
          setError(failure);
        },
      );
    }, delay);
    return () => {
      live = false;
      window.clearTimeout(timer);
    };
  }, [kind, source, theme, streaming]);

  const pictureUnavailable = error !== null;
  const showing = pictureUnavailable ? "code" : view;
  const label =
    kind === "mermaid"
      ? t("markdown.figure.mermaid")
      : t("markdown.figure.svg");

  return (
    <figure
      className="md-figure"
      data-testid="md-figure"
      data-kind={kind}
      data-view={showing}
    >
      <div className="md-figure-head">
        <span className="md-figure-label">{label}</span>
        {!picture && !error ? (
          <span className="md-figure-status" data-testid="md-figure-pending">
            {t("markdown.figure.rendering")}
          </span>
        ) : null}
        <div className="md-figure-actions">
          {kind === "mermaid" ? (
            <FormatButton
              ext="MMD"
              title={t("markdown.figure.downloadSource")}
              testId="md-figure-download-mmd"
              onClick={() => downloadText("diagram.mmd", source, "text/plain")}
            />
          ) : null}
          {kind === "svg" || picture ? (
            <FormatButton
              ext="SVG"
              title={t("markdown.figure.downloadSvg")}
              testId="md-figure-download-svg"
              onClick={() =>
                kind === "svg"
                  ? downloadText("image.svg", source, "image/svg+xml")
                  : downloadText("diagram.svg", picture!.svg, "image/svg+xml")
              }
            />
          ) : null}
          <SourceToggle
            showingSource={showing === "code"}
            disabled={pictureUnavailable}
            onToggle={() => setView((v) => (v === "code" ? "picture" : "code"))}
          />
        </div>
      </div>
      {error ? (
        <p
          className="md-figure-error"
          role="status"
          data-testid="md-figure-error"
        >
          {error.kind === "load"
            ? t("markdown.figure.loadError")
            : t("markdown.figure.error", { message: error.message })}
        </p>
      ) : null}
      {showing === "picture" && picture ? (
        <button
          type="button"
          className="md-figure-picture"
          onClick={() => setZoomed(true)}
          aria-label={t("markdown.figure.open")}
          title={t("markdown.figure.open")}
        >
          <img
            src={svgDataUrl(picture.svg)}
            alt={label}
            width={picture.width}
            height={picture.height}
            data-testid="md-figure-img"
          />
        </button>
      ) : (
        <FigureSource source={source} copyTestId="md-figure-copy">
          {props.children}
        </FigureSource>
      )}
      {zoomed && picture ? (
        <ImageLightbox
          src={svgDataUrl(picture.svg)}
          alt={label}
          onClose={() => setZoomed(false)}
        />
      ) : null}
    </figure>
  );
}

function describeError(err: unknown): FigureError {
  const message = err instanceof Error ? err.message : String(err ?? "");
  // A chunk that did not arrive: the browser's message names the import.
  if (
    /dynamically imported module|Failed to fetch|Importing a module script failed|error loading dynamically/i.test(
      message,
    )
  ) {
    return { kind: "load", message };
  }
  // Mermaid's parse errors are "Parse error on line N:", the line, a caret and
  // "Expecting ..., got ...": the first and the last line say what happened.
  const lines = message
    .split("\n")
    .map((l) => l.trim())
    .filter(Boolean);
  const summary =
    lines.length > 1
      ? `${lines[0]} ${lines[lines.length - 1]}`
      : lines[0] || message;
  return { kind: "render", message: summary.slice(0, 300) };
}
