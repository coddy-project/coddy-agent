export function NavRail(props: { onNewChat: () => void }) {
  return (
    <aside className="rail" aria-label="Nav">
      <button type="button" className="rail-btn" id="btn-new" title="New chat" onClick={props.onNewChat}>
        +
      </button>
    </aside>
  );
}

