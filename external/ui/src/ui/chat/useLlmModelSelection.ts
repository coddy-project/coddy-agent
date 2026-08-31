import {
  useCallback,
  useEffect,
  useMemo,
  useState,
  type Dispatch,
  type RefObject,
  type SetStateAction,
} from "react";
import { fetchJSON } from "../coddyApi";
import { readLlmModelCookie, writeLlmModelCookie } from "./llmModelCookie";
import {
  pickDefaultLlmModelForNewChat,
  pickLlmModelForOpenSession,
} from "./llmModelSelection";
import { readReasoningCookie, writeReasoningCookie } from "./reasoningCookie";
import { pickReasoningLevel } from "./reasoningSelection";

export type ModelInfo = {
  id: string;
  ownedBy?: string;
  maxContextTokens?: number | undefined;
  multimodal?: boolean;
  reasoningLevels?: string[];
  reasoningDefault?: string;
};

type OpenSessionSelection = {
  sid: string;
  model: string;
  reasoning: string;
} | null;

/**
 * LLM backend selection: the /v1/models list, the current model/reasoning
 * picks with their cookies and per-session PATCH persistence, and the restore
 * of an opened session's saved selection once the backends list is known.
 */
export function useLlmModelSelection({
  sessionId,
  headers,
  viewedSessionIdRef,
}: {
  sessionId: string;
  headers: Record<string, string>;
  viewedSessionIdRef: RefObject<string>;
}): {
  llmModelIds: string[];
  llmModel: string;
  llmReasoning: string;
  maxContextTokens: number;
  llmModelMultimodal: boolean;
  llmReasoningLevels: string[];
  onLlmModelChange: (id: string) => void;
  onLlmReasoningChange: (level: string) => void;
  /** Stash an opened session's saved selection; applied once /v1/models lands. */
  setOpenSessionSelection: Dispatch<SetStateAction<OpenSessionSelection>>;
  /** Drop the stashed selection and re-pick the new-chat default model. */
  resetForNewChat: () => void;
  /** Re-fetch /v1/models (e.g. after a config save). */
  bumpModelsEpoch: () => void;
} {
  const [modelInfos, setModelInfos] = useState<ModelInfo[]>([]);
  const [modelsEpoch, setModelsEpoch] = useState(0);
  const [llmModelIds, setLlmModelIds] = useState<string[]>([]);
  const [defaultAgentYamlModel, setDefaultAgentYamlModel] = useState("");
  const [llmModel, setLlmModel] = useState("");
  const [llmReasoning, setLlmReasoning] = useState("");
  /**
   * Raw model/reasoning stored on the opened session. Held until the backends
   * list (`llmModelIds`) is available so the restore survives whichever of
   * `/v1/models` and `/coddy/sessions/.../messages` resolves first on reload.
   */
  const [openSessionSelection, setOpenSessionSelection] =
    useState<OpenSessionSelection>(null);

  useEffect(() => {
    void (async () => {
      const res = await fetchJSON<{
        default_agent_model?: string;
        data?: Array<{
          id?: string;
          owned_by?: string;
          max_context_tokens?: number;
          multimodal?: boolean;
          reasoning_levels?: string[];
          reasoning_default?: string;
        }>;
      }>("/v1/models");
      if (!res.ok || !res.data?.data) {
        return;
      }
      const raw = res.data.data
        .map((d) => ({
          id: (d.id || "").trim(),
          ownedBy: (d.owned_by || "").trim(),
          ...(d.max_context_tokens !== undefined
            ? { maxContextTokens: d.max_context_tokens }
            : {}),
          multimodal: !!d.multimodal,
          reasoningLevels: Array.isArray(d.reasoning_levels)
            ? d.reasoning_levels.map((s) => `${s}`.trim()).filter(Boolean)
            : [],
          reasoningDefault: (d.reasoning_default || "").trim(),
        }))
        .filter((d) => d.id);
      const rows: ModelInfo[] = raw.map((d) => {
        const m: ModelInfo = {
          id: d.id,
          ownedBy: d.ownedBy,
          multimodal: d.multimodal,
          reasoningLevels: d.reasoningLevels,
          reasoningDefault: d.reasoningDefault,
        };
        if (d.maxContextTokens !== undefined) {
          m.maxContextTokens = d.maxContextTokens;
        }
        return m;
      });
      setModelInfos(rows);
      const backends = raw
        .filter((r) => r.ownedBy !== "coddy")
        .map((r) => r.id);
      setLlmModelIds(backends);
      const defaultYaml = (res.data.default_agent_model || "").trim();
      setDefaultAgentYamlModel(defaultYaml);
      if (!viewedSessionIdRef.current.trim()) {
        setLlmModel(
          pickDefaultLlmModelForNewChat({
            backends,
            cookie: readLlmModelCookie(),
            defaultAgentModel: defaultYaml,
          }),
        );
      }
    })();
    // modelsEpoch bumps after config save so the multimodal flag refreshes without a page reload.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [modelsEpoch]);

  // Apply the opened session's saved model/reasoning once the backends list is
  // known. Runs whenever either input lands, so the restore is independent of
  // whether /v1/models or the session messages resolve first after a reload.
  useEffect(() => {
    if (!openSessionSelection || llmModelIds.length === 0) {
      return;
    }
    if (openSessionSelection.sid !== viewedSessionIdRef.current.trim()) {
      return;
    }
    setLlmModel(
      pickLlmModelForOpenSession({
        backends: llmModelIds,
        sessionModel: openSessionSelection.model,
        cookie: readLlmModelCookie(),
        defaultAgentModel: defaultAgentYamlModel,
      }),
    );
    setLlmReasoning(openSessionSelection.reasoning);
  }, [openSessionSelection, llmModelIds, defaultAgentYamlModel]);

  const maxContextTokens = useMemo(() => {
    const row = modelInfos.find((m) => m.id === llmModel);
    return row?.maxContextTokens || 128000;
  }, [modelInfos, llmModel]);

  const llmModelMultimodal = useMemo(() => {
    const row = modelInfos.find((m) => m.id === llmModel);
    return row?.multimodal ?? false;
  }, [modelInfos, llmModel]);

  const llmReasoningLevels = useMemo(() => {
    const row = modelInfos.find((m) => m.id === llmModel);
    return row?.reasoningLevels ?? [];
  }, [modelInfos, llmModel]);

  // Keep the selected reasoning level valid for the current model: keep the user's
  // pick when the new model still offers it, else fall back (cookie -> model default).
  useEffect(() => {
    const row = modelInfos.find((m) => m.id === llmModel);
    const levels = row?.reasoningLevels ?? [];
    setLlmReasoning((prev) =>
      pickReasoningLevel({
        levels,
        cookie: readReasoningCookie(),
        sessionLevel: prev,
        modelDefault: row?.reasoningDefault ?? null,
      }),
    );
  }, [llmModel, modelInfos]);

  const onLlmReasoningChange = useCallback(
    (level: string) => {
      const lv = level.trim();
      if (!lv) {
        return;
      }
      setLlmReasoning(lv);
      writeReasoningCookie(lv);
      const sid = sessionId.trim();
      if (!sid) {
        return;
      }
      void fetch(`/coddy/sessions/${encodeURIComponent(sid)}`, {
        method: "PATCH",
        headers: { ...headers, "Content-Type": "application/json" },
        body: JSON.stringify({ selectedReasoning: lv }),
      });
    },
    [sessionId, headers],
  );

  const onLlmModelChange = useCallback(
    (id: string) => {
      const mid = id.trim();
      if (!mid) {
        return;
      }
      setLlmModel(mid);
      writeLlmModelCookie(mid);
      const sid = sessionId.trim();
      if (!sid || !llmModelIds.includes(mid)) {
        return;
      }
      void fetch(`/coddy/sessions/${encodeURIComponent(sid)}`, {
        method: "PATCH",
        headers: { ...headers, "Content-Type": "application/json" },
        body: JSON.stringify({ selectedModelId: mid }),
      });
    },
    [sessionId, llmModelIds, headers],
  );

  const resetForNewChat = useCallback(() => {
    // Drop any stashed session selection so its restore effect cannot reapply
    // the old session's model over the new chat default.
    setOpenSessionSelection(null);
    if (llmModelIds.length > 0) {
      setLlmModel(
        pickDefaultLlmModelForNewChat({
          backends: llmModelIds,
          cookie: readLlmModelCookie(),
          defaultAgentModel: defaultAgentYamlModel,
        }),
      );
    }
  }, [llmModelIds, defaultAgentYamlModel]);

  const bumpModelsEpoch = useCallback(() => {
    setModelsEpoch((e) => e + 1);
  }, []);

  return {
    llmModelIds,
    llmModel,
    llmReasoning,
    maxContextTokens,
    llmModelMultimodal,
    llmReasoningLevels,
    onLlmModelChange,
    onLlmReasoningChange,
    setOpenSessionSelection,
    resetForNewChat,
    bumpModelsEpoch,
  };
}
