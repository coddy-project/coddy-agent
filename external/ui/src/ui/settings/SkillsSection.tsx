import { useCallback, useEffect, useState } from "react";
import { SchemaForm, type JsonSchema } from "./SchemaForm";

type InstalledSkill = {
  name: string;
  description: string;
  file_path: string;
  enabled: boolean;
  source?: string;
};

async function fetchInstalled(): Promise<InstalledSkill[]> {
  const res = await fetch("/coddy/skills");
  if (!res.ok) return [];
  const data = (await res.json()) as { items?: InstalledSkill[] };
  return data.items ?? [];
}

async function apiSend(
  path: string,
  method: "POST" | "DELETE",
  body?: unknown,
): Promise<{ ok: boolean; error?: string }> {
  const init: RequestInit = { method };
  if (body !== undefined) {
    init.headers = { "Content-Type": "application/json" };
    init.body = JSON.stringify(body);
  }
  const res = await fetch(path, init);
  if (!res.ok) {
    try {
      const j = (await res.json()) as { error?: { message?: string } };
      return { ok: false, error: j.error?.message || `HTTP ${res.status}` };
    } catch {
      return { ok: false, error: `HTTP ${res.status}` };
    }
  }
  return { ok: true };
}

function IconPlug() {
  return (
    <svg width="16" height="16" viewBox="0 0 24 24" fill="none"
      stroke="currentColor" strokeWidth="1.75" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
      <path d="M7 22H4a2 2 0 0 1-2-2v-3a2 2 0 0 0-2 0V7a2 2 0 0 0 2 0H7" />
      <path d="M15 7h4a2 2 0 0 1 2 2v4a2 2 0 0 0 0 2v3a2 2 0 0 1-2 2h-3" />
      <line x1="12" y1="2" x2="12" y2="22" />
    </svg>
  );
}

/**
 * SkillsSection is the combined Skills tab: the schema-driven `skills.dirs`
 * editor plus the installed-skills list with enable/disable toggles (folded in
 * from the former Skills flyout).
 */
