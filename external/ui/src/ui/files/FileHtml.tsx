import { useEffect, useState, useSyncExternalStore } from "react";
import { useT } from "../i18n/I18nProvider";
import { snapshotEnv, subscribeEnv } from "../env/remoteEnv";
import { TooLargeError, readWholeText } from "./api";
import { htmlPreviewDocument } from "./htmlPreview";

/** The most of a page the preview reads, in characters. */
export const HTML_PREVIEW_CHARS = 2_000_000;

/**
 * An HTML file of the workspace drawn as a page, in a frame that runs nothing
 * (`sandbox=""`: no script, no form, no window, an origin that is no one's)
 * over a document that loads nothing (`htmlPreviewDocument`). The file is read
 * whole through the text reader, for the version the window checked, and
 * again when that version or Reload (`epoch`) moves. A page past
 * `HTML_PREVIEW_CHARS` says so; its source is a menu choice away.
 */
export function FileHtml(props: {
  sessionId: string;
  path: string;
  version: string;
  epoch: number;
}) {
  const { t } = useT();
  const env = useSyncExternalStore(subscribeEnv, snapshotEnv, snapshotEnv);
  const [state, setState] = useState({ doc: "", error: "", tooLarge: false });
  useEffect(() => {
    setState({ doc: "", error: "", tooLarge: false });
    const abort = new AbortController();
    void (async () => {
      try {
        const text = await readWholeText(
          props.sessionId,
          props.path,
          props.version,
          HTML_PREVIEW_CHARS,
          abort.signal,
        );
        if (abort.signal.aborted) return;
        setState({
          doc: htmlPreviewDocument(text),
          error: "",
          tooLarge: false,
        });
      } catch (err) {
        if (abort.signal.aborted) return;
        setState({
          doc: "",
          error: err instanceof Error ? err.message : String(err),
          tooLarge: err instanceof TooLargeError,
        });
      }
    })();
    return () => abort.abort();
  }, [props.sessionId, props.path, props.version, props.epoch, env]);
  if (state.error)
    return (
      <p role="alert" className="files-note">
        {state.tooLarge ? t("files.htmlTooLarge") : state.error}
      </p>
    );
  if (!state.doc) return <p className="files-note">{t("files.loading")}</p>;
  return (
    <iframe
      className="files-html"
      sandbox=""
      srcDoc={state.doc}
      title={props.path.slice(props.path.lastIndexOf("/") + 1)}
      referrerPolicy="no-referrer"
    />
  );
}
