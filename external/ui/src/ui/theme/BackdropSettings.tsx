import { useCallback, useRef, useState, useSyncExternalStore } from "react";
import { useT } from "../i18n/I18nProvider";
import { FieldHint } from "../settings/FieldHint";
import { SwitchField } from "../settings/SwitchField";
import { SETTINGS_TEXTS, landmarkAnchor } from "../settings/settingsSections";
import {
  backdropSnapshot,
  clearWallpaper,
  setSmoothBackdrop,
  setWallpaper,
  setWallpaperDim,
  subscribeBackdrop,
  WALLPAPER_MIN_HEIGHT,
  WALLPAPER_MIN_WIDTH,
  type BackdropState,
} from "./backdropPrefs";
import {
  GALLERY,
  galleryThumbUrl,
  pickGalleryPhoto,
  type GalleryPhoto,
  type GalleryResult,
} from "./wallpaperGallery";

const SERVER_SNAPSHOT: BackdropState = {
  smooth: false,
  wallpaper: null,
  credit: null,
  dim: 0,
};

type Refusal = NonNullable<GalleryResult["refusal"]>;

const REFUSAL_KEY: Record<Refusal, string> = {
  "not-image": "appearance.wallpaper.notImage",
  "too-large": "appearance.wallpaper.tooLarge",
  "too-small": "appearance.wallpaper.tooSmall",
  unreadable: "appearance.wallpaper.unreadable",
  offline: "appearance.wallpaper.offline",
};

/**
 * BackdropSettings is the page backdrop under the notifications in Settings →
 * Appearance: the smooth gradients switch and the wallpaper (a picture from
 * this computer or a photo of the gallery, its dimming, its removal). Both are
 * kept in this browser only (ui/theme/backdropPrefs.ts). The gallery's
 * thumbnails are requested only once it is opened.
 */
export function BackdropSettings() {
  const { t } = useT();
  const state = useSyncExternalStore(
    subscribeBackdrop,
    backdropSnapshot,
    () => SERVER_SNAPSHOT,
  );
  const input = useRef<HTMLInputElement>(null);
  const [result, setResult] = useState<GalleryResult | null>(null);
  const [galleryOpen, setGalleryOpen] = useState(false);
  const [loadingId, setLoadingId] = useState<string | null>(null);

  const pickFile = useCallback(async (file: File | undefined) => {
    if (file) {
      setResult(await setWallpaper(file));
    }
  }, []);

  const pickPhoto = useCallback(async (photo: GalleryPhoto) => {
    setLoadingId(photo.id);
    try {
      setResult(await pickGalleryPhoto(photo));
    } finally {
      setLoadingId(null);
    }
  }, []);

  const refusal = result?.refusal ?? null;
  const size = result && "width" in result ? result : null;
  const status = refusal
    ? t(REFUSAL_KEY[refusal], {
        width: String(size?.width ?? ""),
        height: String(size?.height ?? ""),
        minWidth: String(WALLPAPER_MIN_WIDTH),
        minHeight: String(WALLPAPER_MIN_HEIGHT),
      })
    : null;
  const wallpaperLabel = t(SETTINGS_TEXTS.appearanceWallpaper.label);

  return (
    <>
      <div
        className="appearance-backdrop-block"
        data-testid="appearance-smooth-backdrop"
        data-settings-anchor={landmarkAnchor("appearance", "smooth-backdrop")}
      >
        <p className="appearance-section-label">
          {t(SETTINGS_TEXTS.appearanceSmoothBackdrop.label)}
        </p>
        <SwitchField
          checked={state.smooth}
          onChange={setSmoothBackdrop}
          label={t("appearance.smoothBackdrop.toggle")}
          description={t("appearance.smoothBackdrop.hint")}
          dataTestId="appearance-smooth-backdrop-switch"
        />
      </div>
      <div
        className="appearance-backdrop-block"
        data-testid="appearance-wallpaper"
        data-settings-anchor={landmarkAnchor("appearance", "wallpaper")}
      >
        <p className="appearance-section-label">
          {wallpaperLabel}
          <FieldHint
            text={t(SETTINGS_TEXTS.appearanceWallpaper.desc)}
            label={wallpaperLabel}
          />
        </p>
        <div className="appearance-wallpaper-row">
          {state.wallpaper ? (
            <img
              className="appearance-wallpaper-preview"
              src={state.wallpaper}
              alt=""
              data-testid="appearance-wallpaper-preview"
              data-native-image-menu
            />
          ) : null}
          <button
            type="button"
            className="settings-btn"
            data-testid="appearance-wallpaper-choose"
            onClick={() => input.current?.click()}
          >
            {t("appearance.wallpaper.choose")}
          </button>
          <button
            type="button"
            className="settings-btn"
            aria-expanded={galleryOpen}
            data-testid="appearance-wallpaper-gallery"
            onClick={() => setGalleryOpen((open) => !open)}
          >
            {t("appearance.wallpaper.gallery")}
          </button>
          {state.wallpaper ? (
            <button
              type="button"
              className="settings-btn settings-btn-danger"
              data-testid="appearance-wallpaper-remove"
              onClick={() => {
                setResult(null);
                void clearWallpaper();
              }}
            >
              {t("appearance.wallpaper.remove")}
            </button>
          ) : null}
          <input
            ref={input}
            type="file"
            accept="image/*"
            hidden
            data-testid="appearance-wallpaper-input"
            onChange={(event) => {
              void pickFile(event.currentTarget.files?.[0]);
              event.currentTarget.value = "";
            }}
          />
        </div>
        {state.credit ? (
          <p
            className="settings-field-desc appearance-backdrop-status"
            data-testid="appearance-wallpaper-credit"
          >
            {t("appearance.wallpaper.creditPrefix")}{" "}
            <a href={state.credit.url} target="_blank" rel="noreferrer">
              {state.credit.author}
            </a>
            {t("appearance.wallpaper.creditSuffix")}
          </p>
        ) : null}
        {galleryOpen ? (
          <>
            <div
              className="appearance-wallpaper-gallery"
              data-testid="appearance-wallpaper-grid"
            >
              {GALLERY.map((photo, index) => (
                <button
                  key={photo.id}
                  type="button"
                  className={`appearance-wallpaper-thumb${loadingId === photo.id ? " is-loading" : ""}`}
                  title={photo.author}
                  aria-label={t("appearance.wallpaper.photoLabel", {
                    n: String(index + 1),
                    author: photo.author,
                  })}
                  data-testid={`appearance-wallpaper-thumb-${photo.id}`}
                  disabled={loadingId !== null}
                  onClick={() => void pickPhoto(photo)}
                >
                  <img src={galleryThumbUrl(photo)} alt="" loading="lazy" />
                </button>
              ))}
            </div>
            <p className="settings-field-desc appearance-backdrop-status">
              {t("appearance.wallpaper.galleryHint")}
            </p>
          </>
        ) : null}
        {state.wallpaper ? (
          <label className="appearance-wallpaper-dim">
            <span>
              {t("appearance.wallpaper.dim", { value: String(state.dim) })}
            </span>
            <input
              type="range"
              min={0}
              max={90}
              step={5}
              value={state.dim}
              data-testid="appearance-wallpaper-dim"
              onChange={(event) =>
                setWallpaperDim(Number(event.currentTarget.value))
              }
            />
          </label>
        ) : null}
        {status ? (
          <p
            className="settings-field-desc appearance-backdrop-status"
            role="status"
            data-testid="appearance-wallpaper-status"
          >
            {status}
          </p>
        ) : null}
      </div>
    </>
  );
}