export function SkillsSection(props: {
  schema: JsonSchema;
  value: Record<string, unknown>;
  onChange: (next: Record<string, unknown>) => void;
}) {
  const { schema, value, onChange } = props;
  const [installed, setInstalled] = useState<InstalledSkill[]>([]);
  const [busy, setBusy] = useState<Record<string, boolean>>({});
  const [error, setError] = useState<string | null>(null);
  const [status, setStatus] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [sourceInput, setSourceInput] = useState("");
  const [syncing, setSyncing] = useState(false);

  const loadInstalled = useCallback(async () => {
    setLoading(true);
    const data = await fetchInstalled();
    setInstalled(data);
    setLoading(false);
  }, []);

  useEffect(() => {
    void loadInstalled();
  }, [loadInstalled]);

  const onToggle = (skill: InstalledSkill) => {
    setBusy((p) => ({ ...p, [skill.name]: true }));
    setError(null);
    void (async () => {
      const action = skill.enabled ? "disable" : "enable";
      const res = await apiSend(`/coddy/skills/${encodeURIComponent(skill.name)}/${action}`, "POST");
      if (!res.ok) {
        setError(res.error || `Failed to ${action}`);
      } else {
        await loadInstalled();
      }
      setBusy((p) => ({ ...p, [skill.name]: false }));
    })();
  };

  const onRemove = (skill: InstalledSkill) => {
    setBusy((p) => ({ ...p, [skill.name]: true }));
    setError(null);
    void (async () => {
      const res = await apiSend(`/coddy/skills/${encodeURIComponent(skill.name)}`, "DELETE");
      if (!res.ok) {
        setError(res.error || "Failed to remove");
      } else {
        await loadInstalled();
      }
      setBusy((p) => ({ ...p, [skill.name]: false }));
    })();
  };

  const onSync = () => {
    setSyncing(true);
    setError(null);
    setStatus(null);
    void (async () => {
      const res = await apiSend("/coddy/skills/sync", "POST");
      if (!res.ok) setError(res.error || "Sync failed");
      else {
        setStatus("Sync complete.");
        await loadInstalled();
      }
      setSyncing(false);
    })();
  };

  const onAddSource = () => {
    const source = sourceInput.trim();
    if (!source) return;
    setSyncing(true);
    setError(null);
    setStatus(null);
    void (async () => {
      const res = await apiSend("/coddy/skills/sources", "POST", { source, sync: true });
      if (!res.ok) setError(res.error || "Failed to add source");
      else {
        setSourceInput("");
        setStatus(`Added and synced ${source}.`);
        await loadInstalled();
      }
      setSyncing(false);
    })();
  };

  return (
    <div className="settings-skills-section">
      <SchemaForm schema={schema} value={value} onChange={onChange} />

      <p className="appearance-section-label settings-skills-installed-label">
        Remote skill sources
      </p>
      <p className="settings-field-desc">
        Install skills from a GitHub repo (<code>owner/repo</code>), a git URL, or an{" "}
        <a href="https://agents.md" target="_blank" rel="noreferrer">agents-standard</a>{" "}
        <code>marketplace.json</code> URL. Sources are saved to <code>skills.sources</code> and
        fetched only when you sync.
      </p>
      <div className="settings-skills-source-row">
        <input
          className="settings-input"
          type="text"
          placeholder="owner/repo  ·  https://…/marketplace.json"
          value={sourceInput}
          onChange={(e) => setSourceInput(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              onAddSource();
            }
          }}
          disabled={syncing}
          data-testid="skills-source-input"
        />
        <button
          type="button"
          className="settings-btn settings-btn-primary"
          disabled={syncing || !sourceInput.trim()}
          onClick={onAddSource}
        >
          Add &amp; sync
        </button>
        <button
          type="button"
          className="settings-btn"
          disabled={syncing}
          onClick={onSync}
          title="Fetch all configured sources"
        >
          {syncing ? "Syncing…" : "Sync"}
        </button>
      </div>

      <p className="appearance-section-label settings-skills-installed-label">
        Installed skills
      </p>
      <p className="settings-field-desc">
        You can also install skills via <code>npx skills</code> or <code>npx skillsbd</code> — they
        land in <code>~/.agents/skills/</code> and are picked up automatically.
      </p>
      {error ? <p className="settings-error">{error}</p> : null}
      {status ? <p className="settings-muted">{status}</p> : null}

      {loading ? (
        <p className="settings-muted">Loading…</p>
      ) : installed.length === 0 ? (
        <p className="settings-muted">
          No skills found. Use <code>npx skills</code> or <code>npx skillsbd</code> to install.
        </p>
      ) : (
        <ul className="skills-list">
          {installed.map((sk) => (
            <li
              key={sk.name}
              className={`skills-list-item${sk.enabled ? "" : " is-disabled"}`}
            >
              <IconPlug />
              <div className="skills-list-item-text">
                <div className="skills-list-item-name">
                  {sk.name}
                  {sk.source ? (
                    <span className="skills-list-item-badge" title={`Synced from ${sk.source}`}>
                      remote
                    </span>
                  ) : null}
                </div>
                {sk.description ? (
                  <div className="skills-list-item-desc">{sk.description}</div>
                ) : null}
              </div>
              <button
                type="button"
                className="settings-btn skills-list-item-toggle"
                disabled={!!busy[sk.name]}
                onClick={() => onToggle(sk)}
                title={sk.enabled ? "Disable" : "Enable"}
              >
                {sk.enabled ? "Disable" : "Enable"}
              </button>
              {sk.source ? (
                <button
                  type="button"
                  className="settings-btn settings-btn-danger skills-list-item-toggle"
                  disabled={!!busy[sk.name]}
                  onClick={() => onRemove(sk)}
                  title="Remove synced skill"
                >
                  Remove
                </button>
              ) : null}
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
