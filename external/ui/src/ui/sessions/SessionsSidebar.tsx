import type { SessionRow } from './types';

export function SessionsSidebar(props: {
  sessionId: string;
  sessions: SessionRow[];
  onPick: (id: string) => void;
  onRename: (id: string) => void;
  onDelete: (id: string) => void;
  onLoadMore: () => void;
}) {
  return (
    <aside className="sessions" aria-label="Sessions">
      <div className="sessions-head">Chats</div>
      <div className="session-list" id="session-list">
        {props.sessions.map((s) => (
          <div
            key={s.id}
            className={`session-item ${s.id === props.sessionId ? 'active' : ''}`}
            onClick={() => props.onPick(s.id)}
          >
            <div className="session-row">
              <span className="session-title">{s.title || s.id}</span>
              <button
                className="session-menu"
                type="button"
                onClick={(ev) => {
                  ev.stopPropagation();
                  const action = window.prompt('Action (r=rename, d=delete)', 'r');
                  if (!action) {
                    return;
                  }
                  const a = action.trim().toLowerCase();
                  if (a === 'r' || a === 'rename') {
                    props.onRename(s.id);
                    return;
                  }
                  if (a === 'd' || a === 'delete') {
                    props.onDelete(s.id);
                    return;
                  }
                }}
              >
                ...
              </button>
            </div>
          </div>
        ))}
      </div>
      <div className="sessions-foot">
        <button type="button" className="link" id="btn-load-more" onClick={props.onLoadMore}>
          Load more
        </button>
      </div>
    </aside>
  );
}

