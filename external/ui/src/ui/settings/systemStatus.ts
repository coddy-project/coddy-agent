import { getLocale, translate as t, translatePlural as tp } from "../i18n/i18n";
import type {
  SystemAttention,
  SystemSeries,
  SystemStatusDocument,
  SystemStatusState,
} from "./systemStatusApi";

/** The blocks of the page a summary item can take the focus to. */
export type BlockId =
  | "installation"
  | "subsystems"
  | "disk"
  | "activity"
  | "providers";

export type SummaryItem = { text: string; block: BlockId };
/** A sentence of the summary; `quiet` for the note a partial reading adds. */
export type SummaryLine = {
  text: string;
  items: SummaryItem[];
  quiet?: boolean;
};
/** What the summary region says: its state and the sentences it is made of. */
export type Summary = { state: string; lines: SummaryLine[] };

const KIND_KEYS: Record<string, string> = {
  httpserver: "system.kind.httpserver",
  gateway: "system.kind.gateway",
  "gateway.pachca": "system.kind.gateway.pachca",
  swarm: "settings.section.swarm.label",
  scheduler: "settings.section.scheduler.label",
  metrics: "system.kind.metrics",
};

/** A subsystem as a person names it; a kind a newer server adds is shown as sent. */
export function kindName(kind: string): string {
  const key = KIND_KEYS[kind];
  return key ? t(key) : kind;
}

/** A word of the dictionary under a prefix, or the server's own word when there is none. */
export function word(prefix: string, name: string): string {
  const key = `${prefix}.${name}`;
  const text = t(key);
  return text === key ? name : text;
}

const num = (options: Intl.NumberFormatOptions = {}) =>
  new Intl.NumberFormat(getLocale(), options);

export const count = (n: number): string => num().format(n);

/** A rate or a mean: one decimal below ten, whole numbers above. */
export const rate = (n: number): string =>
  num({ maximumFractionDigits: n < 10 ? 1 : 0 }).format(n);

/** A count per minute: one decimal below ten and once abbreviated (2.1K). */
export const compact = (n: number): string =>
  num({
    notation: "compact",
    maximumFractionDigits: n >= 10 && n < 1000 ? 0 : 1,
  }).format(n);

const inUnit = (n: number, unit: string): string =>
  num({
    style: "unit",
    unit,
    unitDisplay: "short",
    maximumFractionDigits: n < 10 ? 1 : 0,
  }).format(n);

/** A duration: milliseconds under a second, so a fast call never reads 0 seconds. */
export const seconds = (n: number): string =>
  n < 1 ? inUnit(n * 1000, "millisecond") : inUnit(n, "second");

export const percent = (ratio: number): string =>
  num({ style: "percent", maximumFractionDigits: 1 }).format(ratio);

const BYTE_UNITS = ["byte", "kilobyte", "megabyte", "gigabyte", "terabyte"];

export function bytes(n: number): string {
  let unit = 0;
  let value = n;
  while (value >= 1000 && unit < BYTE_UNITS.length - 1) {
    value /= 1000;
    unit++;
  }
  return num({
    style: "unit",
    unit: BYTE_UNITS[unit]!,
    unitDisplay: "short",
    maximumFractionDigits: value < 10 ? 1 : 0,
  }).format(value);
}

/** A time span as its two largest units: 2d 4h, 1h, 5m 40s. */
export function span(total: number): string {
  const s = Math.max(0, Math.floor(total));
  const parts: [number, string][] = [
    [Math.floor(s / 86400), "day"],
    [Math.floor((s % 86400) / 3600), "hour"],
    [Math.floor((s % 3600) / 60), "minute"],
    [s % 60, "second"],
  ];
  // Nothing above zero is a span of 0 seconds, not of 0 days.
  const found = parts.findIndex(([n]) => n > 0);
  const first = found === -1 ? parts.length - 1 : found;
  return parts
    .slice(first, first + 2)
    .filter(([n], i) => i === 0 || n > 0)
    .map(([n, unit]) =>
      num({ style: "unit", unit, unitDisplay: "narrow" }).format(n),
    )
    .join(" ");
}

