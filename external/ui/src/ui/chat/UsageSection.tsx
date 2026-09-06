import { useT } from "../i18n/I18nProvider";
import {
  formatDurationSec,
  formatResetTime,
  formatRub,
  summarizeUsage,
  usagePercent,
  usagePlanLabel,
  usageWindowLabelKey,
  type ProviderUsage,
  type UsageWindow,
} from "./providerUsage";

/**
 * The account usage block of the context popover, the way Claude Desktop
 * lists its plan limits under the context window: one meter per metered
 * window (the session, the week, the day when it is above zero) with the
 * reset time and the percent used, the wallet below, and a note for the
 * states that change what the user can do (a hit limit, a rejected key, a
 * model on the provider's unlimited option, a turn waiting for the reset,
 * a stale read). Renders nothing when the selected model's provider has no
 * usage. Design contract: DESIGN.md (Context popover usage section).
 */
export function UsageSection(props: {
  usage: ProviderUsage | null | undefined;
  modelId: string;
  now?: Date;
}) {
  const { t, locale } = useT();
  const summary = summarizeUsage(props.usage, props.modelId);
  if (summary.kind === "none") return null;
  const now = props.now ?? new Date();
  const u = props.usage as ProviderUsage;
  const brand = u.providerType === "neuraldeep" ? "NeuralDeep" : u.provider;
  const windowName = (w: UsageWindow) => {
    const key = usageWindowLabelKey(w);
    return key ? t(key) : w.label || w.id;
  };

  let note = "";
  let noteTone: "" | "warn" | "error" = "";
  switch (summary.kind) {
    case "unauthorized":
      note = t("usage.keyRejected", { provider: summary.provider });
      noteTone = "warn";
      break;
    case "unlimited":
      note = t("usage.unlimitedModel");
      break;
    case "blocked":
      noteTone = "error";
      if (u.resuming) {
        noteTone = "warn";
        note = u.retryAt
          ? t("usage.resumingAt", { time: formatResetTime(u.retryAt, now, locale) })
          : t("usage.resuming");
        break;
      }
      switch (summary.block) {
        case "rate":
          note = t("usage.rateLimited", { retry: formatDurationSec(summary.retryInSec ?? 0) });
          break;
        case "key":
          note = t("usage.keyBlocked");
          break;
        case "wallet":
          note = t("usage.walletEmpty");
          break;
        case "account":
          note = t("usage.accountBlocked");
          break;
        default:
          note = summary.retryAt
            ? t("usage.limitReachedResets", { time: formatResetTime(summary.retryAt, now, locale) })
            : t("usage.limitReached");
      }
      break;
    case "metered":
      if (summary.stale) note = t("usage.stale");
      break;
  }

  const rows = (u.windows ?? []).filter(
    (w) => !(w.id === "day" && usagePercent(w.usedPercent) === 0 && !w.exhausted),
  );
  // Each meter keeps its own tone: a block colours the exhausted window
  // red, the others stay where their percent puts them.
  const rowTone = (w: UsageWindow): "ok" | "warn" | "error" => {
    const pct = usagePercent(w.usedPercent);
    if (w.exhausted || pct >= 100) return "error";
    return pct >= 80 ? "warn" : "ok";
  };

  return (
    <div className="context-usage" data-testid="context-usage" data-kind={summary.kind}>
      <div className="context-usage-head">
        <span className="context-usage-title">
          {u.plan ? `${brand} · ${usagePlanLabel(u.plan)}` : brand}
        </span>
        {note ? (
          <span
            className={["context-usage-note", noteTone ? `context-usage-note--${noteTone}` : ""]
              .filter(Boolean)
              .join(" ")}
            data-testid="context-usage-note"
          >
            {note}
          </span>
        ) : null}
      </div>
      {summary.kind !== "unauthorized" && rows.length > 0 ? (
        <ul className="context-usage-rows">
          {rows.map((w) => {
            const pct = usagePercent(w.usedPercent);
            const tone = rowTone(w);
            return (
              <li key={w.id} data-testid={`context-usage-row-${w.id}`} data-tone={tone}>
                <div className="context-usage-row-head">
                  <span className="context-usage-label">{windowName(w)}</span>
                  <span className="context-usage-meta">
                    {w.resetsAt
                      ? t("usage.resets", { time: formatResetTime(w.resetsAt, now, locale) })
                      : ""}
                    <span className="context-usage-pct">{pct}%</span>
                  </span>
                </div>
                <div
                  className="context-usage-track"
                  role="img"
                  aria-label={`${windowName(w)} ${pct}%`}
                >
                  <div className="context-usage-fill" style={{ width: `${Math.min(100, pct)}%` }} />
                </div>
              </li>
            );
          })}
        </ul>
      ) : null}
      {u.wallet && summary.kind !== "unauthorized" ? (
        <div className="context-usage-foot" data-testid="context-usage-wallet">
          {t("usage.wallet", {
            balance: formatRub(u.wallet.balanceRub),
            spent: formatRub(u.wallet.spentRub30d),
          })}
        </div>
      ) : null}
    </div>
  );
}
