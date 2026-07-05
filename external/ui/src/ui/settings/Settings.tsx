import {
  useCallback,
  useEffect,
  useMemo,
  useState,
  useSyncExternalStore,
} from "react";
import { type JsonSchema } from "./SchemaForm";
import {
  deriveSettingsSections,
  type SectionDescriptor,
} from "./settingsSections";
import { SettingsNav } from "./SettingsNav";
import { SettingsSection } from "./SettingsSection";
import { SettingsTileGrid } from "./SettingsTileGrid";
import {
  serverSnapshotShellStack,
  snapshotShellStack,
  subscribeShellStack,
} from "../shellBreakpoint";
import {
  setSettingsHash,
  setSettingsSectionHash,
} from "../scheduler/hashRoute";

type ValidateResponse = { ok: boolean; error?: string };

async function readJSON<T>(
  path: string,
): Promise<{ ok: boolean; data?: T; error?: string }> {
  const res = await fetch(path);
  if (!res.ok) {
    return { ok: false, error: `${res.status}` };
  }
  try {
    const data = (await res.json()) as T;
    return { ok: true, data };
  } catch (e) {
    return { ok: false, error: e instanceof Error ? e.message : "parse" };
  }
}

function IconSave(props: { className?: string }) {
  return (
    <svg
      className={props.className}
      width="20"
      height="20"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.75"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
    >
      <path d="M19 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h11l5 5v11a2 2 0 0 1-2 2z" />
      <polyline points="17 21 17 13 7 13 7 21" />
      <polyline points="7 3 7 8 15 8" />
    </svg>
  );
}

function IconRefresh(props: { className?: string }) {
  return (
    <svg
      className={props.className}
      width="20"
      height="20"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.75"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
    >
      <polyline points="23 4 23 10 17 10" />
      <polyline points="1 20 1 14 7 14" />
      <path d="M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15" />
    </svg>
  );
}

/** Back arrow (lucide arrow-left) for the mobile section-detail header. */
function IconArrowLeft(props: { className?: string }) {
  return (
    <svg
      className={props.className}
      width="18"
      height="18"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.9"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
    >
      <path d="M19 12H5" />
      <path d="M12 19l-7-7 7-7" />
    </svg>
  );
}

