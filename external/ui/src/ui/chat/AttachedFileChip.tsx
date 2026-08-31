import { useEffect, useMemo } from "react";
import { useT } from "../i18n/I18nProvider";
import { fileTypeIcon } from "../messages/fileTypeIcon";
import { fmtBytes } from "./composerLabels";

/** Local preview URL for an attached image; revoked when the file changes or the chip unmounts. */
function useImageObjectUrl(file: File): string | null {
  const url = useMemo(() => {
    if (!file.type.startsWith("image/")) return null;
    if (
      typeof URL === "undefined" ||
      typeof URL.createObjectURL !== "function"
    ) {
      return null;
    }
    return URL.createObjectURL(file);
  }, [file]);
  useEffect(() => {
    if (!url) return;
    return () => {
      if (
        typeof URL !== "undefined" &&
        typeof URL.revokeObjectURL === "function"
      ) {
        URL.revokeObjectURL(url);
      }
    };
  }, [url]);
  return url;
}

/** Live attachment chip; image files render a thumbnail instead of the generic icon. */
export function AttachedFileChip({
  file,
  disabled,
  onRemove,
}: {
  file: File;
  disabled: boolean;
  onRemove: () => void;
}) {
  const { t } = useT();
  const { svg, label } = fileTypeIcon(file.type, file.name);
  const thumbUrl = useImageObjectUrl(file);
  const tip = t("composer.attachmentTooltip", {
    fileName: file.name,
    label,
    size: fmtBytes(file.size, t),
  });
  return (
    <span
      className={[
        "composer-attachment-chip",
        thumbUrl ? "composer-attachment-chip--image" : "",
        disabled ? "composer-attachment-chip--disabled" : "",
      ]
        .filter(Boolean)
        .join(" ")}
      title={tip}
      aria-disabled={disabled ? "true" : undefined}
      data-testid="composer-attachment-chip"
    >
      <span className="composer-attachment-chip-icon" aria-hidden="true">
        {thumbUrl ? (
          <img
            className="composer-attachment-thumb"
            src={thumbUrl}
            alt=""
            data-testid="composer-attachment-thumb"
          />
        ) : (
          svg
        )}
      </span>
      <span className="composer-attachment-chip-name">{file.name}</span>
      <button
        type="button"
        className="composer-attachment-chip-remove"
        aria-label={t("composer.removeAttachment", { fileName: file.name })}
        onClick={onRemove}
      >
        ×
      </button>
    </span>
  );
}
