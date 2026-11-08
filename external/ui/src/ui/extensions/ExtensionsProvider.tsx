import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  useSyncExternalStore,
  type ReactNode,
} from "react";
import {
  ExtensionRuntime,
  type ExtensionEntry,
  type RuntimePage,
} from "./runtime";
import type { RuntimePanel } from "./panels";
import { useT } from "../i18n/I18nProvider";
import { PanePortal } from "../components/PanePortal";
import { ExtensionPanelHost } from "./ExtensionPanelHost";
import {
  ExtensionModalHost,
  ExtensionOverlayHost,
  ExtensionShellHost,
} from "./ExtensionSurfaces";
import { readAppliedUiLocale, subscribeUiLocale } from "../i18n/uiLocale";
import "./extensions.css";
import { parseAppHash } from "../scheduler/hashRoute";

export async function extensionRequest(path = "", init?: RequestInit) {
  const response = await fetch(`/coddy/extensions${path}`, init);
  if (!response.ok) {
    let reason = `HTTP ${response.status}`;
    try {
      const body = await response.json();
      reason = body.error?.message ?? body.error ?? reason;
    } catch {
      /* Status is enough for non-JSON errors. */
    }
    throw new Error(String(reason));
  }
  return response;
}

type ExtensionsContext = {
  entries: ExtensionEntry[];
  error: string;
  refresh: (retryFailed?: boolean) => Promise<void>;
  runtime: ExtensionRuntime;
  ready: Promise<void>;
};
const Context = createContext<ExtensionsContext | null>(null);
export const useExtensions = () => useContext(Context);

const noShellSubscribe = () => () => {};

/** The shell takeover row while an extension holds the window. */
export function useExtensionShell() {
  const context = useExtensions();
  return useSyncExternalStore(
    context ? context.runtime.subscribe : noShellSubscribe,
    context ? () => context.runtime.snapshot().shell : () => null,
    () => null,
  );
}

