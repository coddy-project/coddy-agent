import type { TokenUsage } from './types';

export function TokenBar(props: { usage: TokenUsage | null }) {
  if (!props.usage) {
    return null;
  }
  return (
    <div id="token-bar" className="token-bar">
      Tokens in this turn: input {props.usage.inputTokens} | output {props.usage.outputTokens} | total {props.usage.totalTokens}
    </div>
  );
}

