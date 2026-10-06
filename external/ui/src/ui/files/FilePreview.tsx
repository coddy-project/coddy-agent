import { useEffect, useMemo, useRef, useState } from "react";
import { useT } from "../i18n/I18nProvider";
import { languageForPath } from "../changes/diffLanguage";
import { highlightLine } from "../changes/highlightLine";
import { FileImage } from "./FileImage";
import { FileMarkdown } from "./FileMarkdown";
import { HttpError, mediaUrl, readMeta, readText } from "./api";
import type { FileMeta, TextPage } from "./api";

/** How many lines one text window holds. */
const PAGE_LINES = 300;

/** A signed media address lives an hour; a check after this long signs a new one. */
const MEDIA_RENEW_MS = 50 * 60 * 1000;

type Kind = "image" | "audio" | "video" | "pdf" | "markdown" | "text";

function renderer(path: string, type: string): Kind {
  if (type.startsWith("image/")) return "image";
  if (type.startsWith("audio/")) return "audio";
  if (type.startsWith("video/")) return "video";
  if (type === "application/pdf") return "pdf";
  if (/\.(md|markdown)$/i.test(path)) return "markdown";
  return "text";
}

function pageOf(line: number): number {
  return Math.floor((Math.max(1, line) - 1) / PAGE_LINES) * PAGE_LINES;
}

/** The folder part of a path with its trailing slash, empty at the root. */
export function folderOf(path: string): string {
  const cut = path.lastIndexOf("/");
  return cut < 0 ? "" : path.slice(0, cut + 1);
}

/** The last segment of a path. */
export function nameOf(path: string): string {
  return path.slice(path.lastIndexOf("/") + 1) || path;
}

/**
 * One open file of the Files window: its head (where it is, how big, when it
 * changed, how to see it) over its body. A text file is read a window of
 * lines at a time; a picture comes through the authenticated reader; audio
 * and video play from a short-lived signed address, which a swarm relay
 * carries to the node as it is (docs/operate/swarm.md).
 *
 * The preview checks the file again when the page comes back into view and
 * when a tool call of the turn finishes (`activity`), with its ETag: a file that
 * did not change is not read again, so the text stays where the reader is and a
 * sound or a film keeps playing from the address it has. A file rewritten under
 * it says so and starts over at its top, rather than splicing lines of two
 * versions together. Reload (`epoch`) reads it again whatever the ETag says and
 * signs a new media address; so does a media element that fails, once, which is
 * what an address past its hour does.
 */
