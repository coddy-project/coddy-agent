import { useEffect, useState, useSyncExternalStore } from "react";
import { useT } from "../i18n/I18nProvider";
import { acquireObjectUrl } from "./objectUrl";
import { workspaceUrl } from "./api";
import { snapshotEnv, subscribeEnv } from "../env/remoteEnv";

export function FileImage(props: {
  sessionId: string;
  path: string;
  alt?: string | undefined;
  version?: string | undefined;
}) {
  const { t } = useT();
  const env = useSyncExternalStore(subscribeEnv, snapshotEnv, snapshotEnv);
  const [state, setState] = useState({ url: "", error: false });
  const [full, setFull] = useState(false);
  const [dimensions, setDimensions] = useState("");
  useEffect(() => {
    setState({ url: "", error: false });
    setDimensions("");
    const lease = acquireObjectUrl(
      workspaceUrl(props.sessionId, "raw", props.path) +
        "&v=" +
        encodeURIComponent(props.version || ""),
      undefined,
      true,
    );
    let cancelled = false;
    void lease.promise.then(
      (url) => {
        if (!cancelled) setState({ url, error: false });
      },
      () => {
        if (!cancelled) setState({ url: "", error: true });
      },
    );
    return () => {
      cancelled = true;
      lease.release();
    };
  }, [props.sessionId, props.path, props.version, env]);
  if (state.error)
    return <span role="alert">{t("files.imageUnavailable")}</span>;
  return (
    <span className={"files-image" + (full ? " files-image--full" : "")}>
      <button type="button" onClick={() => setFull((v) => !v)}>
        {t(full ? "files.fit" : "files.actualSize")}
      </button>
      {dimensions ? (
        <span className="files-image-dimensions">{dimensions}</span>
      ) : null}
      {state.url ? (
        <img
          src={state.url}
          alt={props.alt || props.path}
          onLoad={(e) =>
            setDimensions(
              `${e.currentTarget.naturalWidth} × ${e.currentTarget.naturalHeight}`,
            )
          }
        />
      ) : (
        <span>{t("files.loading")}</span>
      )}
    </span>
  );
}
