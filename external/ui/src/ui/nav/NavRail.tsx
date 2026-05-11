import { useLayoutEffect, useRef } from "react";

/** Octicon-style mark, integer geometry (reads clearly at 18px). */
function IconGitHub(props: { className?: string }) {
  return (
    <svg
      className={props.className}
      width="18"
      height="18"
      viewBox="0 0 24 24"
      fill="currentColor"
      aria-hidden
    >
      <path
        fillRule="evenodd"
        clipRule="evenodd"
        d="M12 2C6.477 2 2 6.477 2 12c0 4.419 2.865 8.166 6.839 9.489.5.092.682-.217.682-.482 0-.237-.008-.866-.013-1.7-2.782.604-3.369-1.341-3.369-1.341-.454-1.155-1.11-1.463-1.11-1.463-.908-.62.069-.608.069-.608 1.003.07 1.531 1.032 1.531 1.032.892 1.529 2.341 1.087 2.91.831.092-.646.35-1.086.636-1.336-2.22-.253-4.555-1.113-4.555-4.951 0-1.093.39-1.988 1.029-2.688-.103-.253-.446-1.272.098-2.65 0 0 .84-.27 2.75 1.026A9.564 9.564 0 0112 6.844a9.59 9.59 0 012.504.337c1.909-1.296 2.748-1.027 2.748-1.027.546 1.379.202 2.398.1 2.651.64.7 1.028 1.595 1.028 2.688 0 3.848-2.339 4.695-4.566 4.943.359.309.678.919.678 1.855 0 1.338-.012 2.419-.012 2.747 0 .268.18.579.688.481A10.02 10.02 0 0022 12c0-5.523-4.477-10-10-10z"
      />
    </svg>
  );
}

function IconBook(props: { className?: string }) {
  return (
    <svg
      className={props.className}
      width="18"
      height="18"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.75"
      aria-hidden
    >
      <path
        d="M4 19.5A2.5 2.5 0 016.5 17H20"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
      <path
        d="M6.5 2H20v20H6.5A2.5 2.5 0 014 19.5v-15A2.5 2.5 0 016.5 2z"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
      <path d="M8 7h8M8 11h6" strokeLinecap="round" />
    </svg>
  );
}

function IconScheduler(props: { className?: string }) {
  return (
    <svg
      className={props.className}
      width="18"
      height="18"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.75"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
    >
      <circle cx="12" cy="12" r="8" />
      <path d="M12 8v4l2.5 2.5" />
    </svg>
  );
}

