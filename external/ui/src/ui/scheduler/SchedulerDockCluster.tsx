import type { Dispatch, RefObject, SetStateAction } from "react";
import {
  setSchedulerCreateHash,
  setSchedulerJobHash,
  setSchedulerListHash,
} from "./hashRoute";
import { SchedulerJobEditorSheet } from "./SchedulerJobEditorSheet";
import { SchedulerJobsDrawer } from "./SchedulerJobsDrawer";
import type { SchedulerInfo, SchedulerJob } from "./types";
import type { SchedulerEditorState } from "./useSchedulerJobs";

/**
 * The scheduler dock: jobs drawer plus the job editor sheet. Rendered by App
 * only while the scheduler UI is open and the HTTP API is linked.
 */
export function SchedulerDockCluster({
  clusterRef,
  editor,
  setEditor,
  info,
  jobs,
  listError,
  loading,
  filterDraft,
  setFilterDraft,
  onClose,
  onRunJob,
  onCancelJob,
  refreshJobs,
  availableModels,
  defaultModel,
  currentCwd,
}: {
  clusterRef: RefObject<HTMLDivElement | null>;
  editor: SchedulerEditorState;
  setEditor: Dispatch<SetStateAction<SchedulerEditorState>>;
  info: SchedulerInfo | null;
  jobs: SchedulerJob[];
  listError: string | null;
  loading: boolean;
  filterDraft: string;
  setFilterDraft: Dispatch<SetStateAction<string>>;
  onClose: () => void;
  onRunJob: (jobId: string) => Promise<void>;
  onCancelJob: (jobId: string) => Promise<void>;
  refreshJobs: (opts?: { silent?: boolean }) => Promise<void>;
  availableModels: string[];
  defaultModel: string;
  currentCwd: string;
}) {
  return (
    <div
      ref={clusterRef}
      className={[
        "scheduler-dock-cluster",
        editor ? "scheduler-dock-cluster-editor-active" : "",
      ]
        .filter(Boolean)
        .join(" ")}
    >
      <SchedulerJobsDrawer
        open
        selectedJobId={editor?.mode === "edit" ? editor.jobId : null}
        className="scheduler-dock-drawer"
        onClose={onClose}
        scheduler={info}
        jobs={jobs}
        listError={listError}
        loading={loading}
        onAddJob={() => {
          setSchedulerCreateHash();
        }}
        onOpenJob={(jid) => {
          setEditor({ mode: "edit", jobId: jid });
          setSchedulerJobHash(jid);
        }}
        onRunJob={(jid) => void onRunJob(jid)}
        onCancelJob={(jid) => void onCancelJob(jid)}
        searchDraft={filterDraft}
        onSearchDraftChange={setFilterDraft}
        onSearchClear={() => setFilterDraft("")}
      />

      <SchedulerJobEditorSheet
        open={!!editor}
        mode={editor?.mode === "create" ? "create" : "edit"}
        jobId={editor?.mode === "edit" ? editor.jobId : null}
        availableModels={availableModels}
        defaultModel={defaultModel}
        currentCwd={currentCwd}
        onClose={() => {
          setEditor(null);
          setSchedulerListHash();
        }}
        onSaved={(createdId) => {
          void refreshJobs({ silent: true });
          if (createdId) {
            setEditor({ mode: "edit", jobId: createdId });
          }
        }}
        onDeleted={() => {
          setEditor(null);
          void refreshJobs({ silent: true });
        }}
      />
    </div>
  );
}
