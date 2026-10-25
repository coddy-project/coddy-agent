import ReactMarkdown, { defaultUrlTransform } from "react-markdown";
import rehypeHighlight from "rehype-highlight";
import { syntaxHighlightOptions } from "./syntaxLanguages";
import {
  createContext,
  isValidElement,
  memo,
  useCallback,
  useContext,
  useMemo,
  useState,
  type KeyboardEvent,
  type ReactNode,
} from "react";
import { copyText } from "../clipboard";
import { useT } from "../i18n/I18nProvider";
import { CodeBlockCopyButton } from "../messages/CodeBlockCopyButton";
import { docsHrefFromCoddyLink } from "../scheduler/hashRoute";
import { DiagramBlock, MarkdownStreamingContext } from "./DiagramBlock";
import {
  FileLink,
  FileReferenceLink,
  InsideLinkContext,
  useFileTarget,
} from "./FileReference";
import { localLinkReference, parseFileReference } from "./fileReferences";
import { MathBlock, MathInline } from "./MathFormula";
import { normalizeMathDelimiters } from "./mathDelimiters";
import { REMARK_PLUGINS, REMARK_PLUGINS_WITH_BREAKS } from "./remarkPlugins";
import { FILE_LINK_SCHEME } from "./remarkFilePaths";
import { useWorkspaceFilesScope } from "../files/workspaceEntries";
import { pictureKindOf } from "./pictureRender";

/**
 * rehype-highlight builds a new highlighter each time its attacher runs - every
 * grammar registered again, and compiled again the first time a block uses it -
 * and react-markdown runs the attachers on every render of every Markdown block.
 * One transformer, built on first use, serves them all: opening a long chat
 * spent a fifth of its first frame here (issue #338).
 */
let highlightTransformer: ReturnType<typeof rehypeHighlight> | undefined;
function rehypeHighlightShared() {
  highlightTransformer ??= rehypeHighlight(syntaxHighlightOptions);
  return highlightTransformer;
}
const REHYPE_PLUGINS = [rehypeHighlightShared];

/** A video file where Markdown has an image: the documentation embeds its
 *  recordings this way, fetched from GitHub when they play. */
