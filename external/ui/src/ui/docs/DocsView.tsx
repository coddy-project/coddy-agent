import {
  useCallback,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  useSyncExternalStore,
  type KeyboardEvent,
  type ReactNode,
} from "react";
import { Chevron } from "../components/Chevron";
import { useT } from "../i18n/I18nProvider";
import { Markdown } from "../markdown/Markdown";
import { appNavHrefDocs } from "../scheduler/hashRoute";
import { sameTabInAppNavClick } from "../nav/sameTabInAppNav";
import {
  serverSnapshotShellStack,
  snapshotShellStack,
  subscribeShellStack,
} from "../shellBreakpoint";
import {
  fetchDocsContents,
  fetchDocsPage,
  searchDocs,
  type DocsContents,
  type DocsHit,
  type DocsPage,
} from "./api";
import {
  addHeadingLinks,
  askDraftFor,
  assignHeadingIds,
  outlineHeadings,
  sectionAnchorAt,
  snippetText,
} from "./docsReader";

const SEARCH_DEBOUNCE_MS = 120;

/**
 * The documentation reader: Coddy's own documentation as the binary carries
 * it, read like a book. The contents on the left (a search box over them),
 * the page in the middle with the page before and after it at the foot, the
 * sections of the page on the right. "Ask the agent" opens a chat with the
 * page, or the section the selection sits in, mentioned as `@coddy:` and the
 * selection quoted.
 */
