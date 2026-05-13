import { readErrorMessage, parseJson } from "../shared/apiUtils";
import type { ApiResult } from "../shared/apiUtils";

export interface AdminProvider {
  name: string;
  type: "openai" | "anthropic";
  api_base: string;
  api_key?: string;
}

export interface AdminModel {
  model: string;
  max_tokens: number;
  temperature: number;
  max_context_tokens: number;
}

export async function listProviders(): Promise<ApiResult<AdminProvider[]>> {
  const res = await fetch("/coddy/admin/providers");
  return parseJson<AdminProvider[]>(res);
}

export async function createProvider(
  p: AdminProvider,
): Promise<ApiResult<AdminProvider>> {
  const res = await fetch("/coddy/admin/providers", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(p),
  });
  return parseJson<AdminProvider>(res);
}

export async function updateProvider(
  name: string,
  p: AdminProvider,
): Promise<ApiResult<AdminProvider>> {
  const res = await fetch(
    `/coddy/admin/providers/${encodeURIComponent(name)}`,
    {
      method: "PUT",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(p),
    },
  );
  return parseJson<AdminProvider>(res);
}

export async function deleteProvider(name: string): Promise<ApiResult<null>> {
  const res = await fetch(
    `/coddy/admin/providers/${encodeURIComponent(name)}`,
    { method: "DELETE" },
  );
  if (res.ok && (res.status === 204 || res.status === 200)) {
    return { ok: true, data: null };
  }
  return {
    ok: false,
    status: res.status,
    message: await readErrorMessage(res),
  };
}

export async function listModels(): Promise<ApiResult<AdminModel[]>> {
  const res = await fetch("/coddy/admin/models");
  return parseJson<AdminModel[]>(res);
}

export async function createModel(
  m: AdminModel,
): Promise<ApiResult<AdminModel>> {
  const res = await fetch("/coddy/admin/models", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(m),
  });
  return parseJson<AdminModel>(res);
}

export async function updateModel(
  id: string,
  m: AdminModel,
): Promise<ApiResult<AdminModel>> {
  const res = await fetch(`/coddy/admin/models/${encodeURIComponent(id)}`, {
    method: "PUT",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(m),
  });
  return parseJson<AdminModel>(res);
}

export async function deleteModel(id: string): Promise<ApiResult<null>> {
  const res = await fetch(`/coddy/admin/models/${encodeURIComponent(id)}`, {
    method: "DELETE",
  });
  if (res.ok && (res.status === 204 || res.status === 200)) {
    return { ok: true, data: null };
  }
  return {
    ok: false,
    status: res.status,
    message: await readErrorMessage(res),
  };
}