const VIDEO_SRC = /\.(mp4|webm|mov)(?:[?#]|$)/i;

/** Whether Markdown may fetch a picture or a video from another site as it
 *  renders: "click" waits for the reader, "load" fetches at once. */
export type RemoteMedia = "click" | "load";

/** The host of a picture's address when it is on another site, else null:
 *  a relative address, the page's own origin, data: and blob: load at once. */
export function remoteMediaHost(src: string): string | null {
  let url: URL;
  try {
    url = new URL(src, window.location.href);
  } catch {
    return null;
  }
  if (url.protocol !== "http:" && url.protocol !== "https:") return null;
  return url.origin === window.location.origin ? null : url.host;
}

/**
 * A picture or a video from another site, held until the reader asks for
 * it: an answer that embeds one would otherwise make the browser fetch it as
 * soon as the message renders, with whatever its address carries, and the
 * model's text is not always the operator's (a README, a web page or a tool
 * result can steer it).
 */
function RemoteMediaGate(props: {
  host: string;
  alt: string;
  video: boolean;
  children: ReactNode;
}) {
  const { t } = useT();
  const [shown, setShown] = useState(false);
  if (shown) return <>{props.children}</>;
  const label = t(
    props.video
      ? "markdown.remoteMedia.loadVideo"
      : "markdown.remoteMedia.loadImage",
    { host: props.host },
  );
  return (
    <button
      type="button"
      className="md-remote-media"
      data-testid="md-remote-media"
      onClick={() => setShown(true)}
      title={label}
    >
      {props.alt ? (
        <span className="md-remote-media-alt">{props.alt}</span>
      ) : null}
      <span className="md-remote-media-action">{label}</span>
    </button>
  );
}

type CodeProps = {
  className?: string | undefined;
  children?: unknown;
};

const MarkdownPreContext = createContext(false);

type PreProps = {
  children?: unknown;
};

type AProps = {
  href?: string | undefined;
  children?: unknown;
};

type ImgProps = {
  src?: string | undefined;
  alt?: string | undefined;
  title?: string | undefined;
};

function normalizeText(children: unknown): string {
  if (Array.isArray(children)) {
    return children.map((c) => normalizeText(c)).join("");
  }
  if (isValidElement(children)) {
    return normalizeText((children.props as any)?.children);
  }
  if (typeof children === "string") {
    return children;
  }
  return "";
}

function InlineCode(props: { className?: string; children?: unknown }) {
  const { t } = useT();
  const text = normalizeText(props.children);
  const [copied, setCopied] = useState(false);
  // A path the chat's workspace has as a file opens it in the Files window;
  // any other code copies itself, as it always did. Inside a link the code is
  // the link's label, and the link decides.
  const inChat = useWorkspaceFilesScope() !== null;
  const inLink = useContext(InsideLinkContext);
  const target = useFileTarget(
    inChat && !inLink ? parseFileReference(text) : null,
  );

  const onCopy = useCallback(async () => {
    if (!text) return;
    if (await copyText(text)) {
      setCopied(true);
      window.setTimeout(() => setCopied(false), 900);
    }
  }, [text]);

  const onKeyDown = useCallback(
    (e: KeyboardEvent<HTMLElement>) => {
      if (e.key === "Enter" || e.key === " ") {
        e.preventDefault();
        void onCopy();
      }
    },
    [onCopy],
  );

  const title = copied ? t("messages.copied") : t("messages.copy");
  const className = ["md-inline-code", props.className || ""]
    .filter(Boolean)
    .join(" ");

  // Inside a link the code is the link's label alone: no button of its own,
  // no copy on a click, no second tab stop.
  if (inLink) {
    return <code className={className}>{props.children as any}</code>;
  }

  if (target) {
    return (
      <FileLink target={target}>
        <code className={`${className} md-inline-code--file`}>
          {props.children as any}
        </code>
      </FileLink>
    );
  }

  return (
    <code
      className={className}
      role="button"
      tabIndex={0}
      title={title}
      aria-label={t("messages.copyCode")}
      data-testid="md-inline-code"
      onClick={() => void onCopy()}
      onKeyDown={onKeyDown}
    >
      {props.children as any}
    </code>
  );
}

/** remark-math marks `$...$` as inline code of these classes. */
const MATH_INLINE = /(?:^|\s)math-inline(?:\s|$)/;
/** `$$...$$` and a ```math fence. */
const MATH_BLOCK = /(?:^|\s)language-math(?:\s|$)/;

function MarkdownCode(props: CodeProps) {
  const inPre = useContext(MarkdownPreContext);
  if (!inPre && MATH_INLINE.test(props.className || "")) {
    // Dollars that only look like a formula were turned back into text by
    // remarkLiteralDollars before they got here.
    return <MathInline source={normalizeText(props.children)} />;
  }
  if (!inPre) {
    return (
      <InlineCode className={props.className || ""}>
        {props.children as any}
      </InlineCode>
    );
  }
  return <code className={props.className || ""}>{props.children as any}</code>;
}

function MarkdownPre(props: PreProps) {
  const txt = normalizeText(props.children);
  const code = isValidElement(props.children)
    ? (props.children.props as { className?: string; children?: unknown })
    : undefined;
  const className = code?.className || "";
  if (MATH_BLOCK.test(className)) {
    return <MathBlock source={txt.replace(/\n$/, "")} />;
  }
  const picture = pictureKindOf(className);
  if (picture) {
    return (
      <MarkdownPreContext.Provider value={true}>
        <DiagramBlock kind={picture} source={txt.replace(/\n$/, "")}>
          <code className={className}>{code?.children as any}</code>
        </DiagramBlock>
      </MarkdownPreContext.Provider>
    );
  }
  return (
    <MarkdownPreContext.Provider value={true}>
      <div className="md-code">
        <CodeBlockCopyButton textToCopy={txt.replace(/\n$/, "")} />
        <pre>{props.children as any}</pre>
      </div>
    </MarkdownPreContext.Provider>
  );
}

/**
 * A link of the Markdown: a path in prose (remarkFilePaths), a skill chip, a
 * page of the documentation, a link to a workspace file or any other address.
 */
function MarkdownLink(p: AProps) {
  const href = typeof p.href === "string" ? p.href : "";
  // A path in prose (remarkFilePaths): a link once it names a file of
  // the chat's workspace, the text it was otherwise.
  if (href.startsWith(FILE_LINK_SCHEME)) {
    let value = "";
    try {
      value = decodeURIComponent(href.slice(FILE_LINK_SCHEME.length));
    } catch {
      return <>{p.children as any}</>;
    }
    return (
      <FileReferenceLink
        reference={parseFileReference(value)}
        fallback={p.children as any}
      >
        {p.children as any}
      </FileReferenceLink>
    );
  }
  if (href.startsWith("coddy-skill:")) {
    const name = href.slice("coddy-skill:".length);
    return (
      <span
        className="coddy-skill-chip"
        data-testid="coddy-skill-span"
        data-skill-name={name}
      >
        {p.children as any}
      </span>
    );
  }
  // A page of the built-in documentation, as pages link to each other
  // and as the agent quotes them: open it in the reader.
  const docsHref = docsHrefFromCoddyLink(href);
  if (docsHref) {
    return (
      <a href={docsHref} className="md-docs-link">
        {p.children as any}
      </a>
    );
  }
  const external = /^https?:\/\//i.test(href);
  const anchor = (
    <a
      href={href}
      {...(external
        ? ({ target: "_blank", rel: "noreferrer noopener" } as const)
        : {})}
    >
      {p.children as any}
    </a>
  );
  // A link to a file of the chat's workspace opens it in the Files
  // window; the page address it is otherwise stays as it was.
  const local = external ? null : localLinkReference(href);
  if (local) {
    return (
      <FileReferenceLink reference={local} fallback={anchor}>
        {p.children as any}
      </FileReferenceLink>
    );
  }
  return anchor;
}

export const Markdown = memo(function Markdown(props: {
  text: string;
  /** The text is still arriving: a diagram that does not parse yet is not an error. */
  streaming?: boolean;
  /** A single newline is a line break, as Obsidian renders a note or a canvas
   *  card. Off, it is CommonMark's soft break, a space: the chat, the
   *  documentation and every other body keep that. */
  breaks?: boolean;
  /** Pictures and videos from other sites: held behind a click by default;
   *  the documentation shipped with Coddy loads its own at once. */
  remoteMedia?: RemoteMedia;
}) {
  const remoteMedia = props.remoteMedia ?? "click";
  const components = useMemo(
    () => ({
      code: MarkdownCode,
      pre: MarkdownPre,
      table: ({ children }: { children?: ReactNode }) => (
        <div className="md-table-scroll">
          <table>{children}</table>
        </div>
      ),
      a: (p: AProps) => (
        <InsideLinkContext.Provider value={true}>
          <MarkdownLink {...p} />
        </InsideLinkContext.Provider>
      ),
      img: (p: ImgProps) => {
        const video = !!p.src && VIDEO_SRC.test(p.src);
        const media = video ? (
          <video
            className="md-video"
            // #t=0.1 shows a first frame before it plays, not a black box.
            src={p.src!.includes("#") ? p.src : `${p.src}#t=0.1`}
            controls
            preload="metadata"
            playsInline
            aria-label={p.alt || undefined}
          />
        ) : (
          <img
            src={p.src}
            alt={p.alt || ""}
            {...(p.title ? { title: p.title } : {})}
            loading="lazy"
          />
        );
        const host =
          remoteMedia === "click" && p.src ? remoteMediaHost(p.src) : null;
        if (!host) return media;
        return (
          <RemoteMediaGate host={host} alt={p.alt || ""} video={video}>
            {media}
          </RemoteMediaGate>
        );
      },
    }),
    [remoteMedia],
  );

  const urlTransform = useCallback((url: string) => {
    if (
      url.startsWith("coddy-skill:") ||
      url.startsWith("coddy:") ||
      url.startsWith(FILE_LINK_SCHEME)
    ) {
      return url;
    }
    return defaultUrlTransform(url);
  }, []);

  return (
    <MarkdownStreamingContext.Provider value={props.streaming === true}>
      <div className="md">
        <ReactMarkdown
          remarkPlugins={
            props.breaks ? REMARK_PLUGINS_WITH_BREAKS : REMARK_PLUGINS
          }
          rehypePlugins={REHYPE_PLUGINS}
          components={components}
          urlTransform={urlTransform}
        >
          {normalizeMathDelimiters(props.text)}
        </ReactMarkdown>
      </div>
    </MarkdownStreamingContext.Provider>
  );
});
