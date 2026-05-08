export function ChatHeader(props: { title: string }) {
  return (
    <header className="chat-header">
      <div className="chat-title" id="chat-title">
        {props.title || 'Chat'}
      </div>
      <div className="header-links">
        <a className="repo-link" href="https://github.com/coddy-project/coddy-agent" target="_blank" rel="noopener">
          GitHub
        </a>
        <a className="repo-link" href="/docs/">
          API docs
        </a>
      </div>
    </header>
  );
}