function IconApi(props: { className?: string }) {
  return (
    <svg
      className={props.className}
      width="18"
      height="18"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.75"
      aria-hidden
    >
      <path
        d="M14 7h2a5 5 0 015 5v0a5 5 0 01-5 5h-2M10 17H8A5 5 0 013 12v0a5 5 0 015-5h2M8 12h8"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

/** Three equal-width lines (symmetric hamburger) for collapse wide rail; not a global app drawer. */
function IconSidebarCollapse(props: { className?: string }) {
  return (
    <svg
      className={props.className}
      width="18"
      height="18"
      viewBox="0 0 20 20"
      fill="none"
      aria-hidden
    >
      <path
        d="M3.5 5.5h13M3.5 10h13M3.5 14.5h13"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeLinecap="round"
      />
    </svg>
  );
}

/** Open wide rail again (narrow column at xl). */
function IconSidebarExpand(props: { className?: string }) {
  return (
    <svg
      className={props.className}
      width="18"
      height="18"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.75"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
    >
      <path d="M11 7l5 5-5 5M6 7l5 5-5 5" />
    </svg>
  );
}

export function NavRail(props: {
  onNewChat: () => void;
  onOpenHistory: () => void;
  historyOpen: boolean;
  /** When false, hide Scheduler (binary built without scheduler HTTP routes). Default true for tests. */
  showScheduler?: boolean;
  onOpenScheduler: () => void;
  schedulerOpen: boolean;
  canWidenRail: boolean;
  railLabelsWide: boolean;
  onToggleRailLabels: () => void;
}) {
  const railRef = useRef<HTMLElement | null>(null);
  useLayoutEffect(() => {
    const el = railRef.current;
    const shell = el?.closest(".shell");
    if (!el || !(shell instanceof HTMLElement)) {
      return undefined;
    }
    const syncTrack = () => {
      shell.style.setProperty(
        "--rail-shell-track-width",
        `${el.offsetWidth}px`,
      );
    };
    syncTrack();
    const ro = new ResizeObserver(syncTrack);
    ro.observe(el);
    return () => {
      ro.disconnect();
      shell.style.removeProperty("--rail-shell-track-width");
    };
  }, [props.canWidenRail, props.railLabelsWide]);

  const showScheduler = props.showScheduler !== false;
  const pillWide = props.canWidenRail && props.railLabelsWide;
  const navBtnCls = pillWide
    ? "rail-hit rail-nav-hit rail-nav-hit-wide"
    : "rail-hit rail-hit-icon rail-nav-hit rail-nav-hit-narrow";
  const navLinkCls = pillWide
    ? "rail-hit rail-nav-hit rail-nav-hit-wide rail-link"
    : "rail-hit rail-hit-link rail-nav-hit rail-nav-hit-narrow rail-link";

  return (
    <aside
      ref={railRef}
      className={`rail-column ${pillWide ? "rail-column-wide" : ""}`}
      aria-label="Nav"
    >
      <div className={`rail-pill ${pillWide ? "is-wide" : ""}`}>
        {props.canWidenRail ? (
          pillWide ? (
            <div className="rail-header rail-header-wide">
              <button
                type="button"
                className="rail-toggle-width"
                onClick={props.onToggleRailLabels}
                aria-label="Use narrow sidebar"
              >
                <IconSidebarCollapse className="rail-toggle-svg" />
              </button>
              <button
                type="button"
                className="rail-brand rail-brand-header"
                aria-label="Coddy agent home"
                data-testid="nav-home"
                onClick={props.onNewChat}
              >
                <span className="rail-brand-text-header-single">
                  Coddy <span className="rail-brand-header-agent">agent</span>
                </span>
              </button>
            </div>
          ) : (
            <>
              <div className="rail-row rail-row-toggle rail-tip-host">
                <button
                  type="button"
                  className="rail-toggle-width"
                  onClick={props.onToggleRailLabels}
                  aria-label="Use wide sidebar"
                >
                  <IconSidebarExpand className="rail-toggle-svg" />
                </button>
                <span className="rail-tip" role="tooltip">
                  Wide sidebar
                </span>
              </div>
              <div className="rail-tip-host rail-brand-tip-host">
                <button
                  type="button"
                  className="rail-brand"
                  aria-label="Coddy agent home"
                  data-testid="nav-home"
                  onClick={props.onNewChat}
                >
                  <span className="rail-brand-text">
                    <span className="rail-brand-title">Coddy</span>
                    <span className="rail-brand-sub">agent</span>
                  </span>
                </button>
                <span className="rail-tip" role="tooltip">
                  New Chat
                </span>
              </div>
            </>
          )
        ) : (
          <div className="rail-tip-host rail-brand-tip-host">
            <button
              type="button"
              className="rail-brand"
              aria-label="Coddy agent home"
              data-testid="nav-home"
              onClick={props.onNewChat}
            >
              <span className="rail-brand-text">
                <span className="rail-brand-title">Coddy</span>
                <span className="rail-brand-sub">agent</span>
              </span>
            </button>
            <span className="rail-tip" role="tooltip">
              New Chat
            </span>
          </div>
        )}

        <div className="rail-middle">
          <div className="rail-tip-host">
            <button
              type="button"
              className={`${navBtnCls} ${props.historyOpen ? "is-active" : ""}`}
              aria-label="History"
              aria-pressed={props.historyOpen}
              data-testid="nav-history"
              onClick={props.onOpenHistory}
            >
              <IconBook className="rail-svg rail-nav-hit-svg" />
              {pillWide ? (
                <span className="rail-nav-label">History</span>
              ) : null}
            </button>
            {!pillWide && !props.historyOpen ? (
              <span className="rail-tip" role="tooltip">
                History
              </span>
            ) : null}
          </div>

          {showScheduler ? (
            <div className="rail-tip-host">
              <button
                type="button"
                className={`${navBtnCls} ${props.schedulerOpen ? "is-active" : ""}`}
                aria-label="Scheduler jobs"
                aria-pressed={props.schedulerOpen}
                data-testid="nav-scheduler"
                onClick={props.onOpenScheduler}
              >
                <IconScheduler className="rail-svg rail-nav-hit-svg" />
                {pillWide ? (
                  <span className="rail-nav-label">Scheduler</span>
                ) : null}
              </button>
              {!pillWide && !props.schedulerOpen ? (
                <span className="rail-tip" role="tooltip">
                  Scheduler
                </span>
              ) : null}
            </div>
          ) : null}

          <div className="rail-spacer rail-spacer-between" aria-hidden />

          <div className="rail-link-stack">
            <div className="rail-tip-host">
              <a
                className={navLinkCls}
                href="https://github.com/coddy-project/coddy-agent"
                target="_blank"
                rel="noopener"
                aria-label="GitHub repository"
                data-testid="nav-github"
              >
                <IconGitHub className="rail-svg rail-nav-hit-svg" />
                {pillWide ? (
                  <span className="rail-nav-label">GitHub</span>
                ) : null}
              </a>
              {!pillWide ? (
                <span className="rail-tip" role="tooltip">
                  GitHub
                </span>
              ) : null}
            </div>

            <div className="rail-tip-host">
              <a
                className={navLinkCls}
                href="/docs/"
                target="_blank"
                rel="noopener"
                aria-label="API documentation"
                data-testid="nav-api-docs"
              >
                <IconApi className="rail-svg rail-nav-hit-svg" />
                {pillWide ? (
                  <span className="rail-nav-label">API docs</span>
                ) : null}
              </a>
              {!pillWide ? (
                <span className="rail-tip" role="tooltip">
                  API docs
                </span>
              ) : null}
            </div>
          </div>
        </div>
      </div>
    </aside>
  );
}
