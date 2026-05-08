import type { SessionRow } from './types';

export function SessionsSidebar(props: {
  sessionId: string;
  sessions: SessionRow[];
  error?: string | null;
  variant?: 'dock' | 'drawer';
  open?: boolean;
  onClose?: () => void;
  onPick: (id: string) => void;
  onRename: (id: string) => void;
  onTitleSave?: (id: string, title: string) => void;
  onDelete: (id: string) => void;
  onLoadMore: () => void;
}) {
  const variant = props.variant || 'dock';
  const isOpen = variant === 'dock' ? true : !!props.open;

  if (!isOpen) {
    return null;
  }

  return (
    <aside className={`sessions ${variant === 'drawer' ? 'drawer' : 'dock'}`} aria-label="Sessions" data-testid="sessions">
      <div className="sessions-head">
        <span>Chats</span>
        {variant === 'drawer' ? (
          <button type="button" className="sessions-close" aria-label="Close" data-testid="sessions-close" onClick={props.onClose}>
            ×
          </button>
        ) : null}
      </div>
      <div className="session-list" id="session-list">
        {props.error ? (
          <div className="sessions-empty" data-testid="sessions-error">
            {props.error}
          </div>
        ) : null}
        {!props.error && props.sessions.length === 0 ? (
          <div className="sessions-empty" data-testid="sessions-empty">
            No chats yet
          </div>
        ) : null}
        {props.sessions.map((s) => (
          <div
            key={s.id}
            className={`session-item ${s.id === props.sessionId ? 'active' : ''}`}
            onClick={() => {
              props.onPick(s.id);
              props.onClose?.();
            }}
          >
            <div className="session-row">
              <span className="session-title" title={s.title || 'New chat'}>
                {s.title || 'New chat'}
              </span>
              <button
                className="session-trash"
                type="button"
                aria-label="Delete chat"
                title="Delete"
                data-testid={`session-delete-${s.id}`}
                onMouseDown={(ev) => ev.stopPropagation()}
                onClick={() => {
                  props.onDelete(s.id);
                }}
              >
                🗑
              </button>
            </div>
          </div>
        ))}
      </div>
      <div className="sessions-foot">
        <button type="button" className="link" id="btn-load-more" data-testid="sessions-load-more" onClick={props.onLoadMore}>
          Load more
        </button>
      </div>
    </aside>
  );
}
