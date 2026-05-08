export function NavRail(props: {
  onNewChat: () => void;
  onToggleMenu: () => void;
  menuOpen: boolean;
}) {
  return (
    <aside className="rail" aria-label="Nav">
      <div className="rail-pill">
        <button
          type="button"
          className={`rail-icon ${props.menuOpen ? 'is-active' : ''}`}
          title="Menu"
          aria-label="Menu"
          data-testid="nav-menu"
          onClick={props.onToggleMenu}
        >
          <span aria-hidden="true">≡</span>
        </button>

        <button
          type="button"
          className="rail-brand"
          aria-label="Coddy chat"
          data-testid="nav-home"
          onClick={props.onNewChat}
        >
          <div className="rail-brand-text">
            <div className="rail-brand-title">Coddy</div>
            <div className="rail-brand-sub">chat</div>
          </div>
        </button>

        <div className="rail-spacer" />

        <a
          className="rail-icon rail-link"
          href="https://github.com/coddy-project/coddy-agent"
          target="_blank"
          rel="noopener"
          aria-label="GitHub"
          data-testid="nav-github"
        >
          <span aria-hidden="true">GH</span>
        </a>
        <a className="rail-icon rail-link" href="/docs/" aria-label="API docs" data-testid="nav-api-docs">
          <span aria-hidden="true">API</span>
        </a>
      </div>
    </aside>
  );
}

