import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import { onEnvironmentSwitch } from "../env/remoteEnv";
import { acquireObjectUrl, RASTER_IMAGE_TYPE } from "../files/objectUrl";
import { useT } from "../i18n/I18nProvider";
import { FieldHint } from "../settings/FieldHint";
import { SETTINGS_TEXTS, landmarkAnchor } from "../settings/settingsSections";
import {
  backdropSnapshot,
  clearWallpaper,
  setWallpaper,
  setWallpaperDim,
  subscribeBackdrop,
  type BackdropState,
} from "./backdropPrefs";
import {
  GalleryReadError,
  galleryPhotoUrl,
  readGallery,
  readGalleryPhoto,
  type GalleryPhoto,
  type GalleryResult,
} from "./wallpaperGallery";

const SERVER_SNAPSHOT: BackdropState = {
  wallpaper: null,
  dim: 0,
};

type Refusal = NonNullable<GalleryResult["refusal"]>;

const REFUSAL_KEY: Record<Refusal, string> = {
  "not-image": "appearance.wallpaper.notImage",
  unreadable: "appearance.wallpaper.unreadable",
  storage: "appearance.wallpaper.storage",
  offline: "appearance.wallpaper.offline",
};

/** A thumbnail owns its authenticated raster read while the gallery is open. */
function GalleryThumbnail({ photo }: { photo: GalleryPhoto }) {
  const { t } = useT();
  const [src, setSrc] = useState<string>();
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    const lease = acquireObjectUrl(galleryPhotoUrl(photo), {
      accept: RASTER_IMAGE_TYPE,
      cap: Number.POSITIVE_INFINITY,
    });
    let active = true;
    void lease.promise.then(
      (url) => {
        if (active) setSrc(url);
      },
      () => {
        if (active) setFailed(true);
      },
    );
    return () => {
      active = false;
      lease.release();
    };
  }, [photo.name]);
  return failed ? (
    <span>{t("appearance.wallpaper.offline")}</span>
  ) : (
    <img src={src} alt="" loading="lazy" />
  );
}

/** Browser-local backdrop choices; the server gallery is read only while open. */
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
  const [photos, setPhotos] = useState<readonly GalleryPhoto[]>([]);
  const [galleryStatus, setGalleryStatus] = useState<
    "loading" | "empty" | "error" | null
  >(null);
  const selection = useRef<AbortController | null>(null);
  const listing = useRef<AbortController | null>(null);

  const cancelSelection = () => {
    selection.current?.abort();
    selection.current = null;
    setLoadingId(null);
  };

  useEffect(() => {
    const stop = onEnvironmentSwitch(() => {
      listing.current?.abort();
      cancelSelection();
      setGalleryOpen(false);
      setPhotos([]);
      setResult(null);
    });
    return () => {
      stop();
      listing.current?.abort();
      selection.current?.abort();
    };
  }, []);

  useEffect(() => {
    if (!galleryOpen) return;
    const abort = new AbortController();
    listing.current = abort;
    setPhotos([]);
    setGalleryStatus("loading");
    void readGallery(abort.signal).then(
      (next) => {
        if (abort.signal.aborted) return;
        setPhotos(next);
        setGalleryStatus(next.length ? null : "empty");
      },
      () => {
        if (!abort.signal.aborted) setGalleryStatus("error");
      },
    );
    return () => abort.abort();
  }, [galleryOpen]);

  const pickFile = async (file: File | undefined) => {
    if (!file) return;
    cancelSelection();
    const abort = new AbortController();
    selection.current = abort;
    const next = await setWallpaper(file, abort.signal);
    if (!abort.signal.aborted) setResult(next);
  };

  const pickPhoto = async (photo: GalleryPhoto) => {
    cancelSelection();
    const abort = new AbortController();
    selection.current = abort;
    setLoadingId(photo.name);
    setResult(null);
    try {
      const blob = await readGalleryPhoto(photo, abort.signal);
      if (abort.signal.aborted) return;
      const next = await setWallpaper(blob, abort.signal);
      if (!abort.signal.aborted) setResult(next);
    } catch (error) {
      if (!abort.signal.aborted) {
        setResult({
          refusal:
            error instanceof GalleryReadError ? error.refusal : "offline",
        });
      }
    } finally {
      if (!abort.signal.aborted) setLoadingId(null);
    }
  };

  const removeWallpaper = async () => {
    cancelSelection();
    const abort = new AbortController();
    selection.current = abort;
    setResult(null);
    const next = await clearWallpaper();
    if (!abort.signal.aborted) setResult(next);
  };

  const refusal = result?.refusal ?? (state.storageError ? "storage" : null);
  const size = result && "width" in result ? result : null;
  const status = refusal
    ? t(REFUSAL_KEY[refusal], {
        width: String(size?.width ?? ""),
        height: String(size?.height ?? ""),
      })
    : null;
  const wallpaperLabel = t(SETTINGS_TEXTS.appearanceWallpaper.label);

  return (
    <>
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
            onClick={() => {
              if (galleryOpen) {
                listing.current?.abort();
                cancelSelection();
              }
              setGalleryOpen((open) => !open);
            }}
          >
            {t("appearance.wallpaper.gallery")}
          </button>
          {state.wallpaper ? (
            <button
              type="button"
              className="settings-btn settings-btn-danger"
              data-testid="appearance-wallpaper-remove"
              onClick={() => void removeWallpaper()}
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
        {galleryOpen ? (
          <>
            <div
              className="appearance-wallpaper-gallery"
              data-testid="appearance-wallpaper-grid"
            >
              {photos.map((photo) => (
                <button
                  key={photo.name}
                  type="button"
                  className={`appearance-wallpaper-thumb${loadingId === photo.name ? " is-loading" : ""}`}
                  title={photo.name}
                  aria-label={photo.name}
                  data-testid={`appearance-wallpaper-thumb-${photo.name}`}
                  disabled={loadingId !== null}
                  onClick={() => void pickPhoto(photo)}
                >
                  <GalleryThumbnail photo={photo} />
                </button>
              ))}
            </div>
            {galleryStatus ? (
              <p
                className="settings-field-desc appearance-backdrop-status"
                role="status"
                data-testid="appearance-wallpaper-gallery-status"
              >
                {t(`appearance.wallpaper.gallery.${galleryStatus}`)}
              </p>
            ) : null}
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