export function DocsView(props: {
  /** The page to show; null shows the first page of the contents. */
  slug: string | null;
  anchor: string | null;
  /** Moves the reader, as following a link does. */
  onOpen: (slug: string, anchor?: string | null, opts?: { replace?: boolean }) => void;
  /** Starts a chat with a draft; absent where there is no chat to start. */
  onAsk?: (draft: string) => void;
  onClose?: () => void;
  headerSlot?: ReactNode;
}) {
  const { t } = useT();
  const { slug, anchor, onOpen } = props;
  const narrow = useSyncExternalStore(
    subscribeShellStack,
    snapshotShellStack,
    serverSnapshotShellStack,
  );
  const [contents, setContents] = useState<DocsContents | null>(null);
  const [page, setPage] = useState<DocsPage | null>(null);
  const [error, setError] = useState("");
  const [query, setQuery] = useState("");
  const [hits, setHits] = useState<DocsHit[] | null>(null);
  const [hitIndex, setHitIndex] = useState(0);
  const [tocOpen, setTocOpen] = useState(false);
  const [selection, setSelection] = useState("");
  const articleRef = useRef<HTMLElement | null>(null);
  const searchRef = useRef<HTMLInputElement | null>(null);

  useEffect(() => {
    const ac = new AbortController();
    void fetchDocsContents(ac.signal)
      .then((res) => {
        if (res.ok) {
          setContents(res.data);
        } else {
          setError(res.message);
        }
      })
      .catch(() => {});
    return () => ac.abort();
  }, []);

  // No page named: the reader opens on the first page of the contents.
  useEffect(() => {
    if (!slug && contents) {
      const first = contents.groups[0]?.pages[0];
      if (first) {
        onOpen(first.slug, null, { replace: true });
      }
    }
  }, [slug, contents, onOpen]);

  useEffect(() => {
    if (!slug) {
      return undefined;
    }
    const ac = new AbortController();
    void fetchDocsPage(slug, ac.signal)
      .then((res) => {
        if (res.ok) {
          setPage(res.data);
          setError("");
        } else {
          setError(res.message);
        }
      })
      .catch(() => {});
    setTocOpen(false);
    return () => ac.abort();
  }, [slug]);

  // Headings get the anchors the server computed, then the reader scrolls to
  // the section the address names, or to the top of a page just opened.
  useLayoutEffect(() => {
    const root = articleRef.current;
    if (!root || !page || page.slug !== slug) {
      return;
    }
    assignHeadingIds(root, page.headings);
    addHeadingLinks(root, page.slug, (heading) => t("docs.anchor.label", { heading }));
    const target = anchor ? root.querySelector(`[id="${CSS.escape(anchor)}"]`) : null;
    if (target instanceof HTMLElement) {
      target.scrollIntoView?.({ block: "start" });
    } else {
      root.closest(".docs-dock-cluster")?.scrollTo?.({ top: 0 });
    }
  }, [page, slug, anchor, t]);

  useEffect(() => {
    if (!narrow) {
      searchRef.current?.focus();
    }
  }, [narrow]);

  // "/" jumps to the search box, as it does on documentation sites.
  useEffect(() => {
    const onKey = (e: globalThis.KeyboardEvent) => {
      if (e.key !== "/" || e.ctrlKey || e.metaKey || e.altKey) {
        return;
      }
      const el = document.activeElement;
      if (
        el instanceof HTMLInputElement ||
        el instanceof HTMLTextAreaElement ||
        (el instanceof HTMLElement && el.isContentEditable)
      ) {
        return;
      }
      e.preventDefault();
      searchRef.current?.focus();
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);

  // The search runs as the query is typed, the last answer winning.
  useEffect(() => {
    const q = query.trim();
    if (!q) {
      setHits(null);
      return undefined;
    }
    const ac = new AbortController();
    const timer = window.setTimeout(() => {
      void searchDocs(q, ac.signal)
        .then((res) => {
          setHits(res.ok ? res.data : []);
          setHitIndex(0);
        })
        .catch(() => {});
    }, SEARCH_DEBOUNCE_MS);
    return () => {
      window.clearTimeout(timer);
      ac.abort();
    };
  }, [query]);

  // What the reader selected on the page: "Ask the agent" quotes it.
  useEffect(() => {
    const onChange = () => {
      const sel = document.getSelection();
      const root = articleRef.current;
      if (!sel || sel.isCollapsed || !root || !sel.anchorNode || !root.contains(sel.anchorNode)) {
        setSelection("");
        return;
      }
      setSelection(sel.toString());
    };
    document.addEventListener("selectionchange", onChange);
    return () => document.removeEventListener("selectionchange", onChange);
  }, []);

  const ask = useCallback(() => {
    if (!page || !props.onAsk) {
      return;
    }
    const sel = document.getSelection();
    const root = articleRef.current;
    let section = anchor;
    if (selection && sel && sel.anchorNode && root) {
      section = sectionAnchorAt(root, sel.anchorNode);
    }
    props.onAsk(askDraftFor(page.slug, section, selection));
  }, [page, props, anchor, selection]);

  const openHit = useCallback(
    (hit: DocsHit | undefined) => {
      if (hit) {
        onOpen(hit.slug, hit.anchor || null);
      }
    },
    [onOpen],
  );

  const onSearchKey = (e: KeyboardEvent<HTMLInputElement>) => {
    if (e.key === "Escape") {
      e.preventDefault();
      setQuery("");
      return;
    }
    if (!hits || hits.length === 0) {
      return;
    }
    if (e.key === "ArrowDown") {
      e.preventDefault();
      setHitIndex((i) => Math.min(hits.length - 1, i + 1));
    } else if (e.key === "ArrowUp") {
      e.preventDefault();
      setHitIndex((i) => Math.max(0, i - 1));
    } else if (e.key === "Enter") {
      e.preventDefault();
      openHit(hits[hitIndex]);
    }
  };

  // The page being read stays on screen until the next one arrives.
  const shown = page;
  const outline = shown ? outlineHeadings(shown.headings) : [];

  return (
    <section className="docs-view" data-testid="docs-view" aria-label={t("docs.title")}>
      <header className="docs-header">
        <div className="docs-title-block">
          <h1 className="docs-title">{t("docs.title")}</h1>
          <p className="docs-subtitle">
            {contents ? t("docs.subtitle", { version: contents.version }) : t("docs.loading")}
          </p>
        </div>
        <div className="docs-header-actions">
          {shown && props.onAsk ? (
            <button
              type="button"
              className="docs-ask"
              data-testid="docs-ask"
              // Keep the selection the reader made: a click would clear it.
              onMouseDown={(e) => e.preventDefault()}
              onClick={ask}
            >
              {selection ? t("docs.ask.selection") : t("docs.ask.page")}
            </button>
          ) : null}
          {props.headerSlot}
          {props.onClose ? (
            <button
              type="button"
              className="sessions-close"
              data-testid="docs-close"
              aria-label={t("docs.close")}
              title={t("docs.close")}
              onClick={props.onClose}
            >
              ×
            </button>
          ) : null}
        </div>
      </header>

      {error ? (
        <p className="docs-error" data-testid="docs-error">
          {t("docs.error", { message: error })}
        </p>
      ) : null}

      <div className="docs-layout">
        <aside className={`docs-sidebar${tocOpen ? " is-open" : ""}`}>
          <input
            ref={searchRef}
            className="docs-search"
            data-testid="docs-search"
            type="search"
            placeholder={t("docs.search.placeholder")}
            aria-label={t("docs.search.placeholder")}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={onSearchKey}
          />
          {query.trim() ? (
            hits === null ? null : hits.length === 0 ? (
              <p className="docs-empty" data-testid="docs-search-empty">
                {t("docs.search.empty")}
              </p>
            ) : (
              <ul className="docs-hits" data-testid="docs-hits" aria-label={t("docs.search.label")}>
                {hits.map((h, i) => (
                  <li key={`${h.slug}#${h.anchor || ""}`}>
                    <a
                      href={appNavHrefDocs(h.slug, h.anchor)}
                      className={`docs-hit${i === hitIndex ? " is-selected" : ""}`}
                      title={snippetText(h.snippet)}
                      onClick={(ev) => sameTabInAppNavClick(ev, () => openHit(h))}
                    >
                      <span className="docs-hit-title">
                        {h.title}
                        {h.heading ? <span className="docs-hit-heading"> › {h.heading}</span> : null}
                      </span>
                      <span className="docs-hit-snippet">
                        {h.snippet.map((f, k) =>
                          f.hit ? <mark key={k}>{f.text}</mark> : <span key={k}>{f.text}</span>,
                        )}
                      </span>
                    </a>
                  </li>
                ))}
              </ul>
            )
          ) : (
            <>
              <button
                type="button"
                className="docs-toc-toggle"
                aria-expanded={tocOpen}
                onClick={() => setTocOpen((v) => !v)}
              >
                <span>{t("docs.toc.label")}</span>
                <Chevron pointing="down" open={tocOpen} />
              </button>
              <nav className="docs-toc" aria-label={t("docs.toc.label")} data-testid="docs-toc">
                {contents?.groups.map((g) => (
                  <div key={g.id} className="docs-toc-group">
                    <div className="docs-toc-group-title">{g.title}</div>
                    <ul>
                      {g.pages.map((p) => (
                        <li key={p.slug}>
                          <a
                            href={appNavHrefDocs(p.slug)}
                            className={`docs-toc-page${p.slug === slug ? " is-active" : ""}`}
                            aria-current={p.slug === slug ? "page" : undefined}
                            title={p.summary}
                            onClick={(ev) => sameTabInAppNavClick(ev, () => onOpen(p.slug))}
                          >
                            {p.title}
                          </a>
                        </li>
                      ))}
                    </ul>
                  </div>
                ))}
              </nav>
            </>
          )}
        </aside>

        <article className="docs-article" ref={articleRef} data-testid="docs-article">
          {shown ? (
            <>
              <p className="docs-breadcrumb">
                <span>
                  {shown.group.title} › {shown.title}
                </span>
                <a
                  className="docs-site-link"
                  href={shown.url}
                  target="_blank"
                  rel="noreferrer noopener"
                  title={t("docs.site.title")}
                >
                  {t("docs.site.label")} ↗
                </a>
              </p>
              {/* A fresh tree per page: the heading links added to it are
                  never left on a heading of the next page. */}
              <Markdown key={shown.slug} text={shown.markdown} />
              <nav className="docs-pager" aria-label={t("docs.pager.label")}>
                {shown.prev ? (
                  <a
                    href={appNavHrefDocs(shown.prev.slug)}
                    className="docs-pager-link docs-pager-prev"
                    data-testid="docs-prev"
                    onClick={(ev) => sameTabInAppNavClick(ev, () => onOpen(shown.prev!.slug))}
                  >
                    <span className="docs-pager-dir">{t("docs.pager.prev")}</span>
                    <span className="docs-pager-title">{shown.prev.title}</span>
                  </a>
                ) : (
                  <span />
                )}
                {shown.next ? (
                  <a
                    href={appNavHrefDocs(shown.next.slug)}
                    className="docs-pager-link docs-pager-next"
                    data-testid="docs-next"
                    onClick={(ev) => sameTabInAppNavClick(ev, () => onOpen(shown.next!.slug))}
                  >
                    <span className="docs-pager-dir">{t("docs.pager.next")}</span>
                    <span className="docs-pager-title">{shown.next.title}</span>
                  </a>
                ) : null}
              </nav>
            </>
          ) : error ? null : (
            <p className="docs-empty">{t("docs.loading")}</p>
          )}
        </article>

        {outline.length > 0 ? (
          <aside className="docs-outline" aria-label={t("docs.outline.label")}>
            <div className="docs-outline-title">{t("docs.outline.label")}</div>
            <ul>
              {outline.map((h) => (
                <li key={h.anchor} className={`docs-outline-l${h.level}`}>
                  <a
                    href={appNavHrefDocs(shown!.slug, h.anchor)}
                    className={h.anchor === anchor ? "is-active" : undefined}
                    onClick={(ev) => sameTabInAppNavClick(ev, () => onOpen(shown!.slug, h.anchor))}
                  >
                    {h.text}
                  </a>
                </li>
              ))}
            </ul>
          </aside>
        ) : null}
      </div>
    </section>
  );
}
