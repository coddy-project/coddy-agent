import { useCallback, useEffect, useRef, useState } from "react";

const HDR = "X-Coddy-Session-ID";

export type PlanDocumentSectionProps = {
  sessionId: string;
  slug: string;
  name: string;
  overview: string;
  content: string;
  expanded: boolean;
  onExpandedChange: (expanded: boolean) => void;
  onDiscard: () => void;
  onRunPlan: () => void;
};

function previewLine(overview: string, content: string): string {
  const o = overview.trim();
  if (o) return o;
  const first = content.split("\n").find((l) => l.trim().length > 0);
  return first?.trim() || "";
}

export function PlanDocumentSection(props: PlanDocumentSectionProps) {
  const [draft, setDraft] = useState(props.content);
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState("");
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    setDraft(props.content);
  }, [props.content]);

  const persist = useCallback(
    async (text: string) => {
      const sid = props.sessionId.trim();
      if (!sid) return;
      setSaving(true);
      setSaveError("");
      try {
        const res = await fetch(
          `/coddy/sessions/${encodeURIComponent(sid)}/plans/${encodeURIComponent(props.slug)}`,
          {
            method: "PUT",
            headers: {
              "Content-Type": "application/json",
              [HDR]: sid,
            },
            body: JSON.stringify({ content: text }),
          },
        );
        if (!res.ok) {
          throw new Error(`save failed (${res.status})`);
        }
      } catch (e) {
        setSaveError(e instanceof Error ? e.message : "save failed");
      } finally {
        setSaving(false);
      }
    },
    [props.sessionId, props.slug],
  );

  const scheduleSave = useCallback(
    (text: string) => {
      if (debounceRef.current) clearTimeout(debounceRef.current);
      debounceRef.current = setTimeout(() => {
        void persist(text);
      }, 600);
    },
    [persist],
  );

  const title = props.name.trim() || props.slug;
  const preview = previewLine(props.overview, props.content);
  const slugLabel = props.slug.trim();

  return (
    <section
      className="plan-document-frame"
      data-test={
        props.expanded ? "plan_document_section" : "plan_document_collapsed"
      }
    >
      <div
        className={
          props.expanded
            ? "plan-document-card plan-document-card--expanded"
            : "plan-document-card"
        }
      >
        <header className="plan-document-head">
          <button
            type="button"
            className="plan-document-head-btn"
            onClick={() => props.onExpandedChange(!props.expanded)}
            aria-expanded={props.expanded}
          >
            <span
              className={
                props.expanded
                  ? "plan-document-chevron plan-document-chevron--open"
                  : "plan-document-chevron"
              }
              aria-hidden
            />
            <span className="plan-document-icon" aria-hidden />
            <span className="plan-document-head-copy">
              <span className="plan-document-kicker">Design plan</span>
              <span className="plan-document-title">{title}</span>
              {!props.expanded && preview ? (
                <span className="plan-document-preview">{preview}</span>
              ) : null}
            </span>
            {slugLabel ? (
              <span className="plan-document-slug" title={slugLabel}>
                {slugLabel}
              </span>
            ) : null}
          </button>
          {props.expanded ? (
            <div className="plan-document-head-meta">
              {saving ? (
                <span className="plan-document-save-hint">Saving…</span>
              ) : null}
              {saveError ? (
                <span className="plan-document-save-error">{saveError}</span>
              ) : null}
            </div>
          ) : null}
        </header>

        {props.expanded ? (
          <div className="plan-document-body">
            <textarea
              className="plan-document-editor"
              value={draft}
              onChange={(e) => {
                const v = e.target.value;
                setDraft(v);
                scheduleSave(v);
              }}
              rows={14}
              spellCheck
              aria-label="Plan markdown"
            />
          </div>
        ) : null}

        <PlanDocumentActions
          onRunPlan={props.onRunPlan}
          onDiscard={props.onDiscard}
        />
      </div>
    </section>
  );
}

function PlanDocumentActions(p: {
  onRunPlan: () => void;
  onDiscard: () => void;
}) {
  return (
    <footer className="plan-document-foot">
      <button
        type="button"
        className="plan-document-discard"
        data-test="plan_document_discard"
        onClick={() => p.onDiscard()}
      >
        Discard
      </button>
      <button
        type="button"
        className="plan-document-run"
        data-test="plan_document_run"
        onClick={() => p.onRunPlan()}
      >
        <span className="plan-document-run-ic" aria-hidden>
          ▶
        </span>
        Run plan
      </button>
    </footer>
  );
}
