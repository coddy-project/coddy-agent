import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import type { TokenUsage } from './types';
import { slashMenuDraftAtCaret } from '../skills/draftSlash';
import { segmentComposerSlashSpans } from '../skills/segmentComposerSlashSpans';

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

/** Short label for **`models[].model`** ids (Coddy profile IDs use displayMode elsewhere). */
function displayLlmId(id: string): string {
  const m = id || '';
  const i = m.lastIndexOf('/');
  if (i >= 0 && i < m.length - 1) {
    return m.slice(i + 1);
  }
  return m || 'Model';
}

type SlashRow = { name: string; description: string };

/** Floating slash menu anchored to **`composer-field-wrap`** (viewport-relative). */
type SlashFloatRect = { left: number; width: number; bottom: number; maxH: number };

export function Composer(props: {
  value: string;
  isEmpty: boolean;
  /** When set, slash command requests send X-Coddy-Session-ID for cwd-scoped skills. */
  sessionId?: string;
  mode: string;
  modes: string[];
  /** Configured backends (`owned_by` != **`coddy`**). Omitted when empty. */
  llmModels?: string[];
  /** Selected **`models[].model`** id (`metadata.model` on profile requests). */
  llmModel?: string;
  onLlmModelChange?: (modelId: string) => void;
  /** Pristine home (no session). Ring stays empty; tooltip does not imply usage. */
  contextIdle?: boolean;
  tokenUsage?: TokenUsage | null;
  contextPct?: number;
  maxContextTokens?: number;
  onModeChange: (mode: string) => void;
  onChange: (v: string) => void;
  onSend: (text: string) => void;
  generating?: boolean;
  onStop?: () => void;
}) {
  const idleSendDisabled = props.value.trim() === '';
  const [menuOpen, setMenuOpen] = useState<'mode' | 'llm' | null>(null);

  const taRef = useRef<HTMLTextAreaElement | null>(null);
  const composerFieldWrapRef = useRef<HTMLDivElement | null>(null);
  const mirrorInnerRef = useRef<HTMLDivElement | null>(null);
  const [composerScrollTop, setComposerScrollTop] = useState(0);
  const debounceSlashRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const [mobileUi, setMobileUi] = useState(false);
  const [slashItems, setSlashItems] = useState<SlashRow[]>([]);
  const [slashOpen, setSlashOpen] = useState(false);
  const [slashPrefix, setSlashPrefix] = useState('');
  const [slashLoading, setSlashLoading] = useState(false);
  const [slashErr, setSlashErr] = useState<string | null>(null);
  const [slashPage, setSlashPage] = useState(1);
  const [slashHasMore, setSlashHasMore] = useState(false);
  const [slashReplace, setSlashReplace] = useState<{ from: number; to: number } | null>(null);
  const [slashFloatRect, setSlashFloatRect] = useState<SlashFloatRect | null>(null);

  const measureSlashFloat = useCallback(() => {
    if (!slashOpen || mobileUi) {
      setSlashFloatRect(null);
      return;
    }
    const el = composerFieldWrapRef.current;
    if (!el) {
      setSlashFloatRect(null);
      return;
    }
    const r = el.getBoundingClientRect();
    if (r.width < 8) {
      setSlashFloatRect(null);
      return;
    }
    const maxH = Math.min(260, Math.round(window.innerHeight * 0.42));
    setSlashFloatRect({
      left: r.left,
      width: r.width,
      bottom: window.innerHeight - r.top + 8,
      maxH,
    });
  }, [slashOpen, mobileUi]);

  useLayoutEffect(() => {
    if (!slashOpen || mobileUi) {
      setSlashFloatRect(null);
      return;
    }
    measureSlashFloat();
    const el = composerFieldWrapRef.current;
    let ro: ResizeObserver | null = null;
    if (typeof ResizeObserver !== 'undefined' && el) {
      ro = new ResizeObserver(() => measureSlashFloat());
      ro.observe(el);
    }
    window.addEventListener('resize', measureSlashFloat);
    const msgEl = typeof document !== 'undefined' ? document.getElementById('messages') : null;
    const onMsgs = () => measureSlashFloat();
    msgEl?.addEventListener('scroll', onMsgs, { passive: true });
    return () => {
      ro?.disconnect();
      window.removeEventListener('resize', measureSlashFloat);
      msgEl?.removeEventListener('scroll', onMsgs);
    };
  }, [slashOpen, mobileUi, measureSlashFloat, props.isEmpty]);

  useEffect(() => {
    const mq = window.matchMedia('(max-width: 720px)');
    const apply = () => setMobileUi(mq.matches);
    apply();
    mq.addEventListener('change', apply);
    return () => mq.removeEventListener('change', apply);
  }, []);

  const cancelSlashDebounce = () => {
    if (debounceSlashRef.current) {
      clearTimeout(debounceSlashRef.current);
      debounceSlashRef.current = null;
    }
  };

  const fetchSlashPage = useCallback(
    async (prefix: string, page: number) => {
      const sp = new URLSearchParams({
        page: String(page),
        page_size: '30',
      });
      if (prefix) {
        sp.set('prefix', prefix);
      }
      const headers: Record<string, string> = {};
      const sid = (props.sessionId || '').trim();
      if (sid) {
        headers['X-Coddy-Session-ID'] = sid;
      }
      const res = await fetch(`/coddy/slash-commands?${sp.toString()}`, { headers });
      if (!res.ok) {
        throw new Error(`HTTP ${res.status}`);
      }
      return (await res.json()) as {
        items: SlashRow[];
        has_more: boolean;
        page: number;
      };
    },
    [props.sessionId],
  );

  const updateSlashMenu = useCallback(
    (value: string, caret: number) => {
      const draft = slashMenuDraftAtCaret(value, caret);
      if (!draft.open) {
        cancelSlashDebounce();
        setSlashOpen(false);
        setSlashReplace(null);
        return;
      }
      setSlashOpen(true);
      setSlashReplace({ from: draft.slashIdx, to: draft.caret });
      setSlashPrefix(draft.prefix);
      cancelSlashDebounce();
      debounceSlashRef.current = setTimeout(() => {
        debounceSlashRef.current = null;
        void (async () => {
          setSlashLoading(true);
          setSlashErr(null);
          try {
            const body = await fetchSlashPage(draft.prefix, 1);
            setSlashItems(body.items || []);
            setSlashPage(1);
            setSlashHasMore(!!body.has_more);
          } catch (e) {
            setSlashErr(e instanceof Error ? e.message : 'request failed');
            setSlashItems([]);
            setSlashHasMore(false);
          } finally {
            setSlashLoading(false);
          }
        })();
      }, 100);
    },
    [fetchSlashPage],
  );

  useEffect(() => () => cancelSlashDebounce(), []);

  const maskComposerText = props.value.length > 0;
  const composerSegments = useMemo(() => segmentComposerSlashSpans(props.value), [props.value]);

  const adjustMirrorToTextarea = useCallback(() => {
    const ta = taRef.current;
    const inner = mirrorInnerRef.current;
    if (!ta || !inner) {
      return;
    }
    const sw = Math.max(0, ta.offsetWidth - ta.clientWidth);
    inner.style.paddingRight = `${16 + sw}px`;
    inner.style.minHeight = `${Math.max(ta.clientHeight, ta.scrollHeight)}px`;
    setComposerScrollTop(ta.scrollTop);
  }, []);

  useLayoutEffect(() => {
    if (!maskComposerText) {
      setComposerScrollTop(0);
      return;
    }
    adjustMirrorToTextarea();
  }, [props.value, maskComposerText, props.isEmpty, adjustMirrorToTextarea]);

  useEffect(() => {
    if (!maskComposerText) {
      return;
    }
    const ta = taRef.current;
    if (!ta) {
      return;
    }
    const ro = new ResizeObserver(() => adjustMirrorToTextarea());
    ro.observe(ta);
    return () => ro.disconnect();
  }, [maskComposerText, adjustMirrorToTextarea]);

  function syncComposerScroll() {
    const ta = taRef.current;
    if (!ta || !maskComposerText) {
      return;
    }
    setComposerScrollTop(ta.scrollTop);
  }

  const applySlashChoice = (name: string) => {
    if (!slashReplace) {
      return;
    }
    const { from, to } = slashReplace;
    const insert = `/${name} `;
    const next = props.value.slice(0, from) + insert + props.value.slice(to);
    props.onChange(next);
    setSlashOpen(false);
    setSlashReplace(null);
    cancelSlashDebounce();
    requestAnimationFrame(() => {
      const el = taRef.current;
      if (!el) {
        return;
      }
      const pos = from + insert.length;
      el.focus();
      el.setSelectionRange(pos, pos);
    });
  };

  const loadMoreSlash = () => {
    if (!slashOpen || slashLoading || !slashHasMore) {
      return;
    }
    void (async () => {
      setSlashLoading(true);
      setSlashErr(null);
      try {
        const nextPage = slashPage + 1;
        const body = await fetchSlashPage(slashPrefix, nextPage);
        setSlashItems((prev) => [...prev, ...(body.items || [])]);
        setSlashPage(nextPage);
        setSlashHasMore(!!body.has_more);
      } catch (e) {
        setSlashErr(e instanceof Error ? e.message : 'request failed');
      } finally {
        setSlashLoading(false);
      }
    })();
  };
  const llmList = props.llmModels ?? [];
  const showLlm = llmList.length > 0;
  const llmVal = (props.llmModel || '').trim();

  function displayMode(id: string): string {
    const m = id || 'agent';
    if (m === 'plan' || m === 'agent') {
      return m.slice(0, 1).toUpperCase() + m.slice(1);
    }
    const i = m.lastIndexOf('/');
    if (i >= 0 && i < m.length - 1) {
      return m.slice(i + 1);
    }
    return m;
  }
  const modeLabel = displayMode(props.mode || 'agent');
  const llmLabel = llmVal ? displayLlmId(llmVal) : 'Model';
  const contextIdle = props.contextIdle === true;
  const pctRaw = typeof props.contextPct === 'number' ? props.contextPct : null;
  const pct = contextIdle ? null : pctRaw;
  const pct01 = contextIdle ? 0 : clamp01(typeof pct === 'number' ? pct / 100 : 0);
  const r = 12;
  const vb = 28;
  const cx = vb / 2;
  const c = 2 * Math.PI * r;
  const off = c * (1 - pct01);
  const usage = contextIdle ? null : props.tokenUsage || null;
  const maxCtx = typeof props.maxContextTokens === 'number' && props.maxContextTokens > 0 ? props.maxContextTokens : 128000;
  const modeMenuDirClass = props.isEmpty ? 'opens-down' : 'opens-up';
  const tip = contextIdle
    ? ['No context usage yet', `Max context ${fmtInt(maxCtx)}`].join('\n')
    : [
        `${typeof pct === 'number' ? pct.toFixed(1) : '0.0'}% context used`,
        usage ? `Input ${fmtInt(usage.inputTokens)}   Output ${fmtInt(usage.outputTokens)}   Total ${fmtInt(usage.totalTokens)}` : '',
        `Max context ${fmtInt(maxCtx)}`,
      ]
        .filter(Boolean)
        .join('\n');

  const slashMenuChrome = (
    <>
      <div className="slash-menu-surface" aria-hidden />
      <div className="slash-menu-scroll" style={{ maxHeight: slashFloatRect?.maxH }}>
        <div className="slash-menu-title">Skills</div>
        {slashLoading && slashItems.length === 0 ? <div className="slash-muted">Loading…</div> : null}
        {slashErr ? <div className="slash-err">{slashErr}</div> : null}
        {!slashLoading && slashItems.length === 0 && !slashErr ? <div className="slash-muted">No commands</div> : null}
        <ul className="slash-rows">
          {slashItems.map((row) => (
            <li key={row.name}>
              <button
                type="button"
                role="option"
                className="slash-row-btn"
                data-testid={`slash-command-row-${row.name}`}
                onMouseDown={(e) => {
                  e.preventDefault();
                  applySlashChoice(row.name);
                }}
              >
                <span className="slash-row-name">/{row.name}</span>
                <span className="slash-row-desc">{row.description}</span>
              </button>
            </li>
          ))}
        </ul>
        {slashHasMore ? (
          <button
            type="button"
            className="slash-load-more"
            disabled={slashLoading}
            data-testid="slash-command-more"
            onMouseDown={(e) => e.preventDefault()}
            onClick={() => loadMoreSlash()}
          >
            {slashLoading ? 'Loading…' : 'More'}
          </button>
        ) : null}
      </div>
    </>
  );

  return (
    <>
      <footer className={['composer-wrap', props.isEmpty ? '' : 'composer-wrap-docked'].filter(Boolean).join(' ')}>
      <label className="sr-only" htmlFor="composer">
        Message
      </label>
      <div className="composer-card">
        {slashOpen && mobileUi ? (
          <button
            type="button"
            className="slash-sheet-backdrop"
            aria-label="Close slash commands"
            onMouseDown={(e) => {
              e.preventDefault();
              setSlashOpen(false);
              setSlashReplace(null);
              cancelSlashDebounce();
            }}
          />
        ) : null}
        <div className="composer-field-wrap" ref={composerFieldWrapRef}>
          <div className="composer-stack">
            {maskComposerText ? (
              <div className="composer-mirror" aria-hidden="true">
                <div
                  ref={mirrorInnerRef}
                  className="composer-mirror-inner"
                  style={{ transform: `translateY(-${composerScrollTop}px)` }}
                >
                  {composerSegments.map((seg, idx) =>
                    seg.type === 'text' ? (
                      <span key={idx}>{seg.value}</span>
                    ) : (
                      <span
                        key={idx}
                        className="composer-skill-chip-inline"
                        data-testid="composer-skill-chip"
                        data-skill-name={seg.name}
                      >
                        {seg.literal}
                      </span>
                    ),
                  )}
                </div>
              </div>
            ) : null}
            <textarea
              ref={taRef}
              id="composer"
              className={maskComposerText ? 'composer-ta-masked' : undefined}
              rows={props.isEmpty ? 5 : 2}
              placeholder={props.isEmpty ? 'Ask anything...' : 'Message Coddy'}
              autoComplete="off"
              value={props.value}
              onChange={(ev) => {
                const v = ev.target.value;
                const caret = ev.target.selectionStart ?? v.length;
                props.onChange(v);
                updateSlashMenu(v, caret);
              }}
              onScroll={() => syncComposerScroll()}
              onKeyUp={(ev) => {
                const el = taRef.current;
                if (!el) {
                  return;
                }
                if (ev.key === 'ArrowLeft' || ev.key === 'ArrowRight' || ev.key === 'Home' || ev.key === 'End') {
                  updateSlashMenu(props.value, el.selectionStart);
                }
              }}
              onSelect={() => {
                const el = taRef.current;
                if (el) {
                  updateSlashMenu(props.value, el.selectionStart);
                  syncComposerScroll();
                }
              }}
              onClick={() => {
                const el = taRef.current;
                if (el) {
                  updateSlashMenu(props.value, el.selectionStart);
                  syncComposerScroll();
                }
              }}
              onKeyDown={(ev) => {
                if (ev.key === 'Escape' && slashOpen) {
                  ev.preventDefault();
                  setSlashOpen(false);
                  setSlashReplace(null);
                  cancelSlashDebounce();
                  return;
                }
                if (ev.key === 'Enter' && !ev.shiftKey && slashOpen && slashItems.length > 0 && !props.generating) {
                  ev.preventDefault();
                  const row0 = slashItems[0];
                  if (row0) {
                    applySlashChoice(row0.name);
                  }
                  return;
                }
                if (ev.key === 'Enter' && !ev.shiftKey) {
                  ev.preventDefault();
                  if (props.generating) {
                    return;
                  }
                  const txt = props.value.trim();
                  if (!txt) {
                    return;
                  }
                  props.onSend(txt);
                }
              }}
            />
          </div>
          {slashOpen && mobileUi ? (
            <div className="slash-menu slash-menu--sheet" data-testid="slash-command-menu" role="listbox" aria-label="Slash commands">
              <div className="slash-menu-surface" aria-hidden />
              <div className="slash-menu-scroll">
                <div className="slash-menu-title">Skills</div>
                {slashLoading && slashItems.length === 0 ? <div className="slash-muted">Loading…</div> : null}
                {slashErr ? <div className="slash-err">{slashErr}</div> : null}
                {!slashLoading && slashItems.length === 0 && !slashErr ? (
                  <div className="slash-muted">No commands</div>
                ) : null}
                <ul className="slash-rows">
                  {slashItems.map((row) => (
                    <li key={row.name}>
                      <button
                        type="button"
                        role="option"
                        className="slash-row-btn"
                        data-testid={`slash-command-row-${row.name}`}
                        onMouseDown={(e) => {
                          e.preventDefault();
                          applySlashChoice(row.name);
                        }}
                      >
                        <span className="slash-row-name">/{row.name}</span>
                        <span className="slash-row-desc">{row.description}</span>
                      </button>
                    </li>
                  ))}
                </ul>
                {slashHasMore ? (
                  <button
                    type="button"
                    className="slash-load-more"
                    disabled={slashLoading}
                    data-testid="slash-command-more"
                    onMouseDown={(e) => e.preventDefault()}
                    onClick={() => loadMoreSlash()}
                  >
                    {slashLoading ? 'Loading…' : 'More'}
                  </button>
                ) : null}
              </div>
            </div>
          ) : null}
        </div>

        <div className="composer-bar">
          <div className="composer-tabs" aria-label="Composer options">
            <div className="mode">
              <button
                type="button"
                className={`composer-tab mode-btn ${props.mode === 'plan' ? 'mode-plan' : 'mode-agent'}`}
                aria-label="Mode"
                title="Mode"
                aria-haspopup="menu"
                aria-expanded={menuOpen === 'mode'}
                onClick={() => setMenuOpen((cur) => (cur === 'mode' ? null : 'mode'))}
              >
                {modeLabel}
              </button>
              {menuOpen === 'mode' ? (
                <div className={`mode-menu ${modeMenuDirClass}`} role="menu">
                  {props.modes.map((m) => {
                    const label = displayMode(m);
                    return (
                      <button
                        key={m}
                        type="button"
                        role="menuitem"
                        className={`mode-item ${m === props.mode ? 'is-selected' : ''}`}
                        onClick={() => {
                          props.onModeChange(m);
                          setMenuOpen(null);
                        }}
                      >
                        {label}
                      </button>
                    );
                  })}
                </div>
              ) : null}
            </div>

            {showLlm && props.onLlmModelChange ? (
              <div className="mode">
                <button
                  type="button"
                  className="composer-tab mode-btn mode-llm"
                  aria-label="Model"
                  title="YAML backend (metadata.model)"
                  aria-haspopup="menu"
                  aria-expanded={menuOpen === 'llm'}
                  onClick={() => setMenuOpen((cur) => (cur === 'llm' ? null : 'llm'))}
                >
                  {llmLabel}
                </button>
                {menuOpen === 'llm' ? (
                  <div className={`mode-menu ${modeMenuDirClass}`} role="menu">
                    {llmList.map((mid) => {
                      const label = displayLlmId(mid);
                      return (
                        <button
                          key={mid}
                          type="button"
                          role="menuitem"
                          title={mid}
                          className={`mode-item ${mid === llmVal ? 'is-selected' : ''}`}
                          onClick={() => {
                            props.onLlmModelChange?.(mid);
                            setMenuOpen(null);
                          }}
                        >
                          {label}
                        </button>
                      );
                    })}
                  </div>
                ) : null}
              </div>
            ) : null}
          </div>

          <div className="composer-bar-actions">
            <div className="composer-context-tip-host" tabIndex={0} aria-label="Context usage">
              <div className="context-ring" role="img" aria-hidden="true">
                <svg viewBox={`0 0 ${vb} ${vb}`} width="30" height="30" aria-hidden="true">
                  <circle className="context-ring-bg" cx={cx} cy={cx} r={r} />
                  <circle
                    className="context-ring-fg"
                    cx={cx}
                    cy={cx}
                    r={r}
                    strokeDasharray={c}
                    strokeDashoffset={off}
                  />
                </svg>
              </div>
              <span className="rail-tip composer-context-tip" role="tooltip">
                {tip}
              </span>
            </div>
            <button
              type="button"
              className={['composer-icon', props.generating ? 'composer-send-stop' : 'composer-send-play'].join(' ')}
              id="btn-send"
              aria-label={props.generating ? 'Stop generation' : 'Send'}
              disabled={!props.generating && idleSendDisabled}
              onClick={() => {
                if (props.generating) {
                  props.onStop?.();
                  return;
                }
                const txt = props.value.trim();
                if (!txt) {
                  return;
                }
                props.onSend(txt);
              }}
            >
              <span className="composer-send-glyph" aria-hidden="true">
                {props.generating ? '■' : '▶'}
              </span>
            </button>
          </div>
        </div>
      </div>
    </footer>
    {slashOpen && !mobileUi && slashFloatRect
      ? createPortal(
          <div
            className="slash-menu slash-menu--portal"
            data-testid="slash-command-menu"
            role="listbox"
            aria-label="Slash commands"
            style={{
              left: slashFloatRect.left,
              width: slashFloatRect.width,
              bottom: slashFloatRect.bottom,
            }}
          >
            {slashMenuChrome}
          </div>,
          document.body,
        )
      : null}
    </>
  );
}
