import { useEffect, useState, useSyncExternalStore } from "react";
import { useT } from "../i18n/I18nProvider";
import {
  RASTER_IMAGE_TYPE,
  acquireObjectUrl,
  objectUrlBlob,
} from "./objectUrl";
import { workspaceUrl } from "./api";
import { snapshotEnv, subscribeEnv } from "../env/remoteEnv";

/**
 * A picture of the workspace, from bounded authenticated bytes, with fit and
 * actual size. A raster picture is shown only as what the node serves it as.
 * An SVG (`svg`) comes as a download and is given its picture type here: an
 * <img> runs nothing an SVG holds and loads nothing it names, so the picture is
 * never SVG markup in the page. It is shown from a `data:` address, never a
 * `blob:` one: a blob carries the page's origin, and an SVG opened from it in
 * a tab of its own (dragged to the tab strip, a browser's own menu) would run
 * its script there, beside the tokens; a `data:` document has no origin of
 * its own. Nor is the picture draggable. Reload (`epoch`) reads it again
 * whatever the version says, so a read that failed on its way is not stuck.
 */
export function FileImage(props: {
  sessionId: string;
  path: string;
  version?: string | undefined;
  svg?: boolean;
  epoch?: number;
}) {
  const { t } = useT();
  const env = useSyncExternalStore(subscribeEnv, snapshotEnv, snapshotEnv);
  const [state, setState] = useState({ url: "", error: false });
  const [full, setFull] = useState(false);
  useEffect(() => {
    setState({ url: "", error: false });
    const lease = acquireObjectUrl(
      workspaceUrl(props.sessionId, "raw", props.path) +
        "&v=" +
        encodeURIComponent(props.version || ""),
      props.svg ? { as: "image/svg+xml" } : { accept: RASTER_IMAGE_TYPE },
    );
    let cancelled = false;
    const svg = !!props.svg;
    void lease.promise
      .then(async (url) => {
        if (!svg) return url;
        const blob = objectUrlBlob(url) ?? (await (await fetch(url)).blob());
        return dataUrlOf(blob);
      })
      .then(
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
  }, [props.sessionId, props.path, props.version, props.svg, props.epoch, env]);
  if (state.error)
    return <span role="alert">{t("files.imageUnavailable")}</span>;
  return (
    <span className={"files-image" + (full ? " files-image--full" : "")}>
      <button type="button" onClick={() => setFull((v) => !v)}>
        {t(full ? "files.fit" : "files.actualSize")}
      </button>
      {state.url ? (
        <img
          src={state.url}
          alt={props.path}
          draggable={false}
          // Saved from the picture's menu under its file's name.
          data-image-name={props.path.slice(props.path.lastIndexOf("/") + 1)}
          // Bytes the browser cannot draw (an .svg that is no picture) say so.
          onError={() => setState({ url: "", error: true })}
        />
      ) : (
        <span>{t("files.loading")}</span>
      )}
    </span>
  );
}

/** The bytes of a blob as a `data:` address of its type. */
function dataUrlOf(blob: Blob): Promise<string> {
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result));
    reader.onerror = () => reject(reader.error);
    reader.readAsDataURL(blob);
  });
}
