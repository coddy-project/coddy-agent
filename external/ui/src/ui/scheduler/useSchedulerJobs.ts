import {
  useCallback,
  useEffect,
  useMemo,
  useState,
  type Dispatch,
  type SetStateAction,
} from "react";
import { useT } from "../i18n/I18nProvider";
import { schedulerCancelJob, schedulerListJobs, schedulerRunJob } from "./api";
import { setSessionHashInLocation } from "./hashRoute";
import type { SchedulerInfo, SchedulerJob } from "./types";

/** Poll job list while scheduler UI is open (running, next_run_utc, paused). */
const SCHEDULER_JOBS_POLL_MS = 12_000;

export type SchedulerEditorState =
  | null
  | { mode: "create" }
  | { mode: "edit"; jobId: string };

/**
 * Scheduler drawer state: the availability probe, the job list with its
 * poll/refresh loop, the search filter, and the run/cancel actions. The
 * open/editor setters are exposed because hash routing and the nav callbacks
 * (which stay in App) drive them.
 */
export function useSchedulerJobs({ sessionId }: { sessionId: string }): {
  schedulerHttpLinked: boolean | null;
  schedulerOpen: boolean;
  setSchedulerOpen: Dispatch<SetStateAction<boolean>>;
  schedulerEditor: SchedulerEditorState;
  setSchedulerEditor: Dispatch<SetStateAction<SchedulerEditorState>>;
  schedulerInfo: SchedulerInfo | null;
  filteredSchedulerJobs: SchedulerJob[];
  schedulerListError: string | null;
  schedulerListLoading: boolean;
  schedulerFilterDraft: string;
  setSchedulerFilterDraft: Dispatch<SetStateAction<string>>;
  refreshSchedulerJobs: (opts?: { silent?: boolean }) => Promise<void>;
  onSchedulerRunJob: (jobId: string) => Promise<void>;
  onSchedulerCancelJob: (jobId: string) => Promise<void>;
} {
  const { t } = useT();
  /** null until first probe of /coddy/scheduler/jobs; false when route returns 404 (binary without scheduler). */
  const [schedulerHttpLinked, setSchedulerHttpLinked] = useState<
    boolean | null
  >(null);
  const [schedulerOpen, setSchedulerOpen] = useState(false);
  const [schedulerEditor, setSchedulerEditor] =
    useState<SchedulerEditorState>(null);
  const [schedulerJobs, setSchedulerJobs] = useState<SchedulerJob[]>([]);
  const [schedulerInfo, setSchedulerInfo] = useState<SchedulerInfo | null>(
    null,
  );
  const [schedulerListError, setSchedulerListError] = useState<string | null>(
    null,
  );
  const [schedulerListLoading, setSchedulerListLoading] = useState(false);
  const [schedulerFilterDraft, setSchedulerFilterDraft] = useState("");
  const [schedulerFilterQ, setSchedulerFilterQ] = useState("");

  const refreshSchedulerJobs = useCallback(
    async (opts?: { silent?: boolean }) => {
      const silent = !!opts?.silent;
      if (!silent) {
        setSchedulerListLoading(true);
        setSchedulerListError(null);
      }
      const res = await schedulerListJobs(false);
      if (!silent) {
        setSchedulerListLoading(false);
      }
      if (!res.ok) {
        let msg = res.message;
        if (res.status === 404) {
          setSchedulerHttpLinked(false);
          setSchedulerOpen(false);
          setSchedulerEditor(null);
          msg = t("scheduler.apiNotAvailable");
          const sid = sessionId.trim();
          if (sid) {
            setSessionHashInLocation(sid);
          } else if (window.location.hash) {
            history.replaceState(
              null,
              "",
              `${window.location.pathname}${window.location.search}`,
            );
          }
          setSchedulerListError(msg);
          setSchedulerJobs([]);
          setSchedulerInfo(null);
          return;
        }
        if (res.status === 503) {
          msg = t("scheduler.disabled");
          if (!silent) {
            setSchedulerListError(msg);
            setSchedulerJobs([]);
            setSchedulerInfo(null);
          }
          return;
        }
        if (!silent) {
          setSchedulerListError(msg);
          setSchedulerJobs([]);
          setSchedulerInfo(null);
        }
        return;
      }
      setSchedulerInfo(res.data.scheduler);
      setSchedulerJobs(res.data.jobs || []);
    },
    [sessionId, t],
  );

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const r = await fetch("/coddy/scheduler/jobs");
        if (cancelled) {
          return;
        }
        setSchedulerHttpLinked(r.status !== 404);
      } catch {
        if (!cancelled) {
          setSchedulerHttpLinked(true);
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    if (!schedulerOpen || schedulerHttpLinked === false) {
      return;
    }
    void refreshSchedulerJobs();
  }, [schedulerOpen, schedulerHttpLinked, refreshSchedulerJobs]);

  useEffect(() => {
    if (!schedulerOpen || schedulerHttpLinked !== true) {
      return;
    }
    const id = window.setInterval(() => {
      void refreshSchedulerJobs({ silent: true });
    }, SCHEDULER_JOBS_POLL_MS);
    return () => window.clearInterval(id);
  }, [schedulerOpen, schedulerHttpLinked, refreshSchedulerJobs]);

  useEffect(() => {
    const t = window.setTimeout(
      () => setSchedulerFilterQ(schedulerFilterDraft.trim()),
      200,
    );
    return () => window.clearTimeout(t);
  }, [schedulerFilterDraft]);

  const onSchedulerRunJob = useCallback(
    async (jobId: string) => {
      const r = await schedulerRunJob(jobId);
      if (!r.ok) {
        setSchedulerListError(r.message);
        return;
      }
      void refreshSchedulerJobs({ silent: true });
    },
    [refreshSchedulerJobs],
  );

  const onSchedulerCancelJob = useCallback(
    async (jobId: string) => {
      const r = await schedulerCancelJob(jobId);
      if (!r.ok) {
        setSchedulerListError(r.message);
        return;
      }
      void refreshSchedulerJobs({ silent: true });
    },
    [refreshSchedulerJobs],
  );

  const filteredSchedulerJobs = useMemo(() => {
    const q = schedulerFilterQ.trim().toLowerCase();
    if (!q) {
      return schedulerJobs;
    }
    return schedulerJobs.filter((j) => {
      const id = (j.job_id || "").toLowerCase();
      const desc = (j.description || "").toLowerCase();
      return id.includes(q) || desc.includes(q);
    });
  }, [schedulerJobs, schedulerFilterQ]);

  return {
    schedulerHttpLinked,
    schedulerOpen,
    setSchedulerOpen,
    schedulerEditor,
    setSchedulerEditor,
    schedulerInfo,
    filteredSchedulerJobs,
    schedulerListError,
    schedulerListLoading,
    schedulerFilterDraft,
    setSchedulerFilterDraft,
    refreshSchedulerJobs,
    onSchedulerRunJob,
    onSchedulerCancelJob,
  };
}
