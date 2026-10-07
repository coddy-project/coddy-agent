import { useT } from "../i18n/I18nProvider";
import {
  formatDurationSec,
  formatResetTime,
  remoteBusy,
  summarizeUsage,
  usageBannerKey,
  usagePercent,
  usageProviderBrand,
  usageWarnWindow,
  usageWindowLabelKey,
  type ProviderUsage,
} from "./providerUsage";

/**
 * The Claude Desktop style notice above the composer: at 80 % of a window,
 * "You've used 85% of your NeuralDeep 3h limit · Resets 20:59" with a
 * dismiss control (remembered per provider row, window and period), and on
 * a block the error tone: a timed block names its reset ("Usage limit
 * reached · Resets 20:59"), the others name their cause (a blocked key, an
 * empty wallet, a blocked account, a rate limit). A call waiting for a free
 * stream slot of a remote Coddy (blocker remote_busy) is no limit of an
 * account: its notice says what is waited for and until when, carries no
 * dismiss control (it goes away by itself) and is never hidden by an earlier
 * dismissal. Returns null when there is nothing to say or the notice was
 * dismissed for this period.
 */
export function UsageBanner(props: {
  usage: ProviderUsage | null | undefined;
  modelId: string;
  dismissedKey?: string;
  onDismiss?: (key: string) => void;
  now?: Date;
}) {
  const { t, locale } = useT();
  const summary = summarizeUsage(props.usage, props.modelId);
  const now = props.now ?? new Date();
  const key = usageBannerKey(props.usage, props.modelId);
  if (!key || (!remoteBusy(props.usage) && props.dismissedKey === key)) {
    return null;
  }
  const u = props.usage as ProviderUsage;
  const brand = usageProviderBrand(u);
  let text = "";
  let tone: "warn" | "error" = "warn";
  const waitingForRemote = summary.kind === "blocked" && remoteBusy(u);
  if (waitingForRemote) {
    text = u.retryAt
      ? t("usage.remoteBusyUntil", {
          time: formatResetTime(u.retryAt, now, locale),
        })
      : t("usage.remoteBusy");
  } else if (summary.kind === "blocked" && u.resuming) {
    // The turn is waiting for the reset and resumes by itself: a calmer
    // notice than a block the user has to act on.
    text = u.retryAt
      ? t("usage.bannerResumingAt", {
          time: formatResetTime(u.retryAt, now, locale),
        })
      : t("usage.bannerResuming");
  } else if (summary.kind === "blocked") {
    tone = "error";
    switch (summary.block) {
      case "rate":
        text = t("usage.bannerRateLimited", {
          retry: formatDurationSec(summary.retryInSec ?? 0),
        });
        break;
      case "key":
        text = t("usage.bannerKeyBlocked");
        break;
      case "wallet":
        text = t("usage.bannerWalletEmpty");
        break;
      case "account":
        text = t("usage.bannerAccountBlocked");
        break;
      default:
        text = summary.retryAt
          ? t("usage.bannerLimitReachedResets", {
              time: formatResetTime(summary.retryAt, now, locale),
            })
          : t("usage.bannerLimitReached");
    }
  } else if (summary.kind === "metered" && summary.warn) {
    const w = usageWarnWindow(u);
    if (!w) return null;
    const labelKey = usageWindowLabelKey(w);
    text = t("usage.bannerUsed", {
      percent: String(usagePercent(w.usedPercent)),
      brand,
      window: labelKey ? t(labelKey) : w.label || w.id,
    });
    if (w.resetsAt) {
      text += ` · ${t("usage.resets", { time: formatResetTime(w.resetsAt, now, locale) })}`;
    }
  } else {
    return null;
  }
  return (
    <div
      className={`usage-banner usage-banner--${tone}`}
      role="status"
      data-testid="usage-banner"
      data-tone={tone}
    >
      <span className="usage-banner-text">{text}</span>
      {waitingForRemote ? null : (
        <button
          type="button"
          className="usage-banner-dismiss"
          aria-label={t("usage.bannerDismiss")}
          onClick={() => props.onDismiss?.(key)}
        >
          ×
        </button>
      )}
    </div>
  );
}
