export function SystemNoticeMessage(props: { level: 'error'; message: string }) {
  return (
    <div className={`msg msg-system msg-system-${props.level}`} role="alert">
      <div className="msg-system-label">System</div>
      <pre className="msg-system-body">{props.message}</pre>
    </div>
  );
}
