import { useCallback, useEffect, useRef, useState } from "react";

import { Markdown } from "../markdown/Markdown";
import { MarkdownLineEditor } from "../markdown/MarkdownLineEditor";
import { planEditorBody } from "./planContent";

type PlanBodyView = "markdown" | "preview";

const HDR = "X-Coddy-Session-ID";

export type PlanDocumentSectionProps = {
  sessionId: string;
  slug: string;
  name: string;
  overview: string;
  content: string;
  body?: string;
  path?: string;
  discarded?: boolean;
  expanded: boolean;
  onExpandedChange: (expanded: boolean) => void;
  onDiscard: () => void;
  onRunPlan: () => void;
};

function descriptionLine(overview: string, body: string): string {
  const o = overview.trim();
  if (o) return o;
  const first = body.split("\n").find((l) => l.trim().length > 0);
  return first?.trim() || "";
}

function planFilePath(slug: string, path?: string): string {
  const p = (path ?? "").trim();
  if (p) return p;
  const s = slug.trim();
  return s ? `plans/${s}.plan.md` : "";
}

function PlanPreviewEyeToggle(p: {
  previewOn: boolean;
  onToggle: () => void;
}) {
  return (
    <button
      type="button"
      className={
        p.previewOn ? "plan-document-eye is-on" : "plan-document-eye"
      }
      title="Toggle preview"
      aria-label="Toggle preview"
      aria-pressed={p.previewOn}
      data-test="plan_document_preview_toggle"
      onClick={() => p.onToggle()}
    >
      <svg
        className="plan-document-eye-svg"
        viewBox="0 0 24 24"
        fill="none"
        xmlns="http://www.w3.org/2000/svg"
        aria-hidden
      >
        <path
          d="M2.25 12s3.75-6.75 9.75-6.75S21.75 12 21.75 12s-3.75 6.75-9.75 6.75S2.25 12 2.25 12Z"
          stroke="currentColor"
          strokeWidth="1.6"
          strokeLinecap="round"
          strokeLinejoin="round"
        />
        <circle cx="12" cy="12" r="3" stroke="currentColor" strokeWidth="1.6" />
        {!p.previewOn ? (
          <path
            d="M4 4l16 16"
            stroke="currentColor"
            strokeWidth="1.6"
            strokeLinecap="round"
          />
        ) : null}
      </svg>
    </button>
  );
}

function PlanDocumentActions(p: {
  discarded: boolean;
  onRunPlan: () => void;
  onDiscard: () => void;
}) {
  return (
    <>
      <button
        type="button"
        className="plan-document-discard"
        data-test="plan_document_discard"
        disabled={p.discarded}
        onClick={() => p.onDiscard()}
      >
        Discard
      </button>
      <button
        type="button"
        className="plan-document-run"
        data-test="plan_document_run"
        disabled={p.discarded}
        onClick={() => p.onRunPlan()}
      >
        <span className="plan-document-run-ic" aria-hidden>
          ▶
        </span>
        Run plan
      </button>
    </>
  );
}

export function PlanDocumentSection(props: PlanDocumentSectionProps) {
  const editorSeed = planEditorBody(props.content, props.body);
  const [draft, setDraft] = useState(editorSeed);
  const [bodyView, setBodyView] = useState<PlanBodyView>("preview");
  const [saving, setSaving] = useState(false);
  const [saveError, setSaveError] = useState("");
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const discarded = props.discarded === true;
  const previewOn = bodyView === "preview";

  useEffect(() => {
    setDraft(planEditorBody(props.content, props.body));
  }, [props.content, props.body]);

  const persist = useCallback(
    async (text: string) => {
      const sid = props.sessionId.trim();
      if (!sid || discarded) return;
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
            body: JSON.stringify({ body: text }),
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
    [props.sessionId, props.slug, discarded],
  );

  const scheduleSave = useCallback(
    (text: string) => {
      if (discarded) return;
      if (debounceRef.current) clearTimeout(debounceRef.current);
      debounceRef.current = setTimeout(() => {
        void persist(text);
      }, 600);
    },
    [persist, discarded],
  );

  const title = props.name.trim() || props.slug;
  const description = descriptionLine(props.overview, editorSeed);
  const filePath = planFilePath(props.slug, props.path);

  const cardClass = [
    "plan-document-card",
    props.expanded ? "plan-document-card--expanded" : "",
    discarded ? "plan-document-card--discarded" : "",
  ]
    .filter(Boolean)
    .join(" ");

  return (
    <section
      className="plan-document-frame"
      data-test={
        props.expanded ? "plan_document_section" : "plan_document_collapsed"
      }
    >
      <div className={cardClass}>
        <header className="plan-document-head">
          <button
            type="button"
            className="plan-document-head-btn"
            onClick={() => props.onExpandedChange(!props.expanded)}
            aria-expanded={props.expanded}
          >
            <span className="plan-document-title" title={filePath}>
              {title}
            </span>
            {!props.expanded && description ? (
              <span className="plan-document-desc">{description}</span>
            ) : null}
          </button>
          {props.expanded && (saving || saveError) ? (
            <div className="plan-document-head-status">
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
            <div className="plan-document-pane">
              <PlanPreviewEyeToggle
                previewOn={previewOn}
                onToggle={() =>
                  setBodyView((v) => (v === "preview" ? "markdown" : "preview"))
                }
              />
              <div
                className={
                  previewOn
                    ? "plan-document-pane-inner plan-document-pane-inner--preview"
                    : "plan-document-pane-inner plan-document-pane-inner--markdown"
                }
              >
                {previewOn ? (
                  <div
                    className="plan-document-preview-pane"
                    data-test="plan_document_preview_pane"
                  >
                    {draft.trim() ? (
                      <Markdown text={draft} />
                    ) : (
                      <p className="plan-document-preview-empty">
                        Nothing to preview yet.
                      </p>
                    )}
                  </div>
                ) : (
                  <MarkdownLineEditor
                    className="md-line-editor--plan"
                    value={draft}
                    readOnly={discarded}
                    minRows={4}
                    spellCheck
                    gutterTestId="plan_editor_gutter"
                    rootTestId="plan_markdown_editor"
                    aria-label="Plan body (markdown)"
                    placeholder="Plan steps and notes…"
                    onChange={(v) => {
                      setDraft(v);
                      scheduleSave(v);
                    }}
                  />
                )}
              </div>
            </div>
          </div>
        ) : null}

        <footer className="plan-document-foot">
          <PlanDocumentActions
            discarded={discarded}
            onRunPlan={props.onRunPlan}
            onDiscard={props.onDiscard}
          />
        </footer>
      </div>
    </section>
  );
}
