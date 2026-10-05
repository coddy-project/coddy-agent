import { useEffect, useMemo, useRef, useState } from "react";
import { useT } from "../i18n/I18nProvider";
import { DockTabs } from "../components/DockTabs";
import { Chevron } from "../components/Chevron";
import type { RightDockTab } from "../components/useRightDock";
import { languageForPath } from "../changes/diffLanguage";
import { highlightLine } from "../changes/highlightLine";
import { FileImage } from "./FileImage";
import { FileMarkdown } from "./FileMarkdown";
import { mediaUrl, readMeta, readText, readTree } from "./api";
import type { FileMeta, TextPage, TreePage } from "./api";

function renderer(path: string, type: string): string {
  if (type.startsWith("image/")) return "image";
  if (type.startsWith("audio/")) return "audio";
  if (type.startsWith("video/")) return "video";
  if (type === "application/pdf") return "pdf";
  if (/\.(md|markdown)$/i.test(path)) return "markdown";
  return "text";
}

/** One directory at a time is loaded; search only considers those loaded rows. */
export function FilesPanel(props: {
  sessionId: string;
  initialPath?: string;
  initialLine?: number;
  toolActivity?: number;
  onTab: (tab: RightDockTab) => void;
  onClose: () => void;
  onNavigate?: (path: string, line: number) => void;
}) {
  const { t } = useT();
  const [path, setPath] = useState(props.initialPath || "");
  const [line, setLine] = useState(props.initialLine || 1);
  const [offset, setOffset] = useState(
    Math.floor(((props.initialLine || 1) - 1) / 300) * 300,
  );
  const [directories, setDirectories] = useState<Map<string, TreePage>>(
    new Map(),
  );
  const [expanded, setExpanded] = useState(new Set([""]));
  const [hidden, setHidden] = useState(false);
  const [filter, setFilter] = useState("");
  const [epoch, setEpoch] = useState(0);
  const [meta, setMeta] = useState<FileMeta | null>(null);
  const metaRef = useRef<{ path: string; meta: FileMeta } | null>(null);
  const [text, setText] = useState<TextPage | null>(null);
  const [source, setSource] = useState(false);
  const [wrap, setWrap] = useState(true);
  const [error, setError] = useState("");
  const [treeError, setTreeError] = useState("");
  const [changed, setChanged] = useState(false);
  const [loading, setLoading] = useState(false);
  const [media, setMedia] = useState("");
  const previewRef = useRef<HTMLDivElement | null>(null);

  useEffect(() => {
    setPath(props.initialPath || "");
    setLine(props.initialLine || 1);
    setOffset(Math.floor(((props.initialLine || 1) - 1) / 300) * 300);
  }, [props.initialPath, props.initialLine]);

  useEffect(() => {
    const refresh = () => setEpoch((n) => n + 1);
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

  const activity = props.toolActivity || 0;
  useEffect(() => {
    const abort = new AbortController();
    setTreeError("");
    void Promise.all(
      [...expanded].map(
        async (dir) =>
          [
            dir,
            await readTree(props.sessionId, dir, "", hidden, abort.signal),
          ] as const,
      ),
    )
      .then((rows) => {
        if (abort.signal.aborted) return;
        setDirectories(new Map(rows));
      })
      .catch((err) => {
        if (!abort.signal.aborted) setTreeError(String(err));
      });
    return () => abort.abort();
  }, [props.sessionId, expanded, hidden, epoch, activity]);

  useEffect(() => {
    setError("");
    setMedia("");
    setText(null);
    if (metaRef.current?.path !== path) setMeta(null);
    if (!path) {
      setMeta(null);
      return;
    }
    const abort = new AbortController();
    setLoading(true);
    void (async () => {
      try {
        const previous =
          metaRef.current?.path === path ? metaRef.current.meta : null;
        const next =
          (await readMeta(
            props.sessionId,
            path,
            previous?.etag,
            abort.signal,
          )) || previous;
        if (!next || abort.signal.aborted) return;
        const moved = previous && previous.etag !== next.etag;
        if (moved) {
          setChanged(true);
          if (offset !== 0) {
            setOffset(0);
            return;
          }
        } else if (!previous) setChanged(false);
        metaRef.current = { path, meta: next };
        setMeta(next);
        const kind = renderer(path, next.type);
        if (kind === "audio" || kind === "video") {
          const url = await mediaUrl(
            props.sessionId,
            path,
            false,
            abort.signal,
          );
          if (!abort.signal.aborted) setMedia(url);
        } else if (kind === "text" || kind === "markdown") {
          const page = await readText(
            props.sessionId,
            path,
            offset,
            next.etag,
            abort.signal,
          );
          if (!abort.signal.aborted) setText(page);
        }
      } catch (err) {
        if (!abort.signal.aborted)
          setError(err instanceof Error ? err.message : String(err));
      } finally {
        if (!abort.signal.aborted) setLoading(false);
      }
    })();
    return () => abort.abort();
  }, [props.sessionId, path, offset, epoch, activity]);

  useEffect(() => {
    if (!text) return;
    previewRef.current
      ?.querySelector(`[data-file-line="${line}"]`)
      ?.scrollIntoView({ block: "center" });
  }, [text, line]);

  const select = (value: string) => {
    setPath(value);
    setLine(1);
    setOffset(0);
    setSource(false);
    props.onNavigate?.(value, 1);
  };
  const loadMore = async (dir: string, page: TreePage) => {
    try {
      const next = await readTree(
        props.sessionId,
        dir,
        page.next_cursor,
        hidden,
      );
      setDirectories((prev) =>
        new Map(prev).set(dir, {
          ...next,
          entries: [...page.entries, ...next.entries],
        }),
      );
    } catch (err) {
      setTreeError(String(err));
    }
  };
  const tree = (dir: string, depth = 0): React.ReactNode => {
    const page = directories.get(dir);
    if (!page) return null;
    return (
      <div
        key={dir}
        className="files-tree-level"
        style={{ paddingLeft: depth ? 12 : 0 }}
      >
        {page.entries.map((entry) => (
          <div key={entry.path_rel}>
            <button
              type="button"
              className={
                "files-tree-row" + (entry.path_rel === path ? " is-active" : "")
              }
              title={entry.path_rel}
              disabled={entry.kind === "symlink" || entry.kind === "special"}
              onClick={() =>
                entry.kind === "directory"
                  ? setExpanded((prev) => {
                      const next = new Set(prev);
                      if (next.has(entry.path_rel)) next.delete(entry.path_rel);
                      else next.add(entry.path_rel);
                      return next;
                    })
                  : select(entry.path_rel)
              }
            >
              {entry.kind === "directory" ? (
                <Chevron open={expanded.has(entry.path_rel)} />
              ) : (
                <span aria-hidden>·</span>
              )}{" "}
              {entry.name}
            </button>
            {entry.kind === "directory" && expanded.has(entry.path_rel)
              ? tree(entry.path_rel, depth + 1)
              : null}
          </div>
        ))}
        {page.has_more ? (
          <button type="button" onClick={() => void loadMore(dir, page)}>
            {t("files.moreEntries")}
          </button>
        ) : null}
      </div>
    );
  };
  const filtered = useMemo(
    () =>
      [
        ...new Map(
          [...directories.values()]
            .flatMap((p) => p.entries)
            .map((e) => [e.path_rel, e]),
        ).values(),
      ].filter((e) => e.path_rel.toLowerCase().includes(filter.toLowerCase())),
    [directories, filter],
  );
  const kind = meta ? renderer(path, meta.type) : "";
  const language = languageForPath(path);
  const highlightPage = useMemo(
    () =>
      (text?.lines.reduce((sum, value) => sum + value.length, 0) || 0) <=
      200000,
    [text],
  );
  const download = async () => {
    try {
      const url = await mediaUrl(props.sessionId, path, true);
      const anchor = document.createElement("a");
      anchor.href = url;
      anchor.rel = "noopener noreferrer";
      anchor.referrerPolicy = "no-referrer";
      anchor.click();
    } catch (err) {
      setError(String(err));
    }
  };

  return (
    <aside
      className="files-panel"
      data-testid="files-panel"
      aria-label={t("files.title")}
    >
      <div className="sessions-head files-panel-head">
        <DockTabs tab="files" onTab={props.onTab} />
        <button
          type="button"
          className="sessions-close"
          onClick={props.onClose}
          aria-label={t("files.close")}
        >
          ×
        </button>
      </div>
      <div className="files-tree-tools">
        <input
          value={filter}
          onChange={(e) => setFilter(e.target.value)}
          placeholder={t("files.search")}
          aria-label={t("files.search")}
        />
        <label>
          <input
            type="checkbox"
            checked={hidden}
            onChange={(e) => setHidden(e.target.checked)}
          />
          {t("files.hidden")}
        </label>
      </div>
      <div className="files-tree" data-testid="files-tree">
        {treeError ? <p role="alert">{treeError}</p> : null}
        {filter
          ? filtered.map((e) => (
              <button
                key={e.path_rel}
                type="button"
                className="files-tree-row"
                disabled={e.kind !== "file"}
                onClick={() => select(e.path_rel)}
              >
                {e.path_rel}
              </button>
            ))
          : tree("")}
      </div>
      <div className="files-preview" ref={previewRef}>
        {path ? (
          <>
            <div className="files-preview-head">
              <strong title={path}>{path}</strong>
              <button type="button" onClick={() => setEpoch((n) => n + 1)}>
                {t("files.reload")}
              </button>
              <button type="button" onClick={() => void download()}>
                {t("files.download")}
              </button>
            </div>
            {meta ? (
              <div className="files-meta">
                <time dateTime={meta.modTime}>
                  {new Date(meta.modTime).toLocaleString()}
                </time>
                <span>
                  {meta.size.toLocaleString()} {t("files.bytes")}
                </span>
              </div>
            ) : null}
            {changed ? (
              <p role="status" className="files-changed">
                {t("files.changed")}
              </p>
            ) : null}
            {loading ? <p>{t("files.loading")}</p> : null}
            {error ? (
              <p role="alert">
                {error}
                <br />
                {t("files.binary")}
              </p>
            ) : null}
            {kind === "image" && meta ? (
              <FileImage
                sessionId={props.sessionId}
                path={path}
                version={meta.etag}
              />
            ) : null}
            {kind === "audio" && media ? (
              <audio src={media} controls preload="metadata" />
            ) : null}
            {kind === "video" && media ? (
              <video src={media} controls preload="metadata" playsInline />
            ) : null}
            {kind === "pdf" ? <p>{t("files.pdfDownload")}</p> : null}
            {text ? (
              <>
                <div className="files-text-tools">
                  {kind === "markdown" ? (
                    <button type="button" onClick={() => setSource((v) => !v)}>
                      {t(source ? "files.rendered" : "files.source")}
                    </button>
                  ) : null}
                  <label>
                    <input
                      type="checkbox"
                      checked={wrap}
                      onChange={(e) => setWrap(e.target.checked)}
                    />
                    {t("files.wrap")}
                  </label>
                  <label>
                    {t("files.line")}{" "}
                    <input
                      type="number"
                      min={1}
                      value={line}
                      onChange={(e) => {
                        const n = Math.max(
                          1,
                          Math.floor(Number(e.target.value)) || 1,
                        );
                        setLine(n);
                        setOffset(Math.floor((n - 1) / 300) * 300);
                        props.onNavigate?.(path, n);
                      }}
                    />
                  </label>
                </div>
                {kind === "markdown" && !source ? (
                  <FileMarkdown
                    sessionId={props.sessionId}
                    path={path}
                    text={text.lines.join("\n")}
                    version={`${meta?.etag || ""}:${epoch}:${activity}`}
                  />
                ) : (
                  <div
                    className={"files-code" + (wrap ? " files-code--wrap" : "")}
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
                          className={no === line ? "is-active" : ""}
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
                <div className="files-pages">
                  <button
                    type="button"
                    disabled={!offset}
                    onClick={() => setOffset(Math.max(0, offset - 300))}
                  >
                    {t("files.previous")}
                  </button>
                  <span>
                    {text.offset + 1}–{text.next_offset}
                  </span>
                  <button
                    type="button"
                    disabled={!text.has_more}
                    onClick={() => setOffset(text.next_offset)}
                  >
                    {t("files.next")}
                  </button>
                </div>
              </>
            ) : null}
          </>
        ) : (
          <p>{t("files.choose")}</p>
        )}
      </div>
    </aside>
  );
}