export function ExtensionsProvider({ children }: { children: ReactNode }) {
  const [entries, setEntries] = useState<ExtensionEntry[]>([]);
  const [error, setError] = useState("");
  const [page, setPage] = useState<RuntimePage | null>(null);
  const [panel, setPanel] = useState<RuntimePanel | null>(null);
  const [runtime] = useState(
    () => new ExtensionRuntime(setPage, undefined, undefined, setPanel),
  );
  const [startup] = useState(() => {
    let resolve!: () => void;
    const ready = new Promise<void>((done) => {
      resolve = done;
    });
    return { ready, resolve, activation: null as Promise<void> | null };
  });
  const generation = useRef(0);
  // The list is polled every 15 s and on every focus. A poll that finds the
  // same packages keeps the array it has: a new one would re-render every
  // consumer of the context, the whole App among them.
  const listed = useRef("");
  useEffect(() => {
    // Extension dictionaries follow the host locale; applyUiLocale is the
    // single write point, so one subscription covers every switch.
    return subscribeUiLocale(() =>
      runtime.i18n.notifyLocale(readAppliedUiLocale()),
    );
  }, [runtime]);
  useEffect(() => {
    let previous = window.location.hash;
    const changed = () => {
      const to = window.location.hash;
      if (previous === to) return;
      const from = previous;
      previous = to;
      const route = parseAppHash();
      runtime.events.emit("tabChanged", {
        from,
        to,
        sessionId: route.branch === "session" ? route.sessionId : null,
      });
    };
    window.addEventListener("hashchange", changed);
    return () => window.removeEventListener("hashchange", changed);
  }, [runtime]);
  const refresh = useCallback(
    async (retryFailed = false) => {
      const current = ++generation.current;
      try {
        const response = await extensionRequest();
        const body = (await response.json()) as { items: ExtensionEntry[] };
        if (current !== generation.current) return;
        const text = JSON.stringify(body.items);
        if (text !== listed.current) {
          listed.current = text;
          setEntries(body.items);
        }
        setError("");
        const activation = runtime.reconcile(body.items, retryFailed);
        if (!startup.activation) {
          startup.activation = activation;
          void activation.then(startup.resolve, startup.resolve);
        }
        await activation;
      } catch (e) {
        if (current === generation.current) setError(String(e));
        if (!startup.activation) startup.resolve();
      }
    },
    [runtime, startup],
  );
  useEffect(() => {
    void refresh();
    const focus = () => {
      runtime.events.emit("focusChanged", { focused: true });
      void refresh();
    };
    const blur = () => runtime.events.emit("focusChanged", { focused: false });
    const beforeUnload = () => runtime.events.emit("beforeUnload", {});
    const timer = window.setInterval(() => void refresh(), 15000);
    window.addEventListener("focus", focus);
    window.addEventListener("blur", blur);
    window.addEventListener("beforeunload", beforeUnload);
    return () => {
      generation.current++;
      window.clearInterval(timer);
      window.removeEventListener("focus", focus);
      window.removeEventListener("blur", blur);
      window.removeEventListener("beforeunload", beforeUnload);
      runtime.stop();
    };
  }, [refresh, runtime]);
  const snapshot = useSyncExternalStore(runtime.subscribe, runtime.snapshot);
  const livePage = page && snapshot.pages.find((p) => p.key === page.key);
  const livePanel = panel && snapshot.panels.find((p) => p.key === panel.key);
  // Surfaces coming and going are events an extension can draw a badge or a
  // mascot from; the diff keeps the stream to real changes only.
  const surfaceState = {
    page: livePage ? livePage.key : null,
    panel: livePanel ? livePanel.key : null,
    modal: snapshot.modals.length
      ? snapshot.modals[snapshot.modals.length - 1]!.key
      : null,
    shell: snapshot.shell ? snapshot.shell.key : null,
  };
  const previousSurfaces = useRef<typeof surfaceState | null>(null);
  useEffect(() => {
    const before = previousSurfaces.current;
    previousSurfaces.current = surfaceState;
    if (!before) return;
    for (const name of Object.keys(
      surfaceState,
    ) as (keyof typeof surfaceState)[]) {
      if (before[name] !== surfaceState[name])
        runtime.events.emit("surfaceChanged", {
          surface: name,
          id: surfaceState[name],
          open: surfaceState[name] !== null,
        });
    }
  });
  const overlayKeys = snapshot.overlays.map((o) => o.key).join(",");
  const previousOverlays = useRef<string | null>(null);
  useEffect(() => {
    // The first run records the starting set; only a real change is an event.
    if (previousOverlays.current === null) {
      previousOverlays.current = overlayKeys;
      return;
    }
    if (previousOverlays.current === overlayKeys) return;
    previousOverlays.current = overlayKeys;
    runtime.events.emit("overlaysChanged", {
      ids: snapshot.overlays.map((o) => o.key),
    });
  });
  const value = useMemo(
    () => ({ entries, error, refresh, runtime, ready: startup.ready }),
    [entries, error, refresh, runtime, startup.ready],
  );
  return (
    <Context.Provider value={value}>
      {children}
      {livePage ? (
        <ExtensionWindow page={livePage} onClose={() => setPage(null)} />
      ) : null}
      {livePanel ? (
        <ExtensionPanelHost panel={livePanel} onClose={() => setPanel(null)} />
      ) : null}
      {snapshot.modals.length ? (
        <ExtensionModalHost
          modal={snapshot.modals[snapshot.modals.length - 1]!}
          onClose={() =>
            runtime
              .snapshot()
              .modals.forEach((row) => runtime.closeSurface("modals", row.key))
          }
        />
      ) : null}
      {snapshot.overlays.map((overlay) => (
        <ExtensionOverlayHost
          key={overlay.key}
          overlay={overlay}
          onClose={() => runtime.closeSurface("overlays", overlay.key)}
        />
      ))}
      {snapshot.shell ? (
        <ExtensionShellHost
          shell={snapshot.shell}
          onRelease={() => runtime.closeSurface("shell", snapshot.shell!.key)}
        />
      ) : null}
    </Context.Provider>
  );
}

function ExtensionWindow({
  page,
  onClose,
}: {
  page: RuntimePage;
  onClose: () => void;
}) {
  const dialog = useRef<HTMLDialogElement>(null);
  const body = useRef<HTMLDivElement>(null);
  const [error, setError] = useState("");
  const { t } = useT();
  useEffect(() => {
    dialog.current?.showModal();
  }, []);
  useEffect(() => {
    const el = body.current;
    if (!el) return;
    setError("");
    let cleanup: void | (() => void);
    try {
      cleanup = page.mount(el, page.api);
    } catch (e) {
      setError(String(e));
    }
    return () => {
      try {
        cleanup?.();
      } catch (e) {
        console.error(e);
      }
      el.replaceChildren();
    };
  }, [page.key, page.mount, page.api]);
  return (
    <PanePortal>
      <dialog
        ref={dialog}
        className="extension-window"
        aria-label={page.title}
        onCancel={onClose}
        onClose={onClose}
      >
        <header>
          <strong>{page.title}</strong>
          <button type="button" className="settings-btn" onClick={onClose}>
            {t("extensions.close")}
          </button>
        </header>
        {error ? <p role="alert">{error}</p> : null}
        <div ref={body} className="extension-window-body" />
      </dialog>
    </PanePortal>
  );
}
