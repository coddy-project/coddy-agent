export type InstalledSkill = {
  name: string;
  description: string;
  file_path: string;
  enabled: boolean;
  version?: string;
  source?: string;
  readonly?: boolean;
};

export type SkillUpdate = {
  name: string;
  source: string;
  version: string;
  latest: string;
  update_available: boolean;
};

export type AvailablePlugin = {
  name: string;
  description: string;
  version?: string;
  source: string;
  installed: boolean;
};

export async function fetchInstalled(): Promise<InstalledSkill[]> {
  const res = await fetch("/coddy/skills");
  if (!res.ok) return [];
  const data = (await res.json()) as { items?: InstalledSkill[] };
  return data.items ?? [];
}

export async function fetchUpdates(): Promise<SkillUpdate[]> {
  const res = await fetch("/coddy/skills/updates");
  if (!res.ok) return [];
  const data = (await res.json()) as { items?: SkillUpdate[] };
  return data.items ?? [];
}

export async function fetchAvailable(): Promise<AvailablePlugin[]> {
  const res = await fetch("/coddy/skills/available");
  if (!res.ok) return [];
  const data = (await res.json()) as { items?: AvailablePlugin[] };
  return data.items ?? [];
}
