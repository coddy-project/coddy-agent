import { useT } from "../i18n/I18nProvider";
import {
  formatResetTime,
  summarizeUsage,
  usageBannerKey,
  usagePercent,
  usageWarnWindow,
  type ProviderUsage,
} from "./providerUsage";

/**
 * The Claude Desktop style notice above the composer: at 80 % of a window,
 * "You've used 85% of your NeuralDeep 3h limit · Resets 20:59" with a
 * dismiss control (remembered per window and period), and on a hit limit
 * "Usage limit reached · Resets 20:59" in the error tone. Returns null when
 * there is nothing to say or the notice was dismissed for this period.
 */
export function UsageBanner(props: {
  usage: ProviderUsage | null | undefined;
  modelId: string;
  dismissedKey?: string;
  onDismiss?: (key: string) => void;
  now?: Date;
}) {
  const { t } = useT();
  const summary = summarizeUsage(props.usage, props.modelId);
  const now = props.now ?? new Date();
  const key = usageBannerKey(props.usage);
  if (!key || props.dismissedKey === key) return null;
  const u = props.usage as ProviderUsage;
  const brand = u.providerType === "neuraldeep" ? "NeuralDeep" : u.provider;
  let text = "";
  let tone: "warn" | "error" = "warn";
  if (summary.kind === "blocked") {
    tone = "error";
    text = summary.retryAt
      ? t("usage.bannerLimitReachedResets", {
          time: formatResetTime(summary.retryAt, now),
        })
      : t("usage.bannerLimitReached");
  } else if (summary.kind === "metered" && summary.warn) {
    const w = usageWarnWindow(u);
    if (!w) return null;
    text = t("usage.bannerUsed", {
      percent: String(usagePercent(w.usedPercent)),
      brand,
      window: w.label || w.id,
    });
    if (w.resetsAt) {
      text += ` · ${t("usage.resets", { time: formatResetTime(w.resetsAt, now) })}`;
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
      <button
        type="button"
        className="usage-banner-dismiss"
        aria-label={t("usage.bannerDismiss")}
        onClick={() => props.onDismiss?.(key)}
      >
        ×
      </button>
    </div>
  );
}
