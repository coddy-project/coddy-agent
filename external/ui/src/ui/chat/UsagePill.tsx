import { useSyncExternalStore } from "react";
import { useT } from "../i18n/I18nProvider";
import {
  serverSnapshotShellStack,
  snapshotShellStack,
  subscribeShellStack,
} from "../shellBreakpoint";
import {
  formatResetTime,
  formatRub,
  formatDurationSec,
  summarizeUsage,
  usagePercent,
  type ProviderUsage,
  type UsageWindow,
} from "./providerUsage";

/**
 * Account usage pill next to the context ring: the session window as percent
 * used ("3h 3%"), the warning tone from 80 %, a "limit reached" pill in the
 * error tone on a block, "∞" for a model on the provider's unlimited option,
 * "key rejected" for a revoked login. The tooltip lists every window with its
 * reset time and the wallet. Design contract: DESIGN.md (Composer usage pill).
 */
export function UsagePill(props: {
  usage: ProviderUsage | null | undefined;
  modelId: string;
  now?: Date;
  /** Force the narrow-shell wording (tests). */
  compact?: boolean;
}) {
  const { t } = useT();
  const isMobileShell = useSyncExternalStore(
    subscribeShellStack,
    snapshotShellStack,
    serverSnapshotShellStack,
  );
  // On a narrow shell the actions row has no room for words: the pill
  // keeps the number (or one short word) and the tooltip keeps the rest.
  const compact = props.compact ?? isMobileShell;
  const summary = summarizeUsage(props.usage, props.modelId);
  if (summary.kind === "none" || summary.kind === "unsupported") return null;
  const now = props.now ?? new Date();
  const u = props.usage as ProviderUsage;
  const brand =
    u.providerType === "neuraldeep" ? "NeuralDeep" : u.provider;

  let text = "";
  let tone: "" | "warn" | "error" = "";
  const tipLines: string[] = [];
  if (u.plan) tipLines.push(`${brand} · ${u.plan}`);

  const windowLine = (w: UsageWindow) => {
    const pct = usagePercent(w.usedPercent);
    const counters =
      typeof w.used === "number" && typeof w.limit === "number"
        ? ` (${w.used.toLocaleString()} / ${w.limit.toLocaleString()})`
        : "";
    const resets = w.resetsAt
      ? ` · ${t("usage.resets", { time: formatResetTime(w.resetsAt, now) })}`
      : "";
    return `${w.label || w.id} ${pct}%${counters}${resets}`;
  };

  switch (summary.kind) {
    case "unauthorized":
      text = compact ? t("usage.pillCompactKey") : t("usage.pillKeyRejected");
      tone = "warn";
      tipLines.push(t("usage.keyRejected", { provider: summary.provider }));
      break;
    case "unlimited":
      text = compact ? "∞" : t("usage.pillUnlimited");
      tipLines.push(t("usage.unlimitedModel"));
      break;
    case "blocked": {
      tone = "error";
      switch (summary.block) {
        case "rate":
          text = t("usage.pillRateLimited");
          tipLines.push(
            t("usage.rateLimited", {
              retry: formatDurationSec(summary.retryInSec ?? 0),
            }),
          );
          break;
        case "key":
          text = t("usage.pillKeyBlocked");
          tipLines.push(t("usage.keyBlocked"));
          break;
        case "wallet":
          text = t("usage.pillWalletEmpty");
          tipLines.push(t("usage.walletEmpty"));
          break;
        case "account":
          text = t("usage.pillAccountBlocked");
          tipLines.push(t("usage.accountBlocked"));
          break;
        default:
          text = t("usage.pillLimitReached");
          tipLines.push(
            summary.retryAt
              ? t("usage.limitReachedResets", {
                  time: formatResetTime(summary.retryAt, now),
                })
              : t("usage.limitReached"),
          );
      }
      if (compact) text = t("usage.pillCompactBlocked");
      for (const w of u.windows ?? []) tipLines.push(windowLine(w));
      break;
    }
    case "metered": {
      const lead = summary.session ?? summary.week ?? summary.day;
      if (lead) {
        text = compact
          ? `${usagePercent(lead.usedPercent)}%`
          : `${lead.label || lead.id} ${usagePercent(lead.usedPercent)}%`;
      } else if (summary.wallet) {
        text = formatRub(summary.wallet.balanceRub);
      }
      tone = summary.warn ? "warn" : "";
      for (const w of u.windows ?? []) {
        if (w.id === "day" && usagePercent(w.usedPercent) === 0 && !w.exhausted) continue;
        tipLines.push(windowLine(w));
      }
      if (summary.stale) tipLines.push(t("usage.stale"));
      break;
    }
  }
  const wallet = u.wallet;
  if (wallet && summary.kind !== "unauthorized") {
    tipLines.push(
      t("usage.wallet", {
        balance: formatRub(wallet.balanceRub),
        spent: formatRub(wallet.spentRub30d),
      }),
    );
  }
  if (!text) return null;
  return (
    <div
      className={["composer-usage-host", tone ? `composer-usage-host--${tone}` : ""]
        .filter(Boolean)
        .join(" ")}
      tabIndex={0}
      aria-label={t("usage.aria")}
      data-testid="composer-usage-pill"
      data-tone={tone || "ok"}
    >
      <span className="composer-usage-pill">{text}</span>
      <span className="rail-tip composer-usage-tip" role="tooltip">
        {tipLines.join("\n")}
      </span>
    </div>
  );
}
