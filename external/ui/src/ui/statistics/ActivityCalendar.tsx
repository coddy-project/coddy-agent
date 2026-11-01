import { useMemo } from "react";
import { useT } from "../i18n/I18nProvider";
import type { StatisticsDay } from "./api";

export function ActivityCalendar({
  days,
  timezone,
  mode,
  end,
}: {
  days: StatisticsDay[];
  timezone: string;
  mode: "days" | "weeks" | "cumulative";
  end?: string;
}) {
  const { t, locale } = useT();
  const cells = useMemo(() => {
    const date =
      end ||
      new Intl.DateTimeFormat("en-CA", {
        timeZone: timezone,
        year: "numeric",
        month: "2-digit",
        day: "2-digit",
      }).format(new Date());
    const last = new Date(date + "T12:00:00Z");
    const first = new Date(last);
    first.setUTCDate(first.getUTCDate() - 364);
    first.setUTCDate(first.getUTCDate() - ((first.getUTCDay() + 6) % 7));
    const lookup = new Map(days.map((d) => [d.date, d]));
    const result: {
      date: string;
      tokens: number | null;
      requests: number;
      month: string;
    }[] = [];
    for (
      const current = new Date(first);
      current <= last;
      current.setUTCDate(current.getUTCDate() + 1)
    ) {
      const key = current.toISOString().slice(0, 10);
      const d = lookup.get(key);
      result.push({
        date: key,
        tokens: d?.totalTokens ?? null,
        requests: d?.requests ?? 0,
        month: new Intl.DateTimeFormat(locale, {
          month: "short",
          timeZone: "UTC",
        }).format(current),
      });
    }
    return result;
  }, [days, timezone, end, locale]);
  const max = Math.max(1, ...cells.map((c) => c.tokens ?? 0));
  const weeks = Array.from({ length: Math.ceil(cells.length / 7) }, (_, i) =>
    cells.slice(i * 7, i * 7 + 7),
  );
  let cumulative = days
    .filter((d) => d.date < (cells[0]?.date ?? ""))
    .reduce((n, d) => n + (d.totalTokens ?? 0), 0);
  const bars = weeks.map((week) => {
    const known = week.filter((c) => c.tokens !== null);
    const sum = known.length
      ? known.reduce((n, c) => n + (c.tokens ?? 0), 0)
      : null;
    cumulative += sum ?? 0;
    return {
      date: week[0]!.date,
      value: mode === "cumulative" ? cumulative : sum,
    };
  });
  const barMax = Math.max(1, ...bars.map((b) => b.value ?? 0));
  const nf = new Intl.NumberFormat(locale);
  return (
    <div className="statistics-calendar" data-testid="statistics-calendar">
      {mode === "days" ? (
        <div
          className="statistics-calendar-grid"
          role="img"
          aria-label={t("stats.activity")}
        >
          {weeks.map((week, index) => (
            <div className="statistics-week" key={week[0]!.date}>
              <span className="statistics-month">
                {index === 0 || week[0]!.month !== weeks[index - 1]![0]!.month
                  ? week[0]!.month
                  : ""}
              </span>
              {week.map((c) => (
                <span
                  tabIndex={0}
                  key={c.date}
                  className={`statistics-cell level-${c.tokens ? Math.min(4, Math.ceil((c.tokens / max) * 4)) : c.requests ? "unknown" : 0}`}
                  title={`${c.date} · ${c.tokens === null ? t("stats.unknown") : nf.format(c.tokens) + " " + t("stats.tokens")} · ${t("stats.requests")}: ${nf.format(c.requests)}`}
                />
              ))}
            </div>
          ))}
        </div>
      ) : (
        <div
          className="statistics-bars"
          role="img"
          aria-label={t(mode === "weeks" ? "stats.weeks" : "stats.cumulative")}
        >
          {bars.map((b) => (
            <div
              key={b.date}
              className="statistics-bar-slot"
              title={`${b.date} · ${b.value === null ? t("stats.unknown") : nf.format(b.value)}`}
            >
              <span
                style={{
                  height: `${Math.max(b.value ? 2 : 0, ((b.value ?? 0) / barMax) * 100)}%`,
                }}
              />
            </div>
          ))}
        </div>
      )}
      <div className="statistics-calendar-caption">
        <span>{t("stats.calendarYear")}</span>
        <span>
          {t("stats.less")} <i className="statistics-cell level-0" />
          <i className="statistics-cell level-1" />
          <i className="statistics-cell level-2" />
          <i className="statistics-cell level-3" />
          <i className="statistics-cell level-4" /> {t("stats.more")}
        </span>
      </div>
    </div>
  );
}
