import { useLayoutEffect, useRef } from "react";
import {
  appNavHrefHistory,
  appNavHrefHome,
  appNavHrefScheduler,
  appNavHrefSettings,
} from "../scheduler/hashRoute";
import { sameTabInAppNavClick } from "./sameTabInAppNav";

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

function IconSettings(props: { className?: string }) {
  return (
    <svg
      className={props.className}
      width="18"
      height="18"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
    >
      <path d="M9.594 3.94c.09-.542.56-.94 1.11-.94h2.593c.55 0 1.02.398 1.11.94l.213 1.281c.063.374.313.686.645.87.074.04.147.083.22.127.324.196.72.257 1.075.124l1.217-.456a1.125 1.125 0 011.37.49l1.296 2.247a1.125 1.125 0 01-.26 1.431l-1.003.827c-.293.24-.438.613-.431.992a6.759 6.759 0 010 .255c-.007.378.138.75.43.99l1.005.828c.424.35.534.954.26 1.43l-1.298 2.247a1.125 1.125 0 01-1.369.491l-1.217-.456c-.355-.133-.75-.072-1.076.124a6.57 6.57 0 01-.22.128c-.331.183-.581.495-.644.869l-.213 1.28c-.09.543-.56.941-1.11.941h-2.594c-.55 0-1.02-.398-1.11-.94l-.213-1.281c-.062-.374-.312-.686-.644-.87a6.52 6.52 0 01-.22-.127c-.325-.196-.72-.257-1.076-.124l-1.217.456a1.125 1.125 0 01-1.369-.49l-1.297-2.247a1.125 1.125 0 01.26-1.431l1.004-.827c.292-.24.437-.613.43-.992a6.932 6.932 0 010-.255c.007-.378-.138-.75-.43-.99l-1.004-.828a1.125 1.125 0 01-.26-1.43l1.297-2.247a1.125 1.125 0 011.37-.491l1.216.456c.356.133.751.072 1.076-.124.072-.044.146-.087.22-.128.332-.183.582-.495.644-.869l.214-1.281z" />
      <path d="M15 12a3 3 0 11-6 0 3 3 0 016 0z" />
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
  onOpenSettings: () => void;
  settingsOpen: boolean;
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
              <a
                href={appNavHrefHome()}
                className="rail-brand rail-brand-header"
                aria-label="Coddy agent home"
                data-testid="nav-home"
                onClick={(ev) =>
                  sameTabInAppNavClick(ev, props.onNewChat)
                }
              >
                <span className="rail-brand-text-header-single">
                  Coddy <span className="rail-brand-header-agent">agent</span>
                </span>
              </a>
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
                <a
                  href={appNavHrefHome()}
                  className="rail-brand"
                  aria-label="Coddy agent home"
                  data-testid="nav-home"
                  onClick={(ev) =>
                    sameTabInAppNavClick(ev, props.onNewChat)
                  }
                >
                  <span className="rail-brand-text">
                    <span className="rail-brand-title">Coddy</span>
                    <span className="rail-brand-sub">agent</span>
                  </span>
                </a>
                <span className="rail-tip" role="tooltip">
                  New Chat
                </span>
              </div>
            </>
          )
        ) : (
          <div className="rail-tip-host rail-brand-tip-host">
            <a
              href={appNavHrefHome()}
              className="rail-brand"
              aria-label="Coddy agent home"
              data-testid="nav-home"
              onClick={(ev) =>
                sameTabInAppNavClick(ev, props.onNewChat)
              }
            >
              <span className="rail-brand-text">
                <span className="rail-brand-title">Coddy</span>
                <span className="rail-brand-sub">agent</span>
              </span>
            </a>
            <span className="rail-tip" role="tooltip">
              New Chat
            </span>
          </div>
        )}

        <div className="rail-middle">
          <div className="rail-tip-host">
            <a
              href={appNavHrefHistory()}
              className={`${navBtnCls} ${props.historyOpen ? "is-active" : ""}`}
              aria-label="History"
              aria-pressed={props.historyOpen}
              data-testid="nav-history"
              onClick={(ev) =>
                sameTabInAppNavClick(ev, props.onOpenHistory)
              }
            >
              <IconBook className="rail-svg rail-nav-hit-svg" />
              {pillWide ? (
                <span className="rail-nav-label">History</span>
              ) : null}
            </a>
            {!pillWide && !props.historyOpen ? (
              <span className="rail-tip" role="tooltip">
                History
              </span>
            ) : null}
          </div>

          {showScheduler ? (
            <div className="rail-tip-host">
              <a
                href={appNavHrefScheduler()}
                className={`${navBtnCls} ${props.schedulerOpen ? "is-active" : ""}`}
                aria-label="Scheduler jobs"
                aria-pressed={props.schedulerOpen}
                data-testid="nav-scheduler"
                onClick={(ev) =>
                  sameTabInAppNavClick(ev, props.onOpenScheduler)
                }
              >
                <IconScheduler className="rail-svg rail-nav-hit-svg" />
                {pillWide ? (
                  <span className="rail-nav-label">Scheduler</span>
                ) : null}
              </a>
              {!pillWide && !props.schedulerOpen ? (
                <span className="rail-tip" role="tooltip">
                  Scheduler
                </span>
              ) : null}
            </div>
          ) : null}

          <div className="rail-spacer rail-spacer-between" aria-hidden />

          <div className="rail-tip-host">
            <a
              href={appNavHrefSettings()}
              className={`${navBtnCls} ${props.settingsOpen ? "is-active" : ""}`}
              aria-label="Settings"
              aria-pressed={props.settingsOpen}
              data-testid="nav-settings"
              onClick={(ev) =>
                sameTabInAppNavClick(ev, props.onOpenSettings)
              }
            >
              <IconSettings className="rail-svg rail-nav-hit-svg" />
              {pillWide ? (
                <span className="rail-nav-label">Settings</span>
              ) : null}
            </a>
            {!pillWide && !props.settingsOpen ? (
              <span className="rail-tip" role="tooltip">
                Settings
              </span>
            ) : null}
          </div>
        </div>
      </div>
    </aside>
  );
}