export function Settings(props: {
  onClose: () => void;
  /** Called after the config is successfully saved so the app can re-fetch model metadata. */
  onConfigSaved?: () => void;
  /** Section id from the `#/settings/<section>` deep link (null = default/grid). */
  initialSection?: string | null;
}) {
  const [schema, setSchema] = useState<JsonSchema | null>(null);
  const [doc, setDoc] = useState<Record<string, unknown>>({});
  const [loadErr, setLoadErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [activeTab, setActiveTab] = useState<string>(
    props.initialSection ?? "",
  );
  // Animation feedback: bump reloadKey to replay the form dissolve/reappear on
  // reload; reloading spins the refresh icon; justSaved pulses the save button.
  const [reloadKey, setReloadKey] = useState(0);
  const [reloading, setReloading] = useState(false);
  const [justSaved, setJustSaved] = useState(false);

  // On narrow shells the section picker is a tile grid (master) that opens one
  // section at a time (detail); `mobileDetailId` null means the grid is showing.
  const isMobileShell = useSyncExternalStore(
    subscribeShellStack,
    snapshotShellStack,
    serverSnapshotShellStack,
  );
  const [mobileDetailId, setMobileDetailId] = useState<string | null>(
    props.initialSection ?? null,
  );

  const sections = useMemo(() => deriveSettingsSections(schema), [schema]);
  const activeSection =
    sections.find((s) => s.id === activeTab) ?? sections[0] ?? null;
  const mobileSection = mobileDetailId
    ? (sections.find((s) => s.id === mobileDetailId) ?? null)
    : null;

  // Reflect the `#/settings/<section>` deep link (initial load and browser
  // back/forward) into local tab state; writing the hash below re-enters here
  // with the same value, so this is a no-op on self-initiated changes.
  const routeSection = props.initialSection ?? null;
  useEffect(() => {
    setActiveTab(routeSection ?? "");
    setMobileDetailId(routeSection);
  }, [routeSection]);

  // Selecting a section (desktop tab or mobile tile) anchors it in the URL.
  const selectSection = useCallback((id: string) => {
    setActiveTab(id);
    setMobileDetailId(id);
    setSettingsSectionHash(id);
  }, []);

  // Mobile back to the tile grid drops the section anchor.
  const backToGrid = useCallback(() => {
    setMobileDetailId(null);
    setSettingsHash();
  }, []);

  const load = useCallback(async () => {
    setLoadErr(null);
    const [sRes, cRes] = await Promise.all([
      readJSON<JsonSchema>("/coddy/config/schema"),
      readJSON<Record<string, unknown>>("/coddy/config"),
    ]);
    if (!sRes.ok || !sRes.data) {
      setLoadErr(sRes.error || "schema");
      return;
    }
    if (!cRes.ok || !cRes.data) {
      setLoadErr(cRes.error || "config");
      return;
    }
    setSchema(sRes.data);
    setDoc(cRes.data);
  }, []);

  useEffect(() => {
    void load();
  }, [load]);

  // Reload with visible feedback: spin the refresh icon and replay the form
  // dissolve/reappear animation (key bump remounts the content) while re-fetching.
  const onReload = useCallback(async () => {
    setReloading(true);
    setReloadKey((k) => k + 1);
    try {
      await Promise.all([
        load(),
        new Promise((r) => window.setTimeout(r, 500)),
      ]);
    } finally {
      setReloading(false);
    }
  }, [load]);

  const onSave = useCallback(async () => {
    setBusy(true);
    setMessage(null);
    setError(null);
    try {
      const body = JSON.stringify(doc);
      const v = await fetch("/coddy/config/validate", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body,
      });
      const vj = (await v.json()) as ValidateResponse;
      if (!vj.ok) {
        setError(vj.error || "validation failed");
        setBusy(false);
        return;
      }
      const p = await fetch("/coddy/config", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body,
      });
      const pj = (await p.json()) as ValidateResponse;
      if (!p.ok || !pj.ok) {
        setError(pj.error || `save failed (${p.status})`);
        setBusy(false);
        return;
      }
      setMessage("Saved all sections. In-process config reloaded.");
      setJustSaved(true);
      window.setTimeout(() => setJustSaved(false), 1100);
      props.onConfigSaved?.();
      await load();
    } catch (e) {
      setError(e instanceof Error ? e.message : "request failed");
    } finally {
      setBusy(false);
    }
  }, [doc, load, props]);

  // Renders the content panel for a section, reusing the schema-present and
  // appearance-without-schema paths for both the desktop rail and the mobile
  // tile-grid detail view.
  const renderSectionBody = (section: SectionDescriptor | null) => {
    if (schema) {
      return (
        <div className="settings-scroll">
          <div
            className={`settings-body${reloadKey > 0 ? " settings-form-anim" : ""}`}
            key={reloadKey}
          >
            {section ? (
              <SettingsSection
                section={section}
                schema={schema}
                doc={doc}
                setDoc={setDoc}
                isMobileShell={isMobileShell}
              />
            ) : null}
          </div>
        </div>
      );
    }
    if (!loadErr) {
      // Appearance is client-side content (the theme picker), available before
      // the config schema loads. Render it in the normal scroll flow — NOT the
      // centered `settings-scroll-placeholder` used for the "Loading…" spinner,
      // which shrinks and off-centers the swatch grid.
      if (section && section.kind === "appearance") {
        return (
          <div className="settings-scroll">
            <div className="settings-body">
              <SettingsSection
                section={section}
                schema={{ type: "object", properties: {} } as JsonSchema}
                doc={doc}
                setDoc={setDoc}
              />
            </div>
          </div>
        );
      }
      return (
        <div className="settings-scroll settings-scroll-placeholder">
          <p className="settings-muted">Loading…</p>
        </div>
      );
    }
    return null;
  };

  return (
    <aside
      className="sessions settings drawer"
      aria-label="Settings"
      data-testid="settings-screen"
      data-variant="drawer"
    >
      <div className="sessions-head">
        {isMobileShell && mobileSection ? (
          <span className="settings-head-titlegroup">
            <button
              type="button"
              className="settings-head-back"
              aria-label="Back to sections"
              title="Back to sections"
              data-testid="settings-mobile-back"
              onClick={backToGrid}
            >
              <IconArrowLeft />
            </button>
            <span className="settings-head-section">{mobileSection.label}</span>
          </span>
        ) : (
          <span>Settings</span>
        )}
        <button
          type="button"
          className="sessions-close"
          aria-label="Close settings"
          data-testid="settings-drawer-close"
          onClick={props.onClose}
        >
          ×
        </button>
      </div>

      <div className="settings-lead-pane">
        <p className="settings-lead">
          Edit configuration from the live JSON schema. Secrets (API keys) are
          shown in full - use only on trusted networks.
        </p>
        {loadErr ? (
          <p className="settings-error">Failed to load: {loadErr}</p>
        ) : null}
        {error ? <p className="settings-error">{error}</p> : null}
        {message ? <p className="settings-ok">{message}</p> : null}
      </div>

      <div className="settings-stack">
        {isMobileShell ? (
          mobileSection ? (
            <div className="settings-mobile-detail">
              {renderSectionBody(mobileSection)}
            </div>
          ) : (
            <SettingsTileGrid sections={sections} onSelect={selectSection} />
          )
        ) : (
          <div className="settings-tabs-layout">
            <SettingsNav
              sections={sections}
              active={activeSection ? activeSection.id : ""}
              onSelect={selectSection}
            />
            {renderSectionBody(activeSection)}
          </div>
        )}

        <div className="scheduler-drawer-footer settings-footer-actions">
          <button
            type="button"
            className="settings-btn settings-btn-icon"
            data-testid="settings-reload"
            disabled={busy || reloading}
            title="Reload from server"
            aria-label="Reload configuration from server"
            onClick={() => void onReload()}
          >
            <IconRefresh
              className={`settings-footer-icon-svg${reloading ? " settings-icon-spin" : ""}`}
            />
          </button>
          <button
            type="button"
            className={`settings-btn settings-btn-primary settings-btn-icon${justSaved ? " is-saved" : ""}`}
            data-testid="settings-save"
            disabled={busy || !schema}
            title="Save all sections"
            aria-label="Save all configuration sections"
            onClick={() => void onSave()}
          >
            <IconSave className="settings-footer-icon-svg" />
          </button>
        </div>
      </div>
    </aside>
  );
}