export function FilePreview(props: {
  sessionId: string;
  path: string;
  line: number;
  wrap: boolean;
  epoch: number;
  activity: number;
  onLine: (line: number) => void;
  onError?: (message: string) => void;
}) {
  const { t } = useT();
  const { sessionId, path } = props;
  const [offset, setOffset] = useState(pageOf(props.line));
  const [meta, setMeta] = useState<FileMeta | null>(null);
  const metaRef = useRef<{ path: string; meta: FileMeta } | null>(null);
  const [text, setText] = useState<TextPage | null>(null);
  const [source, setSource] = useState(false);
  const [error, setError] = useState("");
  // The failed read was of a file that is not text (415), not a network or
  // permission failure: only then does the notice offer the download.
  const [binary, setBinary] = useState(false);
  // What the line field shows while it is typed in; it moves the file on Enter.
  const [lineDraft, setLineDraft] = useState(String(props.line));
  const [changed, setChanged] = useState(false);
  const [loading, setLoading] = useState(false);
  const [media, setMedia] = useState("");
  const [focusEpoch, setFocusEpoch] = useState(0);
  // Bumps when a media element failed: its signed address may have expired.
  // At most once a minute, so a file the browser cannot play does not loop.
  const [mediaRetry, setMediaRetry] = useState(0);
  const lastRetryRef = useRef(0);
  const retryMedia = () => {
    if (Date.now() - lastRetryRef.current < 60_000) return;
    lastRetryRef.current = Date.now();
    setMediaRetry((n) => n + 1);
  };
  const bodyRef = useRef<HTMLDivElement | null>(null);
  // What the last read showed: the file, its page, the Reload count and the
  // media retry it answered, and whether text or media came of it.
  const shownRef = useRef<{
    path: string;
    offset: number;
    epoch: number;
    retry: number;
    content: boolean;
    /** When the media address on show was signed, 0 for anything else. */
    signedAt: number;
  } | null>(null);

  // A line asked for from outside (an address, a link) moves the window to it.
  useEffect(() => {
    setOffset(pageOf(props.line));
  }, [path, props.line]);

  useEffect(() => {
    setSource(false);
  }, [path]);

  // The notice that the file changed stays until another file or Reload.
  useEffect(() => {
    setChanged(false);
  }, [path, props.epoch]);

  useEffect(() => {
    setLineDraft(String(props.line));
  }, [props.line]);

  useEffect(() => {
    const refresh = () => setFocusEpoch((n) => n + 1);
    window.addEventListener("focus", refresh);
    const visible = () => {
      if (document.visibilityState === "visible") refresh();
    };
    document.addEventListener("visibilitychange", visible);
    return () => {
      window.removeEventListener("focus", refresh);
      document.removeEventListener("visibilitychange", visible);
    };
  }, []);

  useEffect(() => {
    const shown = shownRef.current;
    const samePath = shown?.path === path;
    // Reload, another page of lines, a failed media element: read the content
    // again even when the file is the same. A plain check keeps what is shown.
    const reread =
      !samePath ||
      shown.offset !== offset ||
      shown.epoch !== props.epoch ||
      shown.retry !== mediaRetry ||
      !shown.content ||
      (shown.signedAt > 0 && Date.now() - shown.signedAt > MEDIA_RENEW_MS);
    if (!samePath) {
      setMedia("");
      setText(null);
      setMeta(null);
    }
    setError("");
    setBinary(false);
    const abort = new AbortController();
    setLoading(true);
    void (async () => {
      try {
        const previous =
          metaRef.current?.path === path ? metaRef.current.meta : null;
        const fresh = await readMeta(
          sessionId,
          path,
          previous?.etag,
          abort.signal,
        );
        if (abort.signal.aborted) return;
        const next = fresh || previous;
        if (!next) return;
        const moved = !!previous && !!fresh && previous.etag !== fresh.etag;
        if (moved) {
          setChanged(true);
          if (offset !== 0) {
            metaRef.current = { path, meta: next };
            setOffset(0);
            return;
          }
        }
        metaRef.current = { path, meta: next };
        setMeta(next);
        if (!moved && !reread) return;
        const kind = renderer(path, next.type);
        let content = false;
        let signedAt = 0;
        if (kind === "audio" || kind === "video") {
          const url = await mediaUrl(sessionId, path, false, abort.signal);
          if (abort.signal.aborted) return;
          setMedia(url);
          content = true;
          signedAt = Date.now();
        } else if (kind === "text" || kind === "markdown") {
          const page = await readText(
            sessionId,
            path,
            offset,
            next.etag,
            abort.signal,
          );
          if (abort.signal.aborted) return;
          setText(page);
          content = true;
        } else {
          content = true;
        }
        shownRef.current = {
          path,
          offset,
          epoch: props.epoch,
          retry: mediaRetry,
          content,
          signedAt,
        };
      } catch (err) {
        if (!abort.signal.aborted) {
          const message = err instanceof Error ? err.message : String(err);
          setError(message);
          setBinary(err instanceof HttpError && err.status === 415);
          props.onError?.(message);
        }
      } finally {
        if (!abort.signal.aborted) setLoading(false);
      }
    })();
    return () => abort.abort();
    // props.onError is a report, not an input of the read.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [
    sessionId,
    path,
    offset,
    props.epoch,
    props.activity,
    focusEpoch,
    mediaRetry,
  ]);

  useEffect(() => {
    if (!text) return;
    bodyRef.current
      ?.querySelector(`[data-file-line="${props.line}"]`)
      ?.scrollIntoView({ block: "center" });
  }, [text, props.line]);

  const commitLine = () => {
    const n = Math.max(1, Math.floor(Number(lineDraft)) || 1);
    setLineDraft(String(n));
    if (n !== props.line) props.onLine(n);
  };

  const kind = meta ? renderer(path, meta.type) : null;
  const language = languageForPath(path);
  const highlightPage = useMemo(
    () =>
      (text?.lines.reduce((sum, value) => sum + value.length, 0) || 0) <=
      200000,
    [text],
  );

  return (
    <div className="files-file" data-testid="files-file">
      <div className="files-file-head">
        <div className="files-file-path" title={path}>
          <span className="files-file-dir">{folderOf(path)}</span>
          <span className="files-file-name">{nameOf(path)}</span>
        </div>
        {meta ? (
          <div className="files-file-meta">
            <span>
              {meta.size.toLocaleString()} {t("files.bytes")}
            </span>
            {meta.modTime ? (
              <time dateTime={new Date(meta.modTime).toISOString()}>
                {new Date(meta.modTime).toLocaleString()}
              </time>
            ) : null}
          </div>
        ) : null}
        <div className="files-file-tools">
          {kind === "markdown" && text ? (
            <span className="files-seg" role="group">
              <button
                type="button"
                className={!source ? "is-active" : ""}
                aria-pressed={!source}
                onClick={() => setSource(false)}
              >
                {t("files.rendered")}
              </button>
              <button
                type="button"
                className={source ? "is-active" : ""}
                aria-pressed={source}
                onClick={() => setSource(true)}
              >
                {t("files.source")}
              </button>
            </span>
          ) : null}
          {text && (kind === "text" || source) ? (
            <label className="files-line-jump">
              {t("files.line")}
              <input
                type="number"
                min={1}
                value={lineDraft}
                onChange={(e) => setLineDraft(e.target.value)}
                onKeyDown={(e) => {
                  if (e.key === "Enter") commitLine();
                }}
                onBlur={commitLine}
              />
            </label>
          ) : null}
        </div>
      </div>
      <div className="files-file-body" ref={bodyRef}>
        {changed ? (
          <p role="status" className="files-changed">
            {t("files.changed")}
          </p>
        ) : null}
        {loading && !text && !media && kind !== "image" ? (
          <p className="files-note">{t("files.loading")}</p>
        ) : null}
        {error ? (
          <p role="alert" className="files-note">
            {error}
            {binary ? (
              <>
                <br />
                {t("files.binary")}
              </>
            ) : null}
          </p>
        ) : null}
        {kind === "image" && meta ? (
          <FileImage sessionId={sessionId} path={path} version={meta.etag} />
        ) : null}
        {kind === "audio" && media ? (
          <audio
            src={media}
            controls
            preload="metadata"
            onError={retryMedia}
          />
        ) : null}
        {kind === "video" && media ? (
          <video
            src={media}
            controls
            preload="metadata"
            playsInline
            onError={retryMedia}
          />
        ) : null}
        {kind === "pdf" ? (
          <p className="files-note">{t("files.pdfDownload")}</p>
        ) : null}
        {text && (kind === "text" || kind === "markdown") ? (
          <>
            {kind === "markdown" && !source ? (
              <FileMarkdown
                sessionId={sessionId}
                path={path}
                text={text.lines.join("\n")}
                version={`${meta?.etag || ""}:${props.epoch}`}
              />
            ) : (
              <div
                className={
                  "files-code" + (props.wrap ? " files-code--wrap" : "")
                }
              >
                {text.lines.map((value, i) => {
                  const no = text.offset + i + 1;
                  const spans =
                    value.length <= 4096 && highlightPage
                      ? highlightLine(value, language)
                      : null;
                  return (
                    <div
                      key={no}
                      data-file-line={no}
                      className={no === props.line ? "is-active" : ""}
                    >
                      <span className="files-line-no">{no}</span>
                      <code>
                        {spans
                          ? spans.map((span, n) => (
                              <span
                                key={n}
                                className={span.className || undefined}
                              >
                                {span.text}
                              </span>
                            ))
                          : value || " "}
                      </code>
                    </div>
                  );
                })}
              </div>
            )}
            {offset > 0 || text.has_more ? (
              <div className="files-pages">
                <button
                  type="button"
                  disabled={!offset}
                  onClick={() => props.onLine(Math.max(0, offset - PAGE_LINES) + 1)}
                >
                  {t("files.previous")}
                </button>
                <span>
                  {text.offset + 1}–{text.next_offset}
                </span>
                <button
                  type="button"
                  disabled={!text.has_more}
                  onClick={() => props.onLine(text.next_offset + 1)}
                >
                  {t("files.next")}
                </button>
              </div>
            ) : null}
          </>
        ) : null}
      </div>
    </div>
  );
}
