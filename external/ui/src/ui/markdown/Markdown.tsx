import ReactMarkdown, { defaultUrlTransform } from "react-markdown";
import remarkGfm from "remark-gfm";
import rehypeHighlight from "rehype-highlight";
import {
  createContext,
  isValidElement,
  useCallback,
  useContext,
  useMemo,
  useState,
  type KeyboardEvent,
  type ReactNode,
} from "react";

type CodeProps = {
  className?: string | undefined;
  children?: unknown;
};

const MarkdownPreContext = createContext(false);

type PreProps = {
  children?: unknown;
};

type AProps = {
  href?: string;
  children?: unknown;
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

function CopyButton(props: { text: string }) {
  const [copied, setCopied] = useState(false);

  const onCopy = useCallback(async () => {
    try {
      await copyTextToClipboard(props.text);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 900);
    } catch {
      setCopied(false);
    }
  }, [props.text]);

  return (
    <button
      type="button"
      className="md-copy"
      onClick={() => void onCopy()}
      aria-label="Copy code"
    >
      {copied ? "Copied" : "Copy"}
    </button>
  );
}

function InlineCode(props: { className?: string; children?: unknown }) {
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

  const title = copied ? "Copied" : "Copy";
  const className = ["md-inline-code", props.className || ""]
    .filter(Boolean)
    .join(" ");

  return (
    <code
      className={className}
      role="button"
      tabIndex={0}
      title={title}
      aria-label="Copy code"
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
        <CopyButton text={txt.replace(/\n$/, "")} />
        <pre>{props.children as any}</pre>
      </div>
    </MarkdownPreContext.Provider>
  );
}

export function Markdown(props: { text: string }) {
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
    }),
    [],
  );

  const urlTransform = useCallback((url: string, key: string, node: any) => {
    if (url.startsWith("coddy-skill:")) {
      return url;
    }
    return defaultUrlTransform(url, key, node);
  }, []);

  return (
    <div className="md">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        rehypePlugins={[rehypeHighlight]}
        components={components}
        urlTransform={urlTransform}
      >
        {props.text}
      </ReactMarkdown>
    </div>
  );
}
