import ReactMarkdown, { defaultUrlTransform } from "react-markdown";
import remarkGfm from "remark-gfm";
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
import { useT } from "../i18n/I18nProvider";
import { CodeBlockCopyButton } from "../messages/CodeBlockCopyButton";
import { docsHrefFromCoddyLink } from "../scheduler/hashRoute";
import { remarkDocMentions } from "./remarkDocMentions";

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
const REMARK_PLUGINS = [remarkGfm, remarkDocMentions];
const REHYPE_PLUGINS = [rehypeHighlightShared];

/** A video file where Markdown has an image: the documentation embeds its
 *  recordings this way, fetched from GitHub when they play. */
const VIDEO_SRC = /\.(mp4|webm|mov)(?:[?#]|$)/i;

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

function copyTextToClipboard(text: string): Promise<void> {
  return navigator.clipboard.writeText(text).catch(() => {
    const ta = document.createElement("textarea");
    ta.value = text;
    ta.style.position = "fixed";
    ta.style.opacity = "0";
    document.body.appendChild(ta);
    ta.focus();
    ta.select();
    document.execCommand("copy");
    document.body.removeChild(ta);
  });
}

function InlineCode(props: { className?: string; children?: unknown }) {
  const { t } = useT();
  const text = normalizeText(props.children);
  const [copied, setCopied] = useState(false);

  const onCopy = useCallback(async () => {
    if (!text) return;
    try {
      await copyTextToClipboard(text);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 900);
    } catch {
      setCopied(false);
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

function MarkdownCode(props: CodeProps) {
  const inPre = useContext(MarkdownPreContext);
  if (!inPre) {
    return (
      <InlineCode className={props.className || ""}>
        {props.children as any}
      </InlineCode>
    );
  }
  return (
    <code className={props.className || ""}>{props.children as any}</code>
  );
}

function MarkdownPre(props: PreProps) {
  const txt = normalizeText(props.children);
  return (
    <MarkdownPreContext.Provider value={true}>
      <div className="md-code">
        <CodeBlockCopyButton textToCopy={txt.replace(/\n$/, "")} />
        <pre>{props.children as any}</pre>
      </div>
    </MarkdownPreContext.Provider>
  );
}

export const Markdown = memo(function Markdown(props: { text: string }) {
  const components = useMemo(
    () => ({
      code: MarkdownCode,
      pre: MarkdownPre,
      table: ({ children }: { children?: ReactNode }) => (
        <div className="md-table-scroll">
          <table>{children}</table>
        </div>
      ),
      a: (p: AProps) => {
        const href = typeof p.href === "string" ? p.href : "";
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
        return (
          <a
            href={href}
            {...(external
              ? ({ target: "_blank", rel: "noreferrer noopener" } as const)
              : {})}
          >
            {p.children as any}
          </a>
        );
      },
      img: (p: ImgProps) =>
        p.src && VIDEO_SRC.test(p.src) ? (
          <video
            className="md-video"
            // #t=0.1 shows a first frame before it plays, not a black box.
            src={p.src.includes("#") ? p.src : `${p.src}#t=0.1`}
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
        ),
    }),
    [],
  );

  const urlTransform = useCallback((url: string) => {
    if (url.startsWith("coddy-skill:") || url.startsWith("coddy:")) {
      return url;
    }
    return defaultUrlTransform(url);
  }, []);

  return (
    <div className="md">
      <ReactMarkdown
        remarkPlugins={REMARK_PLUGINS}
        rehypePlugins={REHYPE_PLUGINS}
        components={components}
        urlTransform={urlTransform}
      >
        {props.text}
      </ReactMarkdown>
    </div>
  );
});
