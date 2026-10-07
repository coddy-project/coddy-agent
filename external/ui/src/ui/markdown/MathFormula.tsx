import "katex/dist/katex.min.css";
import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type HTMLAttributes,
  type KeyboardEvent,
} from "react";
import { useT } from "../i18n/I18nProvider";
import { FigureSource, SourceToggle } from "./DiagramBlock";
import { loadKatex, type KatexApi } from "./renderers";

/** How every formula is typeset. */
export const KATEX_OPTIONS = {
  // Bad TeX comes out as KaTeX's own red source rather than an exception.
  throwOnError: false,
  // No \href, \url, \includegraphics or \htmlClass from a model's answer.
  trust: false,
  strict: "ignore" as const,
  output: "htmlAndMathml" as const,
  // Bounds on what one formula may cost: sizes in em, macro expansions.
  maxSize: 20,
  maxExpand: 1000,
};

let katex: KatexApi | undefined;

/** For tests: forget the loaded KaTeX. */
export function resetMathForTests() {
  katex = undefined;
}

/** KaTeX once its chunk has loaded, or undefined while it loads (or failed to). */
function useKatex(): KatexApi | undefined {
  const [api, setApi] = useState<KatexApi | undefined>(katex);
  useEffect(() => {
    if (api) return;
    let live = true;
    loadKatex().then(
      (loaded) => {
        katex = loaded;
        if (live) setApi(() => loaded);
      },
      () => {
        // Offline or a stale chunk: the source stays on screen as code.
      },
    );
    return () => {
      live = false;
    };
  }, [api]);
  return api;
}

/**
 * The element a formula is typeset into. KaTeX builds the formula's nodes
 * itself (katex.render, DOM calls, no HTML string), so nothing of a model's
 * answer is ever parsed as markup; React owns the element and leaves its
 * children to KaTeX. A formula KaTeX cannot render calls onFail and the caller
 * shows the source instead.
 */
function Typeset(
  props: {
    api: KatexApi;
    source: string;
    display: boolean;
    onFail: () => void;
    as: "span" | "div";
  } & Omit<HTMLAttributes<HTMLElement>, "children">,
) {
  const { api, source, display, onFail, as: Tag, ...rest } = props;
  const ref = useRef<HTMLElement>(null);
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    try {
      api.render(source, el, { ...KATEX_OPTIONS, displayMode: display });
    } catch {
      el.textContent = "";
      onFail();
    }
  }, [api, source, display, onFail]);
  return <Tag ref={ref as never} {...rest} />;
}

function copyText(text: string): Promise<void> {
  return navigator.clipboard.writeText(text);
}

/** `$...$` in a sentence: typeset in place; the source is its tooltip and a click copies it. */
export function MathInline(props: { source: string }) {
  const { t } = useT();
  const api = useKatex();
  const [failedSource, setFailedSource] = useState<string | null>(null);
  const onFail = useCallback(
    () => setFailedSource(props.source),
    [props.source],
  );
  const [copied, setCopied] = useState(false);
  const timer = useRef<number | undefined>(undefined);
  useEffect(() => () => window.clearTimeout(timer.current), []);
  const delimited = `$${props.source}$`;

  const onCopy = useCallback(() => {
    copyText(delimited).then(
      () => {
        setCopied(true);
        window.clearTimeout(timer.current);
        timer.current = window.setTimeout(() => setCopied(false), 900);
      },
      () => setCopied(false),
    );
  }, [delimited]);

  const onKeyDown = useCallback(
    (e: KeyboardEvent<HTMLElement>) => {
      if (e.key === "Enter" || e.key === " ") {
        e.preventDefault();
        onCopy();
      }
    },
    [onCopy],
  );

  const title = copied
    ? t("messages.copied")
    : t("markdown.math.inlineTitle", { source: delimited });
  if (!api || failedSource === props.source) {
    return (
      <code
        className="md-math-inline md-math-pending"
        data-testid="md-math-inline"
        title={title}
      >
        {delimited}
      </code>
    );
  }
  return (
    <Typeset
      as="span"
      api={api}
      source={props.source}
      display={false}
      onFail={onFail}
      className={copied ? "md-math-inline is-copied" : "md-math-inline"}
      role="button"
      tabIndex={0}
      title={title}
      aria-label={t("markdown.math.copySource")}
      data-testid="md-math-inline"
      data-source={props.source}
      onClick={onCopy}
      onKeyDown={onKeyDown}
    />
  );
}

/** `$$...$$` or a ```math fence: a typeset block with a switch to its source and a copy button. */
export function MathBlock(props: { source: string }) {
  const { t } = useT();
  const api = useKatex();
  const [failedSource, setFailedSource] = useState<string | null>(null);
  const onFail = useCallback(
    () => setFailedSource(props.source),
    [props.source],
  );
  const [view, setView] = useState<"picture" | "code">("picture");
  const unavailable = !api || failedSource === props.source;
  const showing = unavailable ? "code" : view;
  return (
    <figure
      className="md-figure md-figure--math"
      data-testid="md-math-block"
      data-view={showing}
    >
      <div className="md-figure-head">
        <span className="md-figure-label">{t("markdown.math.label")}</span>
        <div className="md-figure-actions">
          <SourceToggle
            showingSource={showing === "code"}
            disabled={unavailable}
            onToggle={() => setView((v) => (v === "code" ? "picture" : "code"))}
          />
        </div>
      </div>
      {showing === "picture" && api ? (
        <Typeset
          as="div"
          api={api}
          source={props.source}
          display
          onFail={onFail}
          className="md-math-display"
        />
      ) : (
        <FigureSource source={props.source} copyTestId="md-math-copy" />
      )}
    </figure>
  );
}
