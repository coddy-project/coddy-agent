import {
  useCallback,
  useEffect,
  useMemo,
  useState,
  type Dispatch,
  type SetStateAction,
} from "react";
import { useT } from "../i18n/I18nProvider";
import {
  clearFinishedBackgroundTasks,
  getBackgroundTask,
  listBackgroundTasks,
  stopBackgroundTask,
} from "./api";
import { tasksPollIntervalMs } from "./taskStatus";
import type { BackgroundTask } from "./types";

/**
 * Background-task drawer state for the current chat session: the task list
 * with its polling loop, the selected task's output, and the elapsed-time
 * clock. The open/selected setters are exposed because hash routing and the
 * nav callbacks (which stay in App) drive them.
 */
export function useBackgroundTasks({ sessionId }: { sessionId: string }): {
  tasksOpen: boolean;
  setTasksOpen: Dispatch<SetStateAction<boolean>>;
  tasksSelectedId: string | null;
  setTasksSelectedId: Dispatch<SetStateAction<string | null>>;
  backgroundTasks: BackgroundTask[];
  backgroundRunning: number;
  backgroundOutput: string;
  backgroundListError: string | null;
  backgroundListLoading: boolean;
  backgroundNowMs: number;
  backgroundTasksByToolCallId: Map<string, BackgroundTask>;
  refreshBackgroundTasks: (opts?: { silent?: boolean }) => Promise<void>;
  refreshBackgroundTaskOutput: (taskId: string) => Promise<void>;
  stopBackgroundTaskById: (taskId: string) => Promise<void>;
  clearFinishedTasks: () => Promise<void>;
} {
  const { t } = useT();
  const [tasksOpen, setTasksOpen] = useState(false);
  const [tasksSelectedId, setTasksSelectedId] = useState<string | null>(null);
  const [backgroundTasks, setBackgroundTasks] = useState<BackgroundTask[]>([]);
  const [backgroundRunning, setBackgroundRunning] = useState(0);
  const [backgroundOutput, setBackgroundOutput] = useState("");
  const [backgroundListError, setBackgroundListError] = useState<string | null>(
    null,
  );
  const [backgroundListLoading, setBackgroundListLoading] = useState(false);
  /** Ticks once a second so elapsed times advance between polls. */
  const [backgroundNowMs, setBackgroundNowMs] = useState(() => Date.now());

  const refreshBackgroundTasks = useCallback(
    async (opts?: { silent?: boolean }) => {
      const sid = sessionId.trim();
      if (!sid) {
        setBackgroundTasks([]);
        setBackgroundRunning(0);
        return;
      }
      const silent = !!opts?.silent;
      if (!silent) {
        setBackgroundListLoading(true);
        setBackgroundListError(null);
      }
      const res = await listBackgroundTasks(sid);
      if (!silent) {
        setBackgroundListLoading(false);
      }
      if (!res.ok) {
        if (!silent) {
          setBackgroundListError(res.message);
          setBackgroundTasks([]);
          setBackgroundRunning(0);
        }
        return;
      }
      setBackgroundListError(null);
      setBackgroundTasks(res.data.data || []);
      setBackgroundRunning(res.data.running || 0);
    },
    [sessionId, t],
  );

  const refreshBackgroundTaskOutput = useCallback(
    async (taskId: string) => {
      const sid = sessionId.trim();
      if (!sid || !taskId) {
        setBackgroundOutput("");
        return;
      }
      const res = await getBackgroundTask(sid, taskId);
      setBackgroundOutput(res.ok ? res.data.output || "" : "");
    },
    [sessionId],
  );

  const stopBackgroundTaskById = useCallback(
    async (taskId: string) => {
      const sid = sessionId.trim();
      if (!sid || !taskId) {
        return;
      }
      const res = await stopBackgroundTask(sid, taskId);
      if (res.ok) {
        setBackgroundOutput(res.data.output || "");
      }
      void refreshBackgroundTasks({ silent: true });
    },
    [sessionId, refreshBackgroundTasks],
  );

  const clearFinishedTasks = useCallback(async () => {
    const sid = sessionId.trim();
    if (!sid) {
      return;
    }
    await clearFinishedBackgroundTasks(sid);
    setTasksSelectedId(null);
    void refreshBackgroundTasks({ silent: true });
  }, [sessionId, refreshBackgroundTasks]);

  // Background tasks outlive the SSE stream of the turn that started them, so
  // the drawer and the nav badge are kept honest by polling rather than by the
  // composer stream. The cadence drops to a slow heartbeat when nothing runs.
  useEffect(() => {
    if (!sessionId.trim()) {
      setBackgroundTasks([]);
      setBackgroundRunning(0);
      setBackgroundOutput("");
      return;
    }
    void refreshBackgroundTasks({ silent: !tasksOpen });
  }, [sessionId, tasksOpen, refreshBackgroundTasks]);

  useEffect(() => {
    if (!sessionId.trim()) {
      return;
    }
    const id = window.setInterval(() => {
      void refreshBackgroundTasks({ silent: true });
      if (tasksOpen && tasksSelectedId) {
        void refreshBackgroundTaskOutput(tasksSelectedId);
      }
    }, tasksPollIntervalMs(backgroundRunning));
    return () => window.clearInterval(id);
  }, [
    sessionId,
    tasksOpen,
    tasksSelectedId,
    backgroundRunning,
    refreshBackgroundTasks,
    refreshBackgroundTaskOutput,
  ]);

  useEffect(() => {
    if (!tasksOpen || !tasksSelectedId) {
      setBackgroundOutput("");
      return;
    }
    void refreshBackgroundTaskOutput(tasksSelectedId);
  }, [tasksOpen, tasksSelectedId, refreshBackgroundTaskOutput]);

  // Elapsed labels must advance between polls, so the clock ticks on its own
  // while something is actually running.
  useEffect(() => {
    if (backgroundRunning <= 0) {
      return;
    }
    const id = window.setInterval(() => setBackgroundNowMs(Date.now()), 1000);
    return () => window.clearInterval(id);
  }, [backgroundRunning]);

  /** Background tasks indexed by the tool call that started them, so a
   *  transcript row can keep ticking after the tool itself returned. */
  const backgroundTasksByToolCallId = useMemo(() => {
    const byToolCall = new Map<string, BackgroundTask>();
    for (const t of backgroundTasks) {
      const tc = (t.tool_call_id || "").trim();
      if (tc) {
        byToolCall.set(tc, t);
      }
    }
    return byToolCall;
  }, [backgroundTasks]);

  return {
    tasksOpen,
    setTasksOpen,
    tasksSelectedId,
    setTasksSelectedId,
    backgroundTasks,
    backgroundRunning,
    backgroundOutput,
    backgroundListError,
    backgroundListLoading,
    backgroundNowMs,
    backgroundTasksByToolCallId,
    refreshBackgroundTasks,
    refreshBackgroundTaskOutput,
    stopBackgroundTaskById,
    clearFinishedTasks,
  };
}
