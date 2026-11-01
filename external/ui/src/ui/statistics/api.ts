import { getEnv, localFetch } from "../env/remoteEnv";

export type Counters = {
  inputTokens: number | null;
  outputTokens: number | null;
  cachedTokens: number | null;
  totalTokens: number | null;
};
export type StatisticsDay = Counters & {
  date: string;
  requests: number;
  manual: boolean;
};
export type Breakdown = { name: string; count: number; tokens: number | null };
export type StatisticsReport = {
  dataset: string;
  timezone: string;
  totals: Counters & {
    costUsd: number | null;
    activeMs: number | null;
    longestMs: number | null;
    maxDailyTokens: number | null;
    requests: number;
    calls: number;
    chats: number;
    skillCalls: number;
    uniqueSkills: number;
    currentStreak: number;
    longestStreak: number;
    unpricedCalls: number;
    unknownUsage: number;
    importedTurns: number;
  };
  days: StatisticsDay[];
  models: Breakdown[];
  providers: Breakdown[];
  projects: Breakdown[];
  sources: Breakdown[];
  reasoning: Breakdown[];
  skills: Breakdown[];
};
export type ImportStatus = {
  state: string;
  done: number;
  total: number;
  failed: number;
  error?: string;
};
export type StatisticsSettings = {
  timezone: string;
  prices: Record<string, { input: number; output: number; cached: number }>;
  import: ImportStatus;
};
export type StatisticsData = {
  report: StatisticsReport;
  allTime: StatisticsReport;
  settings?: StatisticsSettings;
  installedSkills?: number;
  warnings?: string[];
  nodes?: {
    name: string;
    dataset: string;
    path: string[];
    import: ImportStatus;
  }[];
};

export function browserTimezone(): string {
  return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
}

export type StatisticsTarget = { baseUrl: string; token: string };

export async function statisticsRequest<T>(
  path: string,
  init?: RequestInit,
  target?: StatisticsTarget,
): Promise<T> {
  const headers = new Headers(init?.headers);
  headers.set("Accept", "application/json");
  if (init?.body) headers.set("Content-Type", "application/json");
  if (target?.token) headers.set("Authorization", `Bearer ${target.token}`);
  const res = await (target
    ? localFetch(target.baseUrl.replace(/\/+$/, "") + path, {
        ...init,
        headers,
      })
    : fetch(path, { ...init, headers }));
  if (!res.ok)
    throw new Error(`${res.status}: ${(await res.text()).slice(0, 300)}`);
  return res.json() as Promise<T>;
}

export function statisticsRelay(): StatisticsTarget | undefined {
  const env = getEnv();
  return env.mode === "remote" && env.swarmRelay
    ? { baseUrl: env.swarmRelay, token: env.token }
    : undefined;
}

export function exportStatistics(data: StatisticsData, format: "csv" | "json") {
  const cell = (v: unknown) => `"${String(v ?? "").replaceAll('"', '""')}"`;
  const content =
    format === "json"
      ? JSON.stringify(
          {
            report: data.report,
            allTime: data.allTime,
            warnings: data.warnings ?? [],
          },
          null,
          2,
        )
      : [
          [
            "date",
            "timezone",
            "input_tokens",
            "output_tokens",
            "cached_tokens",
            "total_tokens",
            "requests",
          ],
          ...data.report.days.map((d) => [
            d.date,
            data.report.timezone,
            d.inputTokens,
            d.outputTokens,
            d.cachedTokens,
            d.totalTokens,
            d.requests,
          ]),
        ]
          .map((row) => row.map(cell).join(","))
          .join("\r\n");
  const url = URL.createObjectURL(
    new Blob([content], {
      type: format === "csv" ? "text/csv;charset=utf-8" : "application/json",
    }),
  );
  const a = document.createElement("a");
  a.href = url;
  a.download = `coddy-statistics.${format}`;
  a.click();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}
