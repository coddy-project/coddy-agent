import { useEffect, useState, useSyncExternalStore } from "react";
import { useT } from "../i18n/I18nProvider";
import { snapshotEnv, subscribeEnv } from "../env/remoteEnv";
import { workspaceUrl } from "./api";
import { PDF_TYPE, acquireObjectUrl } from "./objectUrl";

/** The most a PDF preview holds in memory. */
export const PDF_BYTE_CAP = 50 * 1024 * 1024;

/**
 * Whether this browser shows a PDF itself (`navigator.pdfViewerEnabled`):
 * Chrome, Edge, Firefox and Safari on a computer do; Chrome on Android does
 * not, and there a PDF stays a download.
 */
export function pdfViewerAvailable(): boolean {
  return (
    typeof navigator !== "undefined" && navigator.pdfViewerEnabled === true
  );
}

/**
 * A PDF of the workspace in the browser's own viewer. Its bytes come through
 * the authenticated reader (so a remote environment and a swarm relay work as
 * for a picture) and only as a PDF, into a `blob:` address the frame shows.
 * The frame has no `sandbox`: every engine's viewer refuses to run inside one,
 * so the viewer itself is what stands between the document and the page. A
 * `blob:` address belongs to the web UI's origin, where the node's raw route
 * would serve the same PDF in its own origin under a sandbox policy, so a flaw
 * in the browser's viewer would reach further from here: the risk the
 * operator accepted for a preview (docs/plans/file-viewer.md, section 16). A
 * read that fails, or a file past `PDF_BYTE_CAP`, falls back to the download
 * notice.
 */
export function FilePdf(props: {
  sessionId: string;
  path: string;
  version: string;
  /** Reload: read again whatever the version says. */
  epoch: number;
}) {
  const { t } = useT();
  const env = useSyncExternalStore(subscribeEnv, snapshotEnv, snapshotEnv);
  const [state, setState] = useState({ url: "", error: false });
  useEffect(() => {
    setState({ url: "", error: false });
    const lease = acquireObjectUrl(
      workspaceUrl(props.sessionId, "raw", props.path) +
        "&v=" +
        encodeURIComponent(props.version),
      { cap: PDF_BYTE_CAP, accept: PDF_TYPE },
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
  }, [props.sessionId, props.path, props.version, props.epoch, env]);
  if (state.error)
    return (
      <p role="alert" className="files-note">
        {t("files.pdfDownload")}
      </p>
    );
  if (!state.url) return <p className="files-note">{t("files.loading")}</p>;
  return (
    <iframe
      className="files-pdf"
      src={state.url}
      title={props.path.slice(props.path.lastIndexOf("/") + 1)}
      referrerPolicy="no-referrer"
    />
  );
}
