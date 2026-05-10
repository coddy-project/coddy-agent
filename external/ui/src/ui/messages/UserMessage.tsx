import { Markdown } from '../markdown/Markdown';
import { slugSlashesForUserBubbleMarkdown } from '../skills/segmentComposerSlashSpans';

export function UserMessage(props: { content: string }) {
  return (
    <div className="msg msg-user">
      <Markdown text={slugSlashesForUserBubbleMarkdown(props.content)} />
    </div>
  );
}
