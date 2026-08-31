import type { MCPServerRow, ProjectTrust } from "./mcpServerJson";

export type MCPList = {
  items: MCPServerRow[];
  /** Effective mcp.project_trust policy for the server's workspace. */
  projectTrust: ProjectTrust;
  /** Workspace the rows were merged for; approvals are recorded against it. */
  workspace: string;
};

export async function fetchServers(refresh = false): Promise<MCPList> {
  const res = await fetch(`/coddy/mcp${refresh ? "?refresh=1" : ""}`);
  if (!res.ok) return { items: [], projectTrust: "ask", workspace: "" };
  const data = (await res.json()) as {
    items?: MCPServerRow[];
    project_trust?: ProjectTrust;
    workspace?: string;
  };
  return {
    items: data.items ?? [],
    projectTrust: data.project_trust ?? "ask",
    workspace: data.workspace ?? "",
  };
}
