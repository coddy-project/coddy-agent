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

  if (!props.expanded) {
    return (
      <section
        className="plan-document-frame plan-document-collapsed"
        data-test="plan_document_collapsed"
      >
        <button
          type="button"
          className="plan-document-toggle"
          onClick={() => props.onExpandedChange(true)}
        >
          <span className="plan-document-title">{title}</span>
          {preview ? (
            <span className="plan-document-preview">{preview}</span>
          ) : null}
        </button>
        <PlanDocumentActions
          onRunPlan={props.onRunPlan}
          onDiscard={props.onDiscard}
        />
      </section>
    );
  }

  return (
    <section
      className="plan-document-frame"
      data-test="plan_document_section"
    >
      <header className="plan-document-header">
        <button
          type="button"
          className="plan-document-toggle"
          onClick={() => props.onExpandedChange(false)}
        >
          {title}
        </button>
        {saving ? (
          <span className="plan-document-save-hint">Saving…</span>
        ) : null}
        {saveError ? (
          <span className="plan-document-save-error">{saveError}</span>
        ) : null}
      </header>
      <textarea
        className="plan-document-editor"
        value={draft}
        onChange={(e) => {
          const v = e.target.value;
          setDraft(v);
          scheduleSave(v);
        }}
        rows={16}
        spellCheck
      />
      <PlanDocumentActions
        onRunPlan={props.onRunPlan}
        onDiscard={props.onDiscard}
      />
    </section>
  );
}

function PlanDocumentActions(p: {
  onRunPlan: () => void;
  onDiscard: () => void;
}) {
  return (
    <footer className="plan-document-actions">
      <button
        type="button"
        className="btn primary"
        data-test="plan_document_run"
        onClick={() => p.onRunPlan()}
      >
        Run plan
      </button>
      <button
        type="button"
        className="btn"
        data-test="plan_document_discard"
        onClick={() => p.onDiscard()}
      >
        Discard
      </button>
    </footer>
  );
}
