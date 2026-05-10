import ReactMarkdown, { defaultUrlTransform } from 'react-markdown';
import remarkGfm from 'remark-gfm';
import rehypeHighlight from 'rehype-highlight';
import { isValidElement, useCallback, useMemo, useState } from 'react';

type CodeProps = {
  inline?: boolean;
  className?: string | undefined;
  children?: unknown;
};

type PreProps = {
  children?: unknown;
};

type AProps = {
  href?: string;
  children?: unknown;
};

function normalizeText(children: unknown): string {
  if (Array.isArray(children)) {
    return children.map((c) => normalizeText(c)).join('');
  }
  if (isValidElement(children)) {
    return normalizeText((children.props as any)?.children);
  }
  if (typeof children === 'string') {
    return children;
  }
  return '';
}

function CopyButton(props: { text: string }) {
  const [copied, setCopied] = useState(false);

  const onCopy = useCallback(async () => {
    try {
      await navigator.clipboard.writeText(props.text);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 900);
    } catch {
      try {
        const ta = document.createElement('textarea');
        ta.value = props.text;
        ta.style.position = 'fixed';
        ta.style.opacity = '0';
        document.body.appendChild(ta);
        ta.focus();
        ta.select();
        document.execCommand('copy');
        document.body.removeChild(ta);
        setCopied(true);
        window.setTimeout(() => setCopied(false), 900);
      } catch {
        setCopied(false);
      }
    }
  }, [props.text]);

  return (
    <button type="button" className="md-copy" onClick={() => void onCopy()} aria-label="Copy code">
      {copied ? 'Copied' : 'Copy'}
    </button>
  );
}

export function Markdown(props: { text: string }) {
  const components = useMemo(
    () => ({
      code: (p: CodeProps) => {
        if (p.inline) {
          return <code className={p.className || ''}>{p.children as any}</code>;
        }
        return <code className={p.className || ''}>{p.children as any}</code>;
      },
      pre: (p: PreProps) => {
        const txt = normalizeText(p.children);
        const codeEl = isValidElement(p.children) ? p.children : null;
        const className = (codeEl?.props as any)?.className || '';
        const hasClass = typeof className === 'string' && className.trim() !== '';
        const isFenced = hasClass && (className.includes('language-') || className.includes('hljs'));

        return (
          <div className="md-code">
            {isFenced ? <CopyButton text={txt.replace(/\n$/, '')} /> : null}
            <pre>{p.children as any}</pre>
          </div>
        );
      },
      a: (p: AProps) => {
        const href = typeof p.href === 'string' ? p.href : '';
        if (href.startsWith('coddy-skill:')) {
          const name = href.slice('coddy-skill:'.length);
          return (
            <span className="coddy-skill-chip" data-testid="coddy-skill-span" data-skill-name={name}>
              {p.children as any}
            </span>
          );
        }
        const external = /^https?:\/\//i.test(href);
        return (
          <a href={href} {...(external ? ({ target: '_blank', rel: 'noreferrer noopener' } as const) : {})}>
            {p.children as any}
          </a>
        );
      },
    }),
    [],
  );

  const urlTransform = useCallback((url: string, key: string, node: any) => {
    if (url.startsWith('coddy-skill:')) {
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
