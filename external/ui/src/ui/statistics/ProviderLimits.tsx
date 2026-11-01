import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useT } from "../i18n/I18nProvider";
import {
  usagePercent,
  usageWindowLabelKey,
  type ProviderUsage,
} from "../chat/providerUsage";
import { CoddyWorkIndicator } from "../messages/CoddyWorkIndicator";
import { statisticsRelay, statisticsRequest, type StatisticsData } from "./api";

type Target = { id: string; name: string; path: string };
type Result = Target & { providers: ProviderUsage[]; error?: string };

export function ProviderLimits({
  aggregate,
  nodes,
  node,
  timezone,
}: {
  aggregate: boolean;
  nodes: StatisticsData["nodes"];
  node: string;
  timezone: string;
}) {
  const { t, locale } = useT();
  const key = JSON.stringify(
    aggregate
      ? (nodes ?? [])
          .filter((n) => !node || n.dataset === node)
          .map((n) => ({
            id: n.dataset,
            name: n.path.join(" / ") || n.name,
            path: n.path
              .map((part) => "/swarm/nodes/" + encodeURIComponent(part))
              .join(""),
          }))
      : [{ id: "local", name: "", path: "" }],
  );
  const targets = useMemo(() => JSON.parse(key) as Target[], [key]);
  const [results, setResults] = useState<Result[]>([]);
  const [busy, setBusy] = useState(false);
  const request = useRef<AbortController | null>(null);
  const read = useCallback(
    async (refresh = false) => {
      request.current?.abort();
      const controller = new AbortController();
      request.current = controller;
      setBusy(true);
      // Nodes have independent accounts, including providers with the same name.
      const next: Result[] = new Array(targets.length);
      let index = 0;
      await Promise.all(
        Array.from({ length: Math.min(4, targets.length) }, async () => {
          while (index < targets.length && !controller.signal.aborted) {
            const i = index++;
            const target = targets[i]!;
            try {
              const answer = await statisticsRequest<{
                providers: ProviderUsage[];
              }>(
                `${target.path}/coddy/providers/usage${refresh ? "?refresh=1" : ""}`,
                { signal: controller.signal },
                aggregate ? statisticsRelay() : undefined,
              );
              next[i] = { ...target, providers: answer.providers };
            } catch (e) {
              next[i] = { ...target, providers: [], error: String(e) };
            }
          }
        }),
      );
      if (controller.signal.aborted) return;
      setResults((previous) =>
        next.map((result) =>
          result.error
            ? {
                ...result,
                providers: (
                  previous.find((old) => old.id === result.id)?.providers ?? []
                ).map((usage) => ({ ...usage, stale: true })),
              }
            : result,
        ),
      );
      request.current = null;
      setBusy(false);
    },
    [targets, aggregate],
  );
  useEffect(() => {
    setResults([]);
    void read();
    const timer = setInterval(() => {
      if (!document.hidden && !request.current) void read();
    }, 30000);
    return () => {
      clearInterval(timer);
      request.current?.abort();
    };
  }, [read]);
  const number = (value: number | undefined) =>
    value == null
      ? "—"
      : new Intl.NumberFormat(locale, { maximumFractionDigits: 2 }).format(
          value,
        );
  const time = (value: string | undefined) => {
    if (!value || !Number.isFinite(Date.parse(value))) return "—";
    return new Intl.DateTimeFormat(locale, {
      timeZone: timezone,
      dateStyle: "medium",
      timeStyle: "short",
    }).format(new Date(value));
  };
  const detail = (label: string, value: string, wide = false) => (
    <div className={wide ? "statistics-limit-wide" : undefined}>
      <dt>{label}</dt>
      <dd>{value}</dd>
    </div>
  );
  return (
    <section className="statistics-limits" aria-label={t("stats.limits")}>
      <div className="statistics-section-head">
        <p className="statistics-muted">{t("stats.limitsHint")}</p>
        <button
          className="settings-btn"
          disabled={busy}
          onClick={() => void read(true)}
        >
          {t("stats.refresh")}
        </button>
      </div>
      {busy && (
        <p className="statistics-progress" role="status">
          <CoddyWorkIndicator />
          {t("stats.loading")}
        </p>
      )}
      {!busy && results.length === 0 && (
        <p className="statistics-muted">{t("stats.noData")}</p>
      )}
      {results.map((result) => (
        <section key={result.id} className="statistics-limit-node">
          {result.name && <h3>{result.name}</h3>}
          {result.error && (
            <p role="alert" className="statistics-warning">
              {t("stats.limitsUnavailable")} {result.error}
            </p>
          )}
          {!result.error && result.providers.length === 0 && (
            <p className="statistics-muted">{t("stats.noProviders")}</p>
          )}
          <div className="statistics-provider-grid">
            {[...result.providers]
              .sort(
                (a, b) =>
                  Number(Boolean(a.unsupported)) -
                  Number(Boolean(b.unsupported)),
              )
              .map((usage) => (
                <article className="statistics-provider" key={usage.provider}>
                  <header>
                    <h3>{usage.provider}</h3>
                    <span className="statistics-muted">
                      {[usage.providerType, usage.plan]
                        .filter(Boolean)
                        .join(" · ")}
                    </span>
                  </header>
                  {usage.disabled ? (
                    <p className="statistics-muted">
                      {t("stats.limitsDisabled")}
                    </p>
                  ) : usage.unsupported ? (
                    <p className="statistics-muted">
                      {t("stats.limitsUnsupported")}
                    </p>
                  ) : (
                    <>
                      {usage.error && (
                        <p className="statistics-warning">
                          {t(
                            usage.error === "unauthorized"
                              ? "stats.limitsUnauthorized"
                              : "stats.limitsUnavailable",
                          )}
                        </p>
                      )}
                      {usage.stale && (
                        <p className="statistics-warning">{t("usage.stale")}</p>
                      )}
                      {usage.blocked && (
                        <p className="statistics-warning">
                          {t("usage.accountBlocked")}
                          {usage.retryAt &&
                            ` · ${t("usage.resets", { time: time(usage.retryAt) })}`}
                        </p>
                      )}
                      {usage.blockers?.length ? (
                        <p className="statistics-muted">
                          {usage.blockers.join(", ")}
                        </p>
                      ) : null}
                      {usage.unlimited && (
                        <p className="statistics-muted">
                          {t("stats.unlimited")}
                        </p>
                      )}
                      {(usage.windows ?? []).map((window) => {
                        const labelKey = usageWindowLabelKey(window);
                        const label = labelKey
                          ? t(labelKey)
                          : window.label || window.id;
                        const percent = usagePercent(window.usedPercent);
                        return (
                          <section
                            className="statistics-limit-window"
                            key={window.id}
                          >
                            <div className="statistics-limit-heading">
                              <h4>{label}</h4>
                              <span>
                                {t("stats.limitPercent", { percent })}
                              </span>
                            </div>
                            <progress
                              max={100}
                              value={Math.min(100, percent)}
                              aria-label={label}
                            />
                            <dl className="statistics-limit-details">
                              {window.used != null &&
                                detail(
                                  t("stats.limitUsed"),
                                  number(window.used),
                                )}
                              {window.limit != null &&
                                detail(
                                  t("stats.limitTotal"),
                                  number(window.limit),
                                )}
                              {window.remaining != null &&
                                detail(
                                  t("stats.limitRemaining"),
                                  number(window.remaining),
                                )}
                              {window.resetsAt &&
                                detail(
                                  t("stats.limitReset"),
                                  time(window.resetsAt),
                                  true,
                                )}
                              {!window.resetsAt &&
                                window.resetInSec != null &&
                                detail(
                                  t("stats.limitReset"),
                                  t("stats.durationSeconds", {
                                    seconds: window.resetInSec,
                                  }),
                                  true,
                                )}
                            </dl>
                          </section>
                        );
                      })}
                      <dl className="statistics-limit-details">
                        {usage.rate && (
                          <>
                            {detail(
                              t("stats.rate"),
                              `${number(usage.rate.used)} / ${number(usage.rate.limit)}`,
                            )}
                            {detail(
                              t("stats.limitRemaining"),
                              number(usage.rate.remaining),
                            )}
                            {detail(
                              t("stats.limitReset"),
                              t("stats.durationSeconds", {
                                seconds: usage.rate.resetInSec,
                              }),
                            )}
                          </>
                        )}
                        {usage.cooldownSec != null &&
                          detail(
                            t("stats.cooldown"),
                            t("stats.durationSeconds", {
                              seconds: usage.cooldownSec,
                            }),
                          )}
                        {usage.wallet && (
                          <>
                            {detail(
                              t("stats.wallet"),
                              new Intl.NumberFormat(locale, {
                                style: "currency",
                                currency: "RUB",
                              }).format(usage.wallet.balanceRub),
                            )}
                            {detail(
                              t("stats.walletSpent"),
                              new Intl.NumberFormat(locale, {
                                style: "currency",
                                currency: "RUB",
                              }).format(usage.wallet.spentRub30d),
                            )}
                          </>
                        )}
                      </dl>
                      {usage.unlimitedModels?.length ? (
                        <p className="statistics-muted">
                          {t("stats.unlimitedModels")}:{" "}
                          {usage.unlimitedModels.join(", ")}
                        </p>
                      ) : null}
                      {usage.blockedModels?.length ? (
                        <div>
                          <h4>{t("stats.blockedModels")}</h4>
                          <dl className="statistics-limit-details">
                            {usage.blockedModels.map((model) => (
                              <div key={model.model}>
                                <dt>
                                  {model.model}
                                  {model.blocker ? ` · ${model.blocker}` : ""}
                                </dt>
                                <dd>
                                  {model.retryAt
                                    ? time(model.retryAt)
                                    : model.retryInSec != null
                                      ? t("stats.durationSeconds", {
                                          seconds: model.retryInSec,
                                        })
                                      : "—"}
                                </dd>
                              </div>
                            ))}
                          </dl>
                        </div>
                      ) : null}
                      {!usage.error &&
                        !usage.windows?.length &&
                        !usage.rate &&
                        !usage.wallet &&
                        !usage.unlimited && (
                          <p className="statistics-muted">
                            {t("stats.limitsNoWindows")}
                          </p>
                        )}
                      {usage.fetchedAt && (
                        <p className="statistics-muted">
                          {t("stats.limitsUpdated", {
                            time: time(usage.fetchedAt),
                          })}
                        </p>
                      )}
                      {usage.refreshPending && (
                        <p className="statistics-muted">
                          {t("stats.limitsRefreshPending")}
                        </p>
                      )}
                    </>
                  )}
                </article>
              ))}
          </div>
        </section>
      ))}
    </section>
  );
}
