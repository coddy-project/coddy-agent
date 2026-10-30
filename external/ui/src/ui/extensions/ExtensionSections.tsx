import { useEffect, useMemo, useRef, useSyncExternalStore } from "react";
import { useExtensions } from "./ExtensionsProvider";
import { ExtensionFields } from "./ExtensionFields";
import type { Owned, SettingsSection } from "./contributions";

const emptySubscribe = () => () => {};
const emptySections: Owned<SettingsSection>[] = [];

/** The registered extension settings sections, live, ordered by weight. The
 * sorted copy keeps its identity between publishes, so a consumer's memo
 * over the combined section list does not rebuild on every render. */
export function useExtensionSettingsSections(): Owned<SettingsSection>[] {
  const runtime = useExtensions()?.runtime;
  const rows = useSyncExternalStore(
    runtime ? runtime.subscribe : emptySubscribe,
    runtime ? () => runtime.snapshot().sections : () => emptySections,
    () => emptySections,
  );
  return useMemo(
    () =>
      [...rows].sort(
        (a, b) =>
          (a.weight ?? 0) - (b.weight ?? 0) ||
          (a.owner < b.owner ? -1 : a.owner > b.owner ? 1 : 0) ||
          (a.id < b.id ? -1 : 1),
      ),
    [rows],
  );
}

/** The descriptor id of an extension section: `ext:<owner>/<id>`. */
export function extensionSectionId(owner: string, id: string): string {
  return `ext:${owner}/${id}`;
}

export function parseExtensionSectionId(
  key: string,
): { owner: string; id: string } | null {
  if (!key.startsWith("ext:")) return null;
  const at = key.indexOf("/", 4);
  if (at <= 4) return null;
  return { owner: key.slice(4, at), id: key.slice(at + 1) };
}

/** The body of one extension settings section: its own mount, then its
 * fields of that owner only. */
export function ExtensionSectionBody({
  sectionKey,
  doc,
  setDoc,
}: {
  sectionKey: string;
  doc: Record<string, unknown>;
  setDoc: (doc: Record<string, unknown>) => void;
}) {
  const runtime = useExtensions()?.runtime;
  const parsed = parseExtensionSectionId(sectionKey);
  const rows = useSyncExternalStore(
    runtime ? runtime.subscribe : emptySubscribe,
    runtime ? () => runtime.snapshot().sections : () => emptySections,
    () => emptySections,
  );
  const section =
    parsed && runtime
      ? rows.find((r) => r.owner === parsed.owner && r.id === parsed.id)
      : undefined;
  if (!parsed || !runtime || !section) return null;
  return (
    <div
      className="extension-settings-section"
      data-extension-settings-section={sectionKey}
    >
      <MountedSection
        key={section.key}
        section={section}
        fail={(error) => runtime.reportError(section.owner, String(error))}
      />
      <ExtensionFields
        section={parsed.id}
        owner={parsed.owner}
        doc={doc}
        setDoc={setDoc}
      />
    </div>
  );
}

function MountedSection({
  section,
  fail,
}: {
  section: Owned<SettingsSection>;
  fail: (error: unknown) => void;
}) {
  const element = useRef<HTMLDivElement>(null);
  const cleanup = useRef<() => void>(() => {});
  useEffect(() => {
    const el = element.current;
    if (!el) return;
    const dispose = () => {
      try {
        Promise.resolve(cleanup.current()).catch(fail);
      } catch (error) {
        fail(error);
      }
      cleanup.current = () => {};
      el.replaceChildren();
    };
    if (typeof section.mount === "function") {
      try {
        const result = section.mount(el, section.api);
        if (typeof result === "function") cleanup.current = result;
        else if (result !== undefined && result !== null) {
          Promise.resolve(result).catch(() => {});
          throw new Error("Settings section mount must be synchronous");
        }
      } catch (error) {
        fail(error);
      }
    }
    return dispose;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [section.key, section.mount, section.api]);
  return <div ref={element} className="extension-settings-section-view" />;
}
