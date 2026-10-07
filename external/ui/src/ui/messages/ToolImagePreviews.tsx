import { useState } from "react";

import { ApiImage, ApiImageLightbox } from "../components/ApiImage";
import type { TranscriptFile } from "../chat/types";
import { useT } from "../i18n/I18nProvider";

/**
 * The pictures a tool call showed the model (`read` on an image file), under
 * the call's row: one preview card per picture, seen without opening the row,
 * that opens the original enlarged. The bytes come through the environment,
 * so a remote server or a relay serves them as well as the local one. A
 * picture with no thumbnail (a WebP) previews its original.
 */
export function ToolImagePreviews(props: {
  images: readonly TranscriptFile[];
}) {
  const { t } = useT();
  const [open, setOpen] = useState<{ src: string; alt: string } | null>(null);
  const shown = props.images.filter((f) => f.previewUrl || f.url);
  if (shown.length === 0) return null;
  return (
    <div className="tool-images" aria-label={t("messages.toolImages")}>
      {shown.map((f, i) => {
        const thumb = (f.previewUrl || f.url) as string;
        const full = (f.url || f.previewUrl) as string;
        return (
          <button
            key={`${i}:${full}`}
            type="button"
            className="tool-image-card"
            title={f.name}
            aria-label={t("messages.openToolImage", { fileName: f.name })}
            data-testid="tool-image-open"
            onClick={() => setOpen({ src: full, alt: f.name })}
          >
            <ApiImage
              className="tool-image-thumb"
              src={thumb}
              alt=""
              data-testid="tool-image-thumb"
            />
          </button>
        );
      })}
      {open ? (
        <ApiImageLightbox
          src={open.src}
          alt={open.alt}
          onClose={() => setOpen(null)}
        />
      ) : null}
    </div>
  );
}
