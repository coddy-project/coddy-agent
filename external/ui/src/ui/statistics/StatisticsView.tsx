import { useCallback, useEffect, useRef, useState } from "react";
import { ProviderLimits } from "./ProviderLimits";
import { StatisticsSheet } from "./StatisticsSheet";
import StatisticsSettings from "./StatisticsSettings";
import { snapshotPhone } from "../shellBreakpoint";
import { useT } from "../i18n/I18nProvider";
import { CoddyWorkIndicator } from "../messages/CoddyWorkIndicator";
import { ActivityCalendar } from "./ActivityCalendar";
import { Combobox } from "../settings/Combobox";
import {
  browserTimezone,
  exportStatistics,
  statisticsRelay,
  statisticsRequest,
  type Breakdown,
  type StatisticsData,
} from "./api";

export default function StatisticsView({
  onClose,
  onSettings,
  swarm,
  relayHome,
  cwd,
}: {
  onClose: () => void;
  onSettings: () => void;
  swarm: boolean;
  relayHome: boolean;
  cwd: string;
}) {
  const { t, locale } = useT();
  const [tab, setTab] = useState<"overview" | "limits">("overview");
  const [data, setData] = useState<StatisticsData | null>(null);
  const [error, setError] = useState("");
  const [timezone, setTimezone] = useState(browserTimezone);
  const [period, setPeriod] = useState("year");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [model, setModel] = useState("");
  const [provider, setProvider] = useState("");
  const [project, setProject] = useState("");
  const [source, setSource] = useState("");
  const [scope, setScope] = useState(relayHome ? "swarm" : "local");
  const [node, setNode] = useState("");
  const [mode, setMode] = useState<"days" | "weeks" | "cumulative">("days");
  const [working, setWorking] = useState(false);
  const [filtersOpen, setFiltersOpen] = useState(false);
  const [settingsOpen, setSettingsOpen] = useState(false);
  const [actionsOpen, setActionsOpen] = useState(false);
  const actionsRef = useRef<HTMLDivElement>(null);
  const openSettings = () => {
    actionsRef.current?.querySelector("button")?.focus();
    setActionsOpen(false);
    if (snapshotPhone()) setSettingsOpen(true);
    else onSettings();
  };
  useEffect(() => {
    if (!actionsOpen) return;
    const outside = (e: PointerEvent) => {
      if (!actionsRef.current?.contains(e.target as Node))
        setActionsOpen(false);
    };
    const key = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return;
      e.preventDefault();
      e.stopImmediatePropagation();
      setActionsOpen(false);
      actionsRef.current?.querySelector("button")?.focus();
    };
    document.addEventListener("pointerdown", outside);
    window.addEventListener("keydown", key, true);
    return () => {
      document.removeEventListener("pointerdown", outside);
      window.removeEventListener("keydown", key, true);
    };
  }, [actionsOpen]);
  const request = useRef(0);
  const aggregate = scope === "swarm";
  const read = useCallback(async () => {
    const id = ++request.current;
    try {
      const q = new URLSearchParams({ timezone, cwd });
      if (period === "custom") {
        if (from) q.set("from", from);
        if (to) q.set("to", to);
      } else if (period !== "all") {
        const today = new Intl.DateTimeFormat("en-CA", {
          timeZone: timezone,
          year: "numeric",
          month: "2-digit",
          day: "2-digit",
        }).format(new Date());
        const first = new Date(today + "T12:00:00Z");
        first.setUTCDate(
          first.getUTCDate() -
            (period === "7" ? 6 : period === "30" ? 29 : 364),
        );
        q.set("from", first.toISOString().slice(0, 10));
        q.set("to", today);
      }
      for (const [key, value] of Object.entries({
        model,
        provider,
        project,
        source,
        node,
      }))
        if (value) q.set(key, value);
      const next = await statisticsRequest<StatisticsData>(
        `${aggregate ? "/swarm/statistics" : "/coddy/statistics"}?${q}`,
        undefined,
        aggregate ? statisticsRelay() : undefined,
      );
      if (request.current !== id) return;
      if (!aggregate && next.report.timezone !== timezone)
        setTimezone(next.report.timezone);
      setData(next);
      setError("");
    } catch (e) {
      if (request.current === id) setError(String(e));
    }
  }, [
    timezone,
    cwd,
    period,
    from,
    to,
    model,
    provider,
    project,
    source,
    aggregate,
    node,
  ]);
  useEffect(() => {
    setData(null);
    void read();
    const timer = setInterval(() => {
      if (!document.hidden) void read();
    }, 5000);
    return () => {
      request.current++;
      clearInterval(timer);
    };
  }, [read]);
  const importHistory = async (action: "start" | "skip") => {
    setWorking(true);
    try {
      const targets = aggregate
        ? (data?.nodes ?? []).filter(
            (n) =>
              (!node || n.dataset === node) &&
              (action === "skip"
                ? n.import.state === "pending"
                : ["pending", "interrupted", "error"].includes(n.import.state)),
          )
        : [null];
      const outcomes = await Promise.allSettled(
        targets.map((n) =>
          statisticsRequest(
            `${n ? n.path.map((part) => "/swarm/nodes/" + encodeURIComponent(part)).join("") : ""}/coddy/statistics/import`,
            { method: "POST", body: JSON.stringify({ action }) },
            aggregate ? statisticsRelay() : undefined,
          ),
        ),
      );
      const failures = outcomes.filter(
        (v): v is PromiseRejectedResult => v.status === "rejected",
      );
      if (failures.length)
        throw new Error(failures.map((v) => String(v.reason)).join("; "));
      await read();
    } catch (e) {
      setError(String(e));
    } finally {
      setWorking(false);
    }
  };
  const nf = new Intl.NumberFormat(locale, {
    notation: "compact",
    maximumFractionDigits: 1,
  });
  const number = (v: number | null | undefined) =>
    v == null ? "—" : nf.format(v);
  const duration = (v: number | null | undefined) =>
    v == null
      ? "—"
      : v < 60000
        ? t("stats.durationSeconds", { seconds: Math.floor(v / 1000) })
        : v < 3600000
          ? t("stats.durationMinutes", { minutes: Math.floor(v / 60000) })
          : t("stats.duration", {
              hours: Math.floor(v / 3600000),
              minutes: Math.floor(v / 60000) % 60,
            });
  const totals = data?.report.totals;
  const all = data?.allTime;
  const nodeImports = (data?.nodes ?? [])
    .filter((n) => !node || n.dataset === node)
    .map((n) => n.import);
  const imp =
    aggregate && nodeImports.length
      ? {
          state: nodeImports.some((s) => s.state === "pending")
            ? "pending"
            : nodeImports.some((s) => s.state === "running")
              ? "running"
              : nodeImports.some((s) =>
                    ["error", "interrupted"].includes(s.state),
                  )
                ? "interrupted"
                : "complete",
          done: nodeImports.reduce((n, s) => n + s.done, 0),
          total: nodeImports.reduce((n, s) => n + s.total, 0),
          failed: nodeImports.reduce((n, s) => n + s.failed, 0),
        }
      : data?.settings?.import;
  const sourceName = (name: string) =>
    (
      ({
        local: t("stats.source.local"),
        webui: t("stats.source.webui"),
        api: "HTTP API",
        subagent: t("stats.source.subagent"),
        memory: t("stats.source.memory"),
        scheduler: t("stats.source.scheduler"),
        background: t("stats.source.background"),
        followup: t("stats.source.followup"),
        print: t("stats.source.print"),
        "gateway:telegram": "Telegram",
        "gateway:pachca": "Pachca",
      }) as Record<string, string>
    )[name] ?? name;
  const select = (
    label: string,
    value: string,
    change: (v: string) => void,
    rows: Breakdown[] = [],
  ) => (
    <label className="statistics-filter">
      <span>{t(label)}</span>
      <Combobox
        inlineOptions
        value={value}
        onChange={change}
        ariaLabel={t(label)}
        placeholder={t("stats.all")}
        options={[
          { value: "", label: t("stats.all") },
          ...rows.map((row) => ({
            value: row.name,
            label: label === "stats.source" ? sourceName(row.name) : row.name,
          })),
        ]}
      />
    </label>
  );
  const breakdown = (title: string, rows: Breakdown[] = [], counts = true) => (
    <section className="statistics-breakdown">
      <h3>{t(title)}</h3>
      {rows.length ? (
        <div className="statistics-table-scroll">
          <table>
            <thead>
              <tr>
                <th>{t("stats.name")}</th>
                <th>{t(counts ? "stats.count" : "stats.calls")}</th>
                <th>{t("stats.tokens")}</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((row) => (
                <tr key={row.name}>
                  <td title={row.name}>{row.name}</td>
                  <td>{number(row.count)}</td>
                  <td>{number(row.tokens)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <p className="statistics-muted">{t("stats.noData")}</p>
      )}
    </section>
  );
  return (
    <div
      className="docs-dock-cluster statistics-dock"
      data-testid="statistics-view"
    >
      <div className="docs-view statistics-view">
        <header className="sessions-head statistics-head">
          <h2>{t("stats.title")}</h2>
          <div className="statistics-actions">
            <div
              className="statistics-actions-anchor"
              ref={actionsRef}
              onBlur={(e) => {
                if (!e.currentTarget.contains(e.relatedTarget))
                  setActionsOpen(false);
              }}
            >
              <button
                className="sessions-close"
                aria-label={t("stats.actions")}
                aria-expanded={actionsOpen}
                onClick={() => setActionsOpen(!actionsOpen)}
              >
                <svg
                  width="16"
                  height="16"
                  viewBox="0 0 24 24"
                  fill="currentColor"
                  aria-hidden="true"
                >
                  <circle cx="5" cy="12" r="2" />
                  <circle cx="12" cy="12" r="2" />
                  <circle cx="19" cy="12" r="2" />
                </svg>
              </button>
              {actionsOpen && (
                <div className="statistics-actions-menu">
                  {data && (
                    <>
                      <button
                        className="settings-btn"
                        onClick={() => {
                          exportStatistics(data, "csv");
                          setActionsOpen(false);
                        }}
                      >
                        {t("stats.exportCsv")}
                      </button>
                      <button
                        className="settings-btn"
                        onClick={() => {
                          exportStatistics(data, "json");
                          setActionsOpen(false);
                        }}
                      >
                        {t("stats.exportJson")}
                      </button>
                    </>
                  )}
                  {!aggregate && (
                    <button className="settings-btn" onClick={openSettings}>
                      {t("stats.settings")}
                    </button>
                  )}
                </div>
              )}
            </div>
            <button
              className="sessions-close"
              aria-label={t("common.close")}
              onClick={onClose}
            >
              <svg
                width="16"
                height="16"
                viewBox="0 0 24 24"
                fill="none"
                stroke="currentColor"
                strokeWidth="2"
                aria-hidden="true"
              >
                <path d="m6 6 12 12M18 6 6 18" />
              </svg>
            </button>
          </div>
        </header>
        <div
          className="statistics-tabs"
          role="tablist"
          aria-label={t("stats.title")}
          onKeyDown={(e) => {
            if (!["ArrowLeft", "ArrowRight", "Home", "End"].includes(e.key))
              return;
            e.preventDefault();
            const next =
              e.key === "Home"
                ? "overview"
                : e.key === "End"
                  ? "limits"
                  : tab === "overview"
                    ? "limits"
                    : "overview";
            setTab(next);
            e.currentTarget
              .querySelector<HTMLButtonElement>(`[id="statistics-tab-${next}"]`)
              ?.focus();
          }}
        >
          {(["overview", "limits"] as const).map((value) => (
            <button
              key={value}
              id={`statistics-tab-${value}`}
              role="tab"
              aria-selected={tab === value}
              aria-controls={`statistics-panel-${value}`}
              tabIndex={tab === value ? 0 : -1}
              onClick={() => setTab(value)}
            >
              {t(`stats.${value}`)}
            </button>
          ))}
        </div>
        <div className="statistics-body">
          <div className="statistics-filters">
            {tab === "overview" && (
              <label className="statistics-filter">
                <span>{t("stats.period")}</span>
                <span className="appearance-language-select-wrap">
                  <select
                    className="appearance-language-select"
                    value={period}
                    onChange={(e) => setPeriod(e.target.value)}
                  >
                    {["7", "30", "year", "all", "custom"].map((v) => (
                      <option key={v} value={v}>
                        {t(`stats.period.${v}`)}
                      </option>
                    ))}
                  </select>
                </span>
              </label>
            )}
            {tab === "overview" && period === "custom" && (
              <>
                <label className="statistics-filter">
                  <span>{t("stats.from")}</span>
                  <input
                    className="settings-input"
                    type="date"
                    value={from}
                    onChange={(e) => setFrom(e.target.value)}
                  />
                </label>
                <label className="statistics-filter">
                  <span>{t("stats.to")}</span>
                  <input
                    className="settings-input"
                    type="date"
                    value={to}
                    onChange={(e) => setTo(e.target.value)}
                  />
                </label>
              </>
            )}
            {(tab === "overview" || swarm) && (
              <button
                className="settings-btn statistics-filter-trigger"
                aria-expanded={filtersOpen}
                onClick={() => setFiltersOpen(true)}
              >
                {t("stats.filters")}
                {(tab === "overview"
                  ? [project, provider, model, source, node]
                  : [node]
                ).filter(Boolean).length > 0 && (
                  <span className="statistics-filter-count">
                    {
                      (tab === "overview"
                        ? [project, provider, model, source, node]
                        : [node]
                      ).filter(Boolean).length
                    }
                  </span>
                )}
              </button>
            )}
          </div>
          <div className="statistics-filter-tags">
            {scope === "swarm" && (
              <button
                className="settings-btn"
                onClick={() => setFiltersOpen(true)}
              >
                {t("stats.swarm")}
              </button>
            )}
            {[
              ["stats.project", tab === "overview" ? project : ""],
              ["stats.provider", tab === "overview" ? provider : ""],
              ["stats.model", tab === "overview" ? model : ""],
              [
                "stats.source",
                tab === "overview" && source ? sourceName(source) : "",
              ],
              [
                "stats.node",
                node
                  ? data?.nodes
                      ?.find((n) => n.dataset === node)
                      ?.path.join(" / ") || node
                  : "",
              ],
            ]
              .filter(([, value]) => value)
              .map(([key, value]) => (
                <button
                  className="settings-btn"
                  key={key}
                  onClick={() => setFiltersOpen(true)}
                >
                  {t(key!)}: {value}
                </button>
              ))}
          </div>
          {filtersOpen && (
            <StatisticsSheet
              title={t("stats.filters")}
              onClose={() => setFiltersOpen(false)}
            >
              <div className="statistics-filters statistics-filter-fields">
                {swarm && (
                  <label className="statistics-filter">
                    <span>{t("stats.scope")}</span>
                    <span className="appearance-language-select-wrap">
                      <select
                        className="appearance-language-select"
                        value={scope}
                        onChange={(e) => {
                          setScope(e.target.value);
                          setNode("");
                          setModel("");
                          setProvider("");
                          setProject("");
                          setSource("");
                        }}
                      >
                        {!relayHome && (
                          <option value="local">{t("stats.thisServer")}</option>
                        )}
                        <option value="swarm">{t("stats.swarm")}</option>
                      </select>
                    </span>
                  </label>
                )}
                {aggregate && (
                  <label className="statistics-filter">
                    <span>{t("stats.node")}</span>
                    <span className="appearance-language-select-wrap">
                      <select
                        className="appearance-language-select"
                        value={node}
                        onChange={(e) => setNode(e.target.value)}
                      >
                        <option value="">{t("stats.all")}</option>
                        {data?.nodes?.map((n) => (
                          <option key={n.dataset} value={n.dataset}>
                            {n.path.join(" / ") || n.name}
                          </option>
                        ))}
                      </select>
                    </span>
                  </label>
                )}
                {tab === "overview" && (
                  <>
                    {select(
                      "stats.project",
                      project,
                      setProject,
                      all?.projects,
                    )}
                    {select(
                      "stats.provider",
                      provider,
                      setProvider,
                      all?.providers,
                    )}
                    {select("stats.model", model, setModel, all?.models)}
                    {select("stats.source", source, setSource, all?.sources)}
                  </>
                )}
              </div>
              <button
                className="settings-btn statistics-reset"
                onClick={() => {
                  setProject("");
                  setProvider("");
                  setModel("");
                  setSource("");
                  setNode("");
                }}
              >
                {t("stats.resetFilters")}
              </button>
            </StatisticsSheet>
          )}
          {settingsOpen && (
            <StatisticsSheet
              title={t("stats.settings")}
              onClose={() => {
                setSettingsOpen(false);
                void read();
              }}
            >
              <StatisticsSettings
                onImported={() => {
                  setSettingsOpen(false);
                  void read();
                }}
              />
            </StatisticsSheet>
          )}
          {error && (
            <div role="alert" className="statistics-error">
              {error}
              <button className="settings-btn" onClick={() => void read()}>
                {t("stats.retry")}
              </button>
            </div>
          )}
          {data?.warnings?.map((w) => (
            <p role="status" className="statistics-warning" key={w}>
              {w}
            </p>
          ))}
          {aggregate && (
            <p className="statistics-muted">{t("stats.swarmHint")}</p>
          )}
          {!data && !error && (
            <p className="statistics-progress" role="status">
              <CoddyWorkIndicator />
              {t("stats.loading")}
            </p>
          )}
          <div
            id="statistics-panel-limits"
            role="tabpanel"
            aria-labelledby="statistics-tab-limits"
            hidden={tab !== "limits"}
            tabIndex={0}
          >
            {tab === "limits" && (
              <ProviderLimits
                aggregate={aggregate}
                nodes={data?.nodes}
                node={node}
                timezone={timezone}
              />
            )}
          </div>
          <div
            className="statistics-overview"
            id="statistics-panel-overview"
            role="tabpanel"
            aria-labelledby="statistics-tab-overview"
            hidden={tab !== "overview"}
            tabIndex={0}
          >
            {imp?.state === "running" && (
              <div className="statistics-import" role="status">
                <div className="statistics-progress">
                  <CoddyWorkIndicator />
                  {t("stats.import.running")}
                </div>
                <progress
                  max={Math.max(1, imp.total)}
                  value={imp.total ? imp.done : undefined}
                />
                <span>
                  {t("stats.import.progress", {
                    done: imp.done,
                    total: imp.total,
                  })}
                </span>
              </div>
            )}
            {imp && ["interrupted", "error"].includes(imp.state) && (
              <p className="statistics-warning">
                {t("stats.import.interrupted")}
                <button
                  className="settings-btn"
                  onClick={() => void importHistory("start")}
                >
                  {t("stats.retry")}
                </button>
              </p>
            )}
            {imp && imp.failed > 0 && (
              <p className="statistics-warning">
                {t("stats.import.failed", { count: imp.failed })}
              </p>
            )}
            {data && totals && (
              <>
                <div
                  className="statistics-cards"
                  aria-label={t("stats.overview")}
                >
                  {[
                    ["stats.tokensPeriod", number(totals.totalTokens)],
                    ["stats.activeTime", duration(totals.activeMs)],
                    [
                      "stats.cost",
                      totals.costUsd == null
                        ? "—"
                        : new Intl.NumberFormat(locale, {
                            style: "currency",
                            currency: "USD",
                            maximumFractionDigits: 2,
                          }).format(totals.costUsd),
                    ],
                    ["stats.currentStreak", number(totals.currentStreak)],
                  ].map(([key, value]) => (
                    <div className="statistics-card" key={key}>
                      <span>{t(key!)}</span>
                      <strong>{value}</strong>
                    </div>
                  ))}
                </div>
                <section className="statistics-activity">
                  <div className="statistics-section-head">
                    <h3>{t("stats.activity")}</h3>
                    <div className="statistics-segments">
                      {(["days", "weeks", "cumulative"] as const).map((v) => (
                        <button
                          className="settings-btn"
                          key={v}
                          aria-pressed={mode === v}
                          onClick={() => setMode(v)}
                        >
                          {t(`stats.segment.${v}`)}
                        </button>
                      ))}
                    </div>
                  </div>
                  <ActivityCalendar
                    days={data.report.days ?? []}
                    timezone={data.report.timezone}
                    mode={mode}
                    {...(period === "custom" && to ? { end: to } : {})}
                  />
                  <p className="statistics-muted">{data.report.timezone}</p>
                </section>
                <div className="statistics-detail-groups">
                  <section className="statistics-detail-group">
                    <h3>{t("stats.tokensHeading")}</h3>
                    <dl>
                      {[
                        [
                          "stats.tokensAllTime",
                          number(all?.totals.totalTokens),
                        ],
                        ["stats.input", number(totals.inputTokens)],
                        ["stats.output", number(totals.outputTokens)],
                        ["stats.cached", number(totals.cachedTokens)],
                        ["stats.maxDaily", number(totals.maxDailyTokens)],
                      ].map(([key, value]) => (
                        <div key={key}>
                          <dt>{t(key!)}</dt>
                          <dd>{value}</dd>
                        </div>
                      ))}
                    </dl>
                  </section>
                  <section className="statistics-detail-group">
                    <h3>{t("stats.activityDetails")}</h3>
                    <dl>
                      {[
                        ["stats.longest", duration(totals.longestMs)],
                        ["stats.longestStreak", number(totals.longestStreak)],
                        ["stats.chats", number(totals.chats)],
                        ["stats.requests", number(totals.requests)],
                        [
                          "stats.calls",
                          number(
                            totals.importedTurns > 0 && totals.calls === 0
                              ? null
                              : totals.calls,
                          ),
                        ],
                      ].map(([key, value]) => (
                        <div key={key}>
                          <dt>{t(key!)}</dt>
                          <dd>{value}</dd>
                        </div>
                      ))}
                    </dl>
                  </section>
                  <section className="statistics-detail-group">
                    <h3>{t("stats.skills")}</h3>
                    <dl>
                      {[
                        [
                          "stats.uniqueSkills",
                          number(
                            totals.importedTurns > 0 &&
                              totals.uniqueSkills === 0
                              ? null
                              : totals.uniqueSkills,
                          ),
                        ],
                        [
                          "stats.skillCalls",
                          number(
                            totals.importedTurns > 0 && totals.skillCalls === 0
                              ? null
                              : totals.skillCalls,
                          ),
                        ],
                        ["stats.installedSkills", number(data.installedSkills)],
                      ].map(([key, value]) => (
                        <div key={key}>
                          <dt>{t(key!)}</dt>
                          <dd>{value}</dd>
                        </div>
                      ))}
                    </dl>
                  </section>
                </div>
                <p className="statistics-muted">{t("stats.unknownHint")}</p>
                {totals.importedTurns > 0 && (
                  <p className="statistics-muted">
                    {t("stats.import.partial")}
                  </p>
                )}
                {totals.unpricedCalls > 0 && (
                  <p className="statistics-muted">
                    {t("stats.unpriced", { count: totals.unpricedCalls })}
                    {!aggregate && (
                      <button className="settings-btn" onClick={openSettings}>
                        {t("stats.prices.configure")}
                      </button>
                    )}
                  </p>
                )}
                <div className="statistics-summary-row">
                  <span>{t("stats.popularReasoning")}</span>
                  <strong>
                    {data.report.reasoning?.[0]
                      ? `${data.report.reasoning[0].name} · ${Math.round(
                          (data.report.reasoning[0].count /
                            Math.max(
                              1,
                              data.report.reasoning.reduce(
                                (n, r) => n + r.count,
                                0,
                              ),
                            )) *
                            100,
                        )}%`
                      : "—"}
                  </strong>
                </div>
                <div className="statistics-breakdowns">
                  {breakdown("stats.models", data.report.models, false)}
                  {breakdown("stats.providers", data.report.providers, false)}
                  {breakdown("stats.projects", data.report.projects)}
                  {breakdown("stats.sources", data.report.sources)}
                  {breakdown("stats.reasoning", data.report.reasoning, false)}
                  {breakdown("stats.skills", data.report.skills)}
                </div>
              </>
            )}
          </div>
        </div>
        {imp?.state === "pending" && tab === "overview" && (
          <StatisticsSheet
            title={t("stats.import.title")}
            onClose={() => void importHistory("skip")}
            busy={working}
          >
            <p className="statistics-muted">{t("stats.import.message")}</p>
            {error && (
              <p role="alert" className="statistics-error">
                {error}
              </p>
            )}
            <div className="statistics-sheet-actions">
              <button
                className="confirm-dialog-btn confirm-dialog-btn--primary"
                disabled={working}
                onClick={() => void importHistory("start")}
              >
                {t("stats.import.start")}
              </button>
              <button
                className="settings-btn"
                disabled={working}
                onClick={() => void importHistory("skip")}
              >
                {t("stats.import.skip")}
              </button>
            </div>
          </StatisticsSheet>
        )}
      </div>
    </div>
  );
}