/** How long ago, in the reader's language ("12 min. ago", "now"). */
export function ago(ms: number): string {
  const rtf = new Intl.RelativeTimeFormat(getLocale(), {
    numeric: "auto",
    style: "short",
  });
  const s = Math.max(0, Math.round(ms / 1000));
  if (s < 60) return rtf.format(-s, "second");
  const m = Math.floor(s / 60);
  if (m < 60) return rtf.format(-m, "minute");
  const h = Math.floor(m / 60);
  return h < 24
    ? rtf.format(-h, "hour")
    : rtf.format(-Math.floor(h / 24), "day");
}

/** An RFC 3339 time as how long ago it was, "" when it does not parse. */
export function agoOf(at: string | undefined): string {
  const time = Date.parse(at ?? "");
  return Number.isNaN(time) ? "" : ago(Date.now() - time);
}

function attentionItem(
  a: SystemAttention,
  doc: SystemStatusDocument,
): SummaryItem {
  switch (a.code) {
    case "disk_low":
      return {
        text: t(
          doc.disk?.availableBytes === 0
            ? "system.item.diskFull"
            : "system.item.diskLow",
        ),
        block: "disk",
      };
    case "restart_required":
      return { text: t("system.item.restart"), block: "subsystems" };
    case "subsystem_down":
      return {
        text: t("system.item.down", { name: kindName(a.subsystem ?? "") }),
        block: "subsystems",
      };
    case "provider_failing":
      return {
        text: t("system.item.provider", { name: a.provider ?? "" }),
        block: "providers",
      };
    case "swarm_join_down":
      return { text: t("system.item.relay"), block: "subsystems" };
    default:
      return { text: a.code, block: "installation" };
  }
}

/**
 * The summary: a document says what needs attention and what is missing, the
 * absence of one says why. The count is the length of the document's own
 * `attention`: the browser has no rule of its own. It never says everything is
 * OK while a block is missing or the values are old.
 */
export function summarize(state: SystemStatusState, version: string): Summary {
  const { doc, issue, stale } = state;
  if (doc !== null) {
    const items = (doc.attention ?? []).map((a) => attentionItem(a, doc));
    const missing = (doc.unavailable ?? []).map(
      (name): SummaryItem => ({
        text: name === "disk" ? t("system.disk") : name,
        block: name === "disk" ? "disk" : "installation",
      }),
    );
    const lines: SummaryLine[] = [];
    if (items.length > 0) {
      lines.push({
        text: `${tp("system.attention.count", items.length)}:`,
        items,
      });
    }
    if (missing.length > 0) {
      lines.push({ text: t("system.partial"), items: missing, quiet: true });
    }
    if (lines.length === 0) {
      lines.push({ text: t("system.clear"), items: [] });
    }
    return {
      state:
        items.length > 0
          ? "problems"
          : missing.length > 0
            ? "partial"
            : "clear",
      lines,
    };
  }
  if (issue === null) {
    return { state: "pending", lines: [] };
  }
  const say = (text: string): SummaryLine => ({ text, items: [] });
  const lines: SummaryLine[] = [];
  if (issue.kind === "offline") {
    const seen = agoOf(issue.lastSeen);
    lines.push(
      say(
        [
          t("system.offline", { node: issue.node }),
          seen && t("system.lastSeen", { ago: seen }),
        ]
          .filter(Boolean)
          .join(" "),
      ),
    );
  }
  if (stale !== null) {
    lines.push({ ...say(t("system.stale")), quiet: true });
    return { state: "stale", lines };
  }
  if (lines.length === 0) {
    lines.push(
      say(
        issue.kind === "notFound"
          ? t("system.notFound", { version: version || t("system.unknown") })
          : issue.kind === "unauthorized"
            ? t("system.refused")
            : t("system.failed"),
      ),
    );
  }
  return {
    state:
      issue.kind === "notFound"
        ? "notFound"
        : issue.kind === "unauthorized"
          ? "refused"
          : issue.kind,
    lines,
  };
}

