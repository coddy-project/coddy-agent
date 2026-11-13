import React, { useEffect, useRef, useState } from "react";
import { useT } from "../i18n/I18nProvider";
import { getLocale } from "../i18n/i18n";
import { BranchIcon } from "./workspaceIcons";
import { GitDialog } from "./GitDialog";
import {
  layoutGitGraph,
  type GitCommit,
  type GitHistoryPage,
} from "./gitGraph";

export function GitGraphDialog(props: { path: string; onClose: () => void }) {
  const { t } = useT();
  const [commits, setCommits] = useState<GitCommit[]>([]);
  const [hasMore, setHasMore] = useState(false);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const request = useRef<AbortController | null>(null);
  async function load(append = false) {
    request.current?.abort();
    const controller = new AbortController();
    request.current = controller;
    setLoading(true);
    setError("");
    try {
      const query = new URLSearchParams({
        path: props.path,
        offset: String(append ? commits.length : 0),
        limit: "100",
      });
      const response = await fetch(`/coddy/workspace/graph?${query}`, {
        signal: controller.signal,
      });
      if (!response.ok) {
        const body = await response.json().catch(() => null);
        throw new Error(body?.error?.message || t("git.graphFailed"));
      }
      const page = (await response.json()) as GitHistoryPage;
      if (controller.signal.aborted) return;
      setCommits((old) => (append ? [...old, ...page.commits] : page.commits));
      setHasMore(page.has_more);
    } catch (err) {
      if (!controller.signal.aborted)
        setError(err instanceof Error ? err.message : t("git.graphFailed"));
    } finally {
      if (!controller.signal.aborted) setLoading(false);
    }
  }
  useEffect(() => {
    setCommits([]);
    setHasMore(false);
    void load();
    return () => request.current?.abort();
  }, [props.path]);
  const rows = layoutGitGraph(commits);
  const graphWidth = Math.max(80, ...rows.map((row) => 24 + row.width * 18));
  const date = new Intl.DateTimeFormat(getLocale(), {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
  return (
    <GitDialog
      title={t("git.graph")}
      wide
      onClose={props.onClose}
      toolbar={
        <button
          type="button"
          className="git-dialog-icon"
          disabled={loading}
          aria-label={t("git.refresh")}
          onClick={() => void load()}
        >
          ↻
        </button>
      }
    >
      {error ? (
        <p role="alert" className="git-dialog-error">
          {error}
        </p>
      ) : null}
      <div
        className="git-graph-scroll"
        tabIndex={0}
        aria-label={t("git.graph")}
        aria-busy={loading}
      >
        <table
          className="git-graph-table"
          style={{ minWidth: graphWidth + 720 }}
        >
          <thead>
            <tr>
              <th style={{ width: graphWidth }}>{t("git.graphColumn")}</th>
              <th>{t("git.description")}</th>
              <th>{t("git.date")}</th>
              <th>{t("git.author")}</th>
              <th>{t("git.commit")}</th>
            </tr>
          </thead>
          <tbody>
            {commits.map((commit, index) => {
              const row = rows[index]!;
              return (
                <tr
                  key={commit.hash}
                  className={commit.refs.includes("HEAD") ? "is-head" : ""}
                >
                  <td className="git-graph-cell">
                    <svg width={graphWidth} height={42} aria-hidden="true">
                      {row.incoming ? (
                        <path
                          className={`git-graph-color-${row.color % 6}`}
                          d={`M ${14 + row.lane * 18} 0 V 21`}
                        />
                      ) : null}
                      {row.edges.map((edge, i) => (
                        <path
                          key={i}
                          className={`git-graph-color-${edge.color % 6}`}
                          d={`M ${14 + edge.from * 18} ${edge.from === row.lane ? 21 : 0} L ${14 + edge.from * 18} 21 C ${14 + edge.from * 18} 32, ${14 + edge.to * 18} 30, ${14 + edge.to * 18} 42`}
                        />
                      ))}
                      <circle
                        className={`git-graph-color-${row.color % 6}`}
                        cx={14 + row.lane * 18}
                        cy={21}
                        r={commit.refs.includes("HEAD") ? 5 : 3.5}
                      />
                    </svg>
                  </td>
                  <td>
                    <div className="git-graph-description">
                      {commit.refs.map((ref) => (
                        <span
                          key={ref}
                          title={ref}
                          className={`git-graph-ref${ref === "HEAD" ? " git-graph-ref--head" : ""}`}
                        >
                          <BranchIcon />
                          <span>{ref}</span>
                        </span>
                      ))}
                      <span
                        className="git-graph-subject"
                        title={commit.subject}
                      >
                        {commit.subject}
                      </span>
                    </div>
                  </td>
                  <td title={new Date(commit.date).toLocaleString(getLocale())}>
                    {date.format(new Date(commit.date))}
                  </td>
                  <td title={commit.author}>{commit.author}</td>
                  <td className="git-graph-hash" title={commit.hash}>
                    {commit.hash.slice(0, 7)}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
        {!commits.length && !loading && !error ? (
          <p className="git-graph-empty">{t("git.empty")}</p>
        ) : null}
      </div>
      <footer className="git-graph-footer">
        {loading ? (
          <span role="status">{t("git.loading")}</span>
        ) : (
          <span>{t("git.commitCount", { count: commits.length })}</span>
        )}
        {hasMore ? (
          <button
            type="button"
            className="workspace-modal-btn"
            disabled={loading}
            onClick={() => void load(true)}
          >
            {t("git.loadMore")}
          </button>
        ) : null}
      </footer>
    </GitDialog>
  );
}
