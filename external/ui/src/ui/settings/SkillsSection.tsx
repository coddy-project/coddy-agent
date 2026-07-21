import { useCallback, useEffect, useState } from "react";
import { SchemaForm, type JsonSchema } from "./SchemaForm";

type InstalledSkill = {
  name: string;
  description: string;
  file_path: string;
  enabled: boolean;
  version?: string;
  source?: string;
};

type SkillUpdate = {
  name: string;
  source: string;
  version: string;
  latest: string;
  update_available: boolean;
};

async function fetchInstalled(): Promise<InstalledSkill[]> {
  const res = await fetch("/coddy/skills");
  if (!res.ok) return [];
  const data = (await res.json()) as { items?: InstalledSkill[] };
  return data.items ?? [];
}

async function fetchSources(): Promise<string[]> {
  const res = await fetch("/coddy/skills/sources");
  if (!res.ok) return [];
  const data = (await res.json()) as { items?: string[] };
  return data.items ?? [];
}

async function fetchUpdates(): Promise<SkillUpdate[]> {
  const res = await fetch("/coddy/skills/updates");
  if (!res.ok) return [];
  const data = (await res.json()) as { items?: SkillUpdate[] };
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

function IconRefresh() {
  return (
    <svg width="15" height="15" viewBox="0 0 24 24" fill="none"
      stroke="currentColor" strokeWidth="1.9" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
      <path d="M21 12a9 9 0 1 1-2.64-6.36" />
      <polyline points="21 3 21 9 15 9" />
    </svg>
  );
}

function IconUpdate() {
  return (
    <svg width="15" height="15" viewBox="0 0 24 24" fill="none"
      stroke="currentColor" strokeWidth="1.9" strokeLinecap="round" strokeLinejoin="round" aria-hidden>
      <path d="M12 3v12" />
      <polyline points="7 10 12 15 17 10" />
      <path d="M5 21h14" />
    </svg>
  );
}

/**
 * SkillsSection is the combined Skills tab: the schema-driven `skills.dirs`
 * editor, remote marketplace source management (add / list / remove / sync),
 * and the installed-skills list with versions, enable/disable, remove, and a
 * per-skill Update action shown when a newer version is available upstream.
 */
export function SkillsSection(props: {
  schema: JsonSchema;
  value: Record<string, unknown>;
  onChange: (next: Record<string, unknown>) => void;
}) {
  const { schema, value, onChange } = props;
  const [installed, setInstalled] = useState<InstalledSkill[]>([]);
  const [sources, setSources] = useState<string[]>([]);
  const [updates, setUpdates] = useState<Record<string, SkillUpdate>>({});
  const [busy, setBusy] = useState<Record<string, boolean>>({});
  const [error, setError] = useState<string | null>(null);
  const [status, setStatus] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [sourceInput, setSourceInput] = useState("");
  const [syncing, setSyncing] = useState(false);
  const [checking, setChecking] = useState(false);

  const loadInstalled = useCallback(async () => {
    setLoading(true);
    const [data, srcs] = await Promise.all([fetchInstalled(), fetchSources()]);
    setInstalled(data);
    setSources(srcs);
    setLoading(false);
  }, []);

  const refreshUpdates = useCallback(async () => {
    const ups = await fetchUpdates();
    const map: Record<string, SkillUpdate> = {};
    for (const u of ups) map[u.name] = u;
    setUpdates(map);
    return map;
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

  const onUpdateSkill = (skill: InstalledSkill) => {
    setBusy((p) => ({ ...p, [skill.name]: true }));
    setError(null);
    setStatus(null);
    void (async () => {
      const res = await apiSend(`/coddy/skills/${encodeURIComponent(skill.name)}/update`, "POST");
      if (!res.ok) {
        setError(res.error || "Update failed");
      } else {
        setStatus(`Updated ${skill.name}.`);
        await loadInstalled();
        await refreshUpdates();
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
        await refreshUpdates();
      }
      setSyncing(false);
    })();
  };

  // Refresh the installed list and check every source for newer versions.
  const onRefresh = () => {
    setChecking(true);
    setError(null);
    setStatus(null);
    void (async () => {
      await loadInstalled();
      await refreshUpdates();
      setStatus("Skill list refreshed.");
      setChecking(false);
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
        await refreshUpdates();
      }
      setSyncing(false);
    })();
  };

  const onRemoveSource = (source: string) => {
    setSyncing(true);
    setError(null);
    setStatus(null);
    void (async () => {
      const res = await apiSend(`/coddy/skills/sources?source=${encodeURIComponent(source)}`, "DELETE");
      if (!res.ok) setError(res.error || "Failed to remove source");
      else {
        setStatus(`Removed source ${source}.`);
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

      {sources.length > 0 ? (
        <ul className="skills-source-list" data-testid="skills-source-list">
          {sources.map((src) => (
            <li key={src} className="skills-source-item">
              <span className="skills-source-item-name" title={src}>{src}</span>
              <button
                type="button"
                className="settings-btn settings-btn-danger skills-source-item-remove"
                disabled={syncing}
                onClick={() => onRemoveSource(src)}
                title={`Remove source ${src}`}
                aria-label={`Remove source ${src}`}
              >
                Remove
              </button>
            </li>
          ))}
        </ul>
      ) : null}

      <div className="skills-installed-header">
        <p className="appearance-section-label settings-skills-installed-label">
          Installed skills
        </p>
        <button
          type="button"
          className="settings-btn skills-refresh-btn"
          disabled={checking || loading}
          onClick={onRefresh}
          title="Refresh the skill list and check sources for newer versions"
          aria-label="Refresh skills and check for updates"
          data-testid="skills-refresh"
        >
          <IconRefresh />
          <span>{checking ? "Checking…" : "Refresh"}</span>
        </button>
      </div>
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
          {installed.map((sk) => {
            const upd = updates[sk.name];
            const hasUpdate = !!upd?.update_available;
            return (
              <li
                key={sk.name}
                className={`skills-list-item${sk.enabled ? "" : " is-disabled"}`}
              >
                <IconPlug />
                <div className="skills-list-item-text">
                  <div className="skills-list-item-name">
                    {sk.name}
                    {sk.version ? (
                      <span className="skills-list-item-version">v{sk.version}</span>
                    ) : null}
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
                {hasUpdate ? (
                  <button
                    type="button"
                    className="settings-btn settings-btn-primary skills-update-btn"
                    disabled={!!busy[sk.name]}
                    onClick={() => onUpdateSkill(sk)}
                    title={`Update ${sk.name} from v${upd?.version || sk.version || "?"} to v${upd?.latest}`}
                    aria-label={`Update ${sk.name} to version ${upd?.latest}`}
                    data-testid={`skills-update-${sk.name}`}
                  >
                    <IconUpdate />
                    <span>Update</span>
                  </button>
                ) : null}
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
            );
          })}
        </ul>
      )}
    </div>
  );
}