/** The unfinished tasks as "commands: 2 running · 1 queued; subsystems: ...". */
export function tasksText(tasks: SystemStatusDocument["tasks"]): string {
  const order = ["command", "agent", "server"];
  const kinds = Object.keys(tasks ?? {}).sort(
    (a, b) =>
      (order.indexOf(a) + 1 || 99) - (order.indexOf(b) + 1 || 99) ||
      a.localeCompare(b),
  );
  return kinds
    .map((kind) => {
      const by = tasks![kind]!;
      const states = Object.keys(by).sort(
        (a, b) => Number(b === "running") - Number(a === "running"),
      );
      return `${word("system.task", kind)}: ${states
        .map((s) => {
          const key = `system.task.${s}`;
          const n = by[s] ?? 0;
          // A state that agrees with its number is a plural family; one that
          // reads the same for any number is a plain key.
          const plural = tp(key, n);
          const text = plural !== key ? plural : t(key, { count: n });
          return text === key ? `${n} ${s}` : text;
        })
        .join(" · ")}`;
    })
    .join("; ");
}

/**
 * One sentence for each relay that has not registered the node, with how long
 * it has been so (`offlineSince` to the document's own clock, in the format of
 * "Running for"); a time that is missing or does not parse leaves it out.
 */
export function relayLines(doc: SystemStatusDocument): string[] {
  const now = Date.parse(doc.generatedAt);
  return (doc.attention ?? []).flatMap((a) => {
    if (a.code !== "swarm_join_down") {
      return [];
    }
    const down = t("system.relay.down", { relay: a.relay ?? "" });
    const since = Date.parse(a.offlineSince ?? "");
    return [
      now > since
        ? `${down} ${t("system.relay.for", { duration: span((now - since) / 1000) })}`
        : down,
    ];
  });
}

export type ChartDef = {
  id: string;
  title: string;
  value: string;
  max: number;
  lines: { values: (number | null)[]; tone: string; step?: boolean }[];
  legend: { text: string; tone: string }[];
};

const last = (values: (number | null)[]): number | null =>
  [...values].reverse().find((v) => v !== null) ?? null;

/** The charts of the last hour: what each draws and the value it names. */
export function chartsOf(s: SystemSeries): ChartDef[] {
  const dash = "—";
  const val = (v: number | null, f: (n: number) => string) =>
    v === null ? dash : f(v);
  const chart = (
    id: string,
    value: string,
    lines: ChartDef["lines"],
    legend: [string, string][],
  ): ChartDef => ({
    id,
    title: t(`system.chart.${id}`),
    value,
    max: Math.max(
      0,
      ...lines.flatMap((l) => l.values.filter((v) => v !== null)),
    ),
    lines,
    legend: legend.map(([text, tone]) => ({ text: t(text), tone })),
  });
  return [
    chart(
      "attempts",
      `${t("system.w.ok")} ${val(last(s.attemptsOk), rate)} · ${t("system.w.errors")} ${val(last(s.providerErrors), rate)}`,
      [
        { values: s.attemptsOk, tone: "ok" },
        { values: s.providerErrors, tone: "error" },
      ],
      [
        ["system.w.ok", "ok"],
        ["system.w.errors", "error"],
      ],
    ),
    chart(
      "time",
      val(last(s.attemptSecondsMean), seconds),
      [{ values: s.attemptSecondsMean, tone: "accent" }],
      [["system.col.time", "accent"]],
    ),
    chart(
      "tokens",
      `${t("system.w.in")} ${val(last(s.tokensIn), compact)} · ${t("system.w.out")} ${val(last(s.tokensOut), compact)}`,
      [
        { values: s.tokensIn, tone: "accent" },
        { values: s.tokensOut, tone: "ok" },
      ],
      [
        ["system.w.in", "accent"],
        ["system.w.out", "ok"],
      ],
    ),
    chart(
      "load",
      `${t("system.w.turns")} ${val(last(s.turnsRunning), count)} · ${t("system.w.tasks")} ${val(last(s.tasksUnfinished), count)}`,
      [
        { values: s.turnsRunning, tone: "accent", step: true },
        { values: s.tasksUnfinished, tone: "muted", step: true },
      ],
      [
        ["system.w.turns", "accent"],
        ["system.w.tasks", "muted"],
      ],
    ),
  ];
}
