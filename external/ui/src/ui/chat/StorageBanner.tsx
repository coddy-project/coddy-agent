import { useT } from "../i18n/I18nProvider";
import {
  dismissStorageLow,
  useStorageStatus,
  type StorageStatus,
} from "../env/storageStatus";

const MB = 1024 * 1024;
const GB = 1024 * MB;

/**
 * formatStorageSize writes a byte count the way the person's language writes a size: whole
 * megabytes below a gigabyte, gigabytes with one decimal above ("300 MB", "1.5 GB"; "300 МБ",
 * "1,5 ГБ"). The unit comes from the platform's number formatting, so no language needs a
 * unit string of its own here.
 */
export function formatStorageSize(bytes: number, locale: string): string {
  const useGB = bytes >= GB;
  const value = useGB ? bytes / GB : Math.max(Math.round(bytes / MB), 0);
  try {
    return new Intl.NumberFormat(locale, {
      style: "unit",
      unit: useGB ? "gigabyte" : "megabyte",
      unitDisplay: "short",
      maximumFractionDigits: useGB ? 1 : 0,
    }).format(value);
  } catch {
    return `${useGB ? value.toFixed(1) : value} ${useGB ? "GB" : "MB"}`;
  }
}

function messageKey(status: StorageStatus): string {
  return `storage.banner.${status.state}.${status.volume === "home" ? "home" : "sessions"}`;
}

/**
 * The notice above the composer when the disk that stores the server's sessions runs low or
 * full (issue #465). Low is the warning tone and can be dismissed for the tab; full is the
 * error tone, an alert, and stays until a save works again, since it is the reason a message
 * may not be kept. It stands where the usage notice stands, in the hero and in the docked
 * composer, and says nothing while the disk has room, or when the server does not report one.
 */
export function StorageBanner() {
  const { t, locale } = useT();
  const { status, dismissedLow } = useStorageStatus();
  if (!status || status.state === "ok") return null;
  const full = status.state === "full";
  if (!full && dismissedLow) return null;
  const free =
    status.freeBytes === null
      ? ""
      : formatStorageSize(status.freeBytes, locale);
  return (
    <div
      className={`storage-banner storage-banner--${full ? "error" : "warn"}`}
      role={full ? "alert" : "status"}
      data-testid="storage-banner"
      data-tone={full ? "error" : "warn"}
      data-state={status.state}
      data-volume={status.volume}
    >
      <span className="storage-banner-text">
        {t(messageKey(status), { free })}
      </span>
      {full ? null : (
        <button
          type="button"
          className="storage-banner-dismiss"
          aria-label={t("storage.banner.dismiss")}
          onClick={dismissStorageLow}
        >
          ×
        </button>
      )}
    </div>
  );
}
