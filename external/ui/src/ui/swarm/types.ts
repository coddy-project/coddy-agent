/**
 * What a relay answers with. A swarm is a set of coddy nodes reachable through
 * one or more relays; a relay stores nothing itself, so everything here is a
 * live view rather than a record.
 */

export type SwarmInfo = {
  swarm: boolean;
  name: string;
  uuid: string;
  version: string;
  node_count: number;
  started_at: string;
  /** True while a just-restarted relay is still waiting for nodes to check in. */
  registry_warming: boolean;
};

export type SwarmNode = {
  name: string;
  kind: "agent" | "relay";
  transport: "direct" | "tunnel";
  url?: string;
  instance_uuid: string;
  version?: string;
  online: boolean;
  last_seen?: string;
  generation: number;
  labels?: Record<string, string>;
};

export type SwarmSession = {
  id: string;
  title?: string;
  updatedAt?: string;
  cwd?: string;
  /** The agent that owns this session: identity, stable across renames. */
  agent_uuid?: string;
  /** How to reach it from the relay we are attached to: a route, not identity. */
  node_path: string[];
  node_name: string;
  node_url?: string;
  node_kind?: string;
  turnActive?: boolean;
  permissionPending?: boolean;
};

export type SwarmSessionList = {
  sessions: SwarmSession[];
  warnings: string[];
  node_more?: Record<string, boolean>;
  hasMore?: boolean;
};

export type TopologyNode = {
  uuid: string;
  name: string;
  kind: "agent" | "relay";
  transport?: string;
  online: boolean;
  version?: string;
};

export type TopologyEdge = {
  from_uuid: string;
  to_uuid: string;
  name: string;
};

export type TopologyRoute = {
  path: string[];
  alternates?: string[][];
};

export type SwarmTopology = {
  root: TopologyNode;
  nodes: TopologyNode[];
  edges: TopologyEdge[];
  routes: Record<string, TopologyRoute>;
  warnings: string[];
};
