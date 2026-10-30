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
      void refresh();
    };
    const timer = window.setInterval(focus, 15000);
    window.addEventListener("focus", focus);
    return () => {
      generation.current++;
      window.clearInterval(timer);
      window.removeEventListener("focus", focus);
      runtime.stop();
    };
  }, [refresh, runtime]);
  const snapshot = useSyncExternalStore(runtime.subscribe, runtime.snapshot);
  const livePage = page && snapshot.pages.find((p) => p.key === page.key);
  const livePanel = panel && snapshot.panels.find((p) => p.key === panel.key);
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
