import { useState } from 'react';
import type { TokenUsage } from './types';

function clamp01(x: number): number {
  if (!Number.isFinite(x)) return 0;
  if (x < 0) return 0;
  if (x > 1) return 1;
  return x;
}

function fmtInt(n: number | undefined): string {
  if (typeof n !== 'number' || !Number.isFinite(n)) return '0';
  return Math.max(0, Math.trunc(n)).toString();
}

export function Composer(props: {
  value: string;
  isEmpty: boolean;
  mode: string;
  modes: string[];
  tokenUsage?: TokenUsage | null;
  contextPct?: number;
  maxContextTokens?: number;
  modelLabel?: string;
  onModeChange: (mode: string) => void;
  onChange: (v: string) => void;
  onSend: (text: string) => void;
}) {
  const sendDisabled = props.value.trim() === '';
  const [modeOpen, setModeOpen] = useState(false);
  const modeLabel = (props.mode || 'agent').slice(0, 1).toUpperCase() + (props.mode || 'agent').slice(1);
  const pct = typeof props.contextPct === 'number' ? props.contextPct : null;
  const pct01 = clamp01(typeof pct === 'number' ? pct / 100 : 0);
  const r = 10;
  const c = 2 * Math.PI * r;
  const off = c * (1 - pct01);
  const usage = props.tokenUsage || null;
  const maxCtx = typeof props.maxContextTokens === 'number' && props.maxContextTokens > 0 ? props.maxContextTokens : 128000;
  const tip = [
    `${typeof pct === 'number' ? pct.toFixed(1) : '0.0'}% context used`,
    props.modelLabel ? `Model ${props.modelLabel}` : '',
    usage ? `Input ${fmtInt(usage.inputTokens)}   Output ${fmtInt(usage.outputTokens)}   Total ${fmtInt(usage.totalTokens)}` : '',
    `Max context ${fmtInt(maxCtx)}`,
  ]
    .filter(Boolean)
    .join('\n');

  return (
    <footer className="composer-wrap">
      <label className="sr-only" htmlFor="composer">
        Message
      </label>
      <div className="composer-card">
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

        <div className="composer-bar">
          <div className="composer-tabs" aria-label="Composer options">
            <div className="mode">
              <button
                type="button"
                className={`composer-tab mode-btn ${props.mode === 'plan' ? 'mode-plan' : 'mode-agent'}`}
                aria-label="Mode"
                title="Mode"
                onClick={() => setModeOpen((v) => !v)}
              >
                {modeLabel}
              </button>
              {modeOpen ? (
                <div className="mode-menu" role="menu">
                  {props.modes.map((m) => {
                    const label = m.slice(0, 1).toUpperCase() + m.slice(1);
                    return (
                    <button
                      key={m}
                      type="button"
                      role="menuitem"
                      className={`mode-item ${m === props.mode ? 'is-selected' : ''}`}
                      onClick={() => {
                        props.onModeChange(m);
                        setModeOpen(false);
                      }}
                    >
                      {label}
                    </button>
                    );
                  })}
                </div>
              ) : null}
            </div>
          </div>

          <div className="composer-bar-actions">
            <div className="context-ring" title={tip} aria-label="Context usage">
              <svg viewBox="0 0 24 24" width="22" height="22" aria-hidden="true">
                <circle className="context-ring-bg" cx="12" cy="12" r={r} />
                <circle
                  className="context-ring-fg"
                  cx="12"
                  cy="12"
                  r={r}
                  strokeDasharray={c}
                  strokeDashoffset={off}
                />
              </svg>
              <span className="context-ring-label" aria-hidden="true">
                {typeof pct === 'number' ? `${pct.toFixed(0)}%` : ''}
              </span>
            </div>
            <button
              type="button"
              className="composer-icon composer-send"
              id="btn-send"
              aria-label="Send"
              disabled={sendDisabled}
              onClick={() => {
                const txt = props.value.trim();
                if (!txt) {
                  return;
                }
                props.onSend(txt);
              }}
            >
              <span aria-hidden="true">➤</span>
            </button>
          </div>
        </div>
      </div>
    </footer>
  );
}
