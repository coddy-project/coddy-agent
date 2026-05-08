export function Composer(props: {
  value: string;
  isEmpty: boolean;
  onChange: (v: string) => void;
  onSend: (text: string) => void;
}) {
  const sendDisabled = props.value.trim() === '';

  return (
    <footer className="composer-wrap">
      <label className="sr-only" htmlFor="composer">
        Message
      </label>
      <textarea
        id="composer"
        rows={props.isEmpty ? 5 : 2}
        placeholder={props.isEmpty ? 'Ask anything...' : 'Message Coddy'}
        autoComplete="off"
        value={props.value}
        onChange={(ev) => props.onChange(ev.target.value)}
        onKeyDown={(ev) => {
          if (ev.key === 'Enter' && !ev.shiftKey) {
            ev.preventDefault();
            const txt = props.value.trim();
            if (!txt) {
              return;
            }
            props.onSend(txt);
          }
        }}
      />
      <div className="composer-actions">
        <button type="button" className="pill">
          Auto
        </button>
        <button
          type="button"
          className="send"
          id="btn-send"
          disabled={sendDisabled}
          onClick={() => {
            const txt = props.value.trim();
            if (!txt) {
              return;
            }
            props.onSend(txt);
          }}
        >
          Send
        </button>
      </div>
    </footer>
  );
}

