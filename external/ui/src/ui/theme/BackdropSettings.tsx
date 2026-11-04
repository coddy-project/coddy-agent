import { useEffect, useRef, useState, useSyncExternalStore } from "react";
import { getEnv, onEnvironmentSwitch, subscribeEnv } from "../env/remoteEnv";
import { acquireObjectUrl, RASTER_IMAGE_TYPE } from "../files/objectUrl";
import { useT } from "../i18n/I18nProvider";
import { useConfirm, type ConfirmFn } from "../components/useConfirm";
import { FieldHint } from "../settings/FieldHint";
import { SETTINGS_TEXTS, landmarkAnchor } from "../settings/settingsSections";
import {
  backdropSnapshot,
  beginWallpaperChoice,
  clearWallpaper,
  clearDeletedWallpaper,
  wallpaperChoiceRevision,
  loadWallpaper,
  setWallpaper,
  setWallpaperDim,
  subscribeBackdrop,
  type BackdropState,
} from "./backdropPrefs";
import {
  GalleryReadError,
  deleteGalleryPhoto,
  galleryEnvironmentScope,
  galleryPhotoUrl,
  readGallery,
  readGalleryPhoto,
  uploadGalleryPhoto,
  type GalleryPhoto,
  type GalleryResult,
} from "./wallpaperGallery";

const SERVER_SNAPSHOT: BackdropState = { wallpaper: null, dim: 0 };
const REFUSAL_KEY: Record<NonNullable<GalleryResult["refusal"]>, string> = {
  "not-image": "appearance.wallpaper.notImage",
  unreadable: "appearance.wallpaper.unreadable",
  storage: "appearance.wallpaper.storage",
  offline: "appearance.wallpaper.offline",
  upload: "appearance.wallpaper.uploadError",
  delete: "appearance.wallpaper.deleteError",
};

/** A thumbnail owns its authenticated raster read for as long as it is drawn. */
function GalleryThumbnail({ photo }: { photo: GalleryPhoto }) {
  const { t } = useT();
  const [src, setSrc] = useState<string>();
  const [failed, setFailed] = useState(false);
  const image = useRef<HTMLImageElement>(null);
  useEffect(() => {
    let active = true;
    let lease: ReturnType<typeof acquireObjectUrl> | undefined;
    const load = () => {
      if (lease) return;
      lease = acquireObjectUrl(galleryPhotoUrl(photo), {
        accept: RASTER_IMAGE_TYPE,
        cap: Number.POSITIVE_INFINITY,
      });
      void lease.promise.then(
        (url) => {
          if (active) setSrc(url);
        },
        () => {
          if (active) setFailed(true);
        },
      );
    };
    let observer: IntersectionObserver | undefined;
    if (typeof IntersectionObserver === "undefined" || !image.current) {
      load();
    } else {
      observer = new IntersectionObserver(
        (entries) => {
          if (entries.some((entry) => entry.isIntersecting)) {
            observer?.disconnect();
            load();
          }
        },
        { rootMargin: "200px" },
      );
      observer.observe(image.current);
    }
    return () => {
      active = false;
      observer?.disconnect();
      lease?.release();
    };
  }, [photo.name]);
  return failed ? (
    <span>{t("appearance.wallpaper.offline")}</span>
  ) : (
    <img ref={image} src={src} alt="" />
  );
}

function GalleryDeleteButton({
  photo,
  onDelete,
}: {
  photo: GalleryPhoto;
  onDelete: (photo: GalleryPhoto, confirm: ConfirmFn) => Promise<void>;
}) {
  const { t } = useT();
  const confirm = useConfirm();
  const label = t("appearance.wallpaper.delete", { name: photo.name });
  return (
    <button
      type="button"
      className="appearance-wallpaper-delete"
      title={label}
      aria-label={label}
      data-testid={`appearance-wallpaper-delete-${photo.name}`}
      onClick={() => void onDelete(photo, confirm)}
    >
      ×
    </button>
  );
}

function WallpaperActionIcon({ clear = false }: { clear?: boolean }) {
  return (
    <svg
      width="18"
      height="18"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.75"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden
    >
      {clear ? (
        <>
          <rect x="3" y="3" width="18" height="18" rx="3" />
          <path d="m7 7 10 10M17 7 7 17" />
        </>
      ) : (
        <>
          <path d="M12 16V3m-5 5 5-5 5 5M4 16v5h16v-5" />
        </>
      )}
    </svg>
  );
}

/** Server gallery management and the browser-local background selection. */
export function BackdropSettings() {
  const { t } = useT();
  const state = useSyncExternalStore(
    subscribeBackdrop,
    backdropSnapshot,
    () => SERVER_SNAPSHOT,
  );
  const env = useSyncExternalStore(subscribeEnv, getEnv, getEnv);
  const input = useRef<HTMLInputElement>(null);
  const [result, setResult] = useState<GalleryResult | null>(null);
  const [loadingId, setLoadingId] = useState<string | null>(null);
  const [photos, setPhotos] = useState<readonly GalleryPhoto[]>([]);
  const [galleryStatus, setGalleryStatus] = useState<
    "loading" | "empty" | "error" | null
  >("loading");
  const operation = useRef<AbortController | null>(null);
  const listing = useRef<AbortController | null>(null);
  const mounted = useRef(false);
  const viewRevision = useRef(0);

  const refresh = async (source = getEnv(), parent?: AbortSignal) => {
    listing.current?.abort();
    const abort = new AbortController();
    listing.current = abort;
    const cancel = () => abort.abort();
    parent?.addEventListener("abort", cancel, { once: true });
    if (parent?.aborted) cancel();
    try {
      const next = await readGallery(abort.signal);
      if (abort.signal.aborted || getEnv() !== source) return;
      setPhotos(next);
      setGalleryStatus(next.length ? null : "empty");
    } catch {
      if (!abort.signal.aborted && getEnv() === source)
        setGalleryStatus("error");
    } finally {
      parent?.removeEventListener("abort", cancel);
    }
  };

  useEffect(() => {
    mounted.current = true;
    viewRevision.current++;
    operation.current?.abort();
    setPhotos([]);
    setResult(null);
    setLoadingId(null);
    setGalleryStatus("loading");
    void refresh(env);
    const stop = onEnvironmentSwitch(() => {
      operation.current?.abort();
      listing.current?.abort();
    });
    return () => {
      mounted.current = false;
      viewRevision.current++;
      stop();
      operation.current?.abort();
      listing.current?.abort();
    };
  }, [env]);

  const begin = (id: string) => {
    operation.current?.abort();
    const abort = new AbortController();
    operation.current = abort;
    const source = getEnv();
    setLoadingId(id);
    setResult(null);
    return {
      abort,
      source,
      current: () => !abort.signal.aborted && getEnv() === source,
    };
  };
  const finish = (action: ReturnType<typeof begin>) => {
    if (operation.current === action.abort) setLoadingId(null);
  };

  const pickPhoto = async (photo: GalleryPhoto) => {
    const action = begin(photo.name);
    beginWallpaperChoice();
    const gallery = { scope: galleryEnvironmentScope(), name: photo.name };
    try {
      const blob = await readGalleryPhoto(photo, action.abort.signal);
      if (!action.current()) return;
      const next = await setWallpaper(blob, action.abort.signal, gallery);
      if (action.current()) setResult(next);
    } catch (error) {
      if (action.current())
        setResult({
          refusal:
            error instanceof GalleryReadError ? error.refusal : "offline",
        });
    } finally {
      finish(action);
    }
  };

  const upload = async (file: File | undefined) => {
    if (!file) return;
    const action = begin("upload");
    const scope = galleryEnvironmentScope();
    try {
      const { wallpaper } = await uploadGalleryPhoto(file, action.abort.signal);
      if (!action.current()) return;
      setPhotos((previous) =>
        [
          ...previous.filter((photo) => photo.name !== wallpaper.name),
          wallpaper,
        ].sort((a, b) => (a.name < b.name ? -1 : a.name > b.name ? 1 : 0)),
      );
      setGalleryStatus(null);
      await refresh(action.source, action.abort.signal);
      if (!action.current()) return;
      await loadWallpaper();
      if (
        !action.current() ||
        backdropSnapshot().wallpaper ||
        backdropSnapshot().storageError
      )
        return;
      const blob = file.type.startsWith("image/")
        ? file
        : await readGalleryPhoto(wallpaper, action.abort.signal);
      if (!action.current()) return;
      const next = await setWallpaper(blob, action.abort.signal, {
        scope,
        name: wallpaper.name,
      });
      if (action.current()) setResult(next);
    } catch (error) {
      if (action.current())
        setResult({
          refusal: error instanceof GalleryReadError ? error.refusal : "upload",
        });
    } finally {
      finish(action);
    }
  };

  const removeWallpaper = async () => {
    const action = begin("clear");
    try {
      const next = await clearWallpaper(action.abort.signal);
      if (action.current()) setResult(next);
    } finally {
      finish(action);
    }
  };

  const deletePhoto = async (photo: GalleryPhoto, confirm: ConfirmFn) => {
    // Capture the source before opening the dialog; its answer may arrive elsewhere.
    const source = getEnv();
    const scope = galleryEnvironmentScope();
    const previousOperation = operation.current;
    const choiceRevision = wallpaperChoiceRevision();
    const view = viewRevision.current;
    const yes = await confirm({
      title: t("appearance.wallpaper.deleteConfirm", { name: photo.name }),
      confirmLabel: t("appearance.wallpaper.yes"),
      cancelLabel: t("appearance.wallpaper.no"),
      variant: "danger",
      initialFocus: "confirm",
    });
    if (
      !yes ||
      !mounted.current ||
      viewRevision.current !== view ||
      getEnv() !== source ||
      operation.current !== previousOperation ||
      wallpaperChoiceRevision() !== choiceRevision
    )
      return;
    const action = begin(`delete:${photo.name}`);
    try {
      const revision = wallpaperChoiceRevision();
      await deleteGalleryPhoto(photo, new AbortController().signal);
      if (mounted.current && getEnv() === action.source) {
        setPhotos((previous) =>
          previous.filter((item) => item.name !== photo.name),
        );
        void refresh(action.source);
      }
      const next = await clearDeletedWallpaper(
        { scope, name: photo.name },
        revision,
      );
      if (action.current()) setResult(next);
    } catch {
      if (action.current()) setResult({ refusal: "delete" });
    } finally {
      finish(action);
    }
  };

  const refusal = result?.refusal ?? (state.storageError ? "storage" : null);
  const visibleGalleryStatus =
    galleryStatus === "loading" || galleryStatus === "error"
      ? galleryStatus
      : photos.length === 0
        ? "empty"
        : null;
  const wallpaperLabel = t(SETTINGS_TEXTS.appearanceWallpaper.label);
  const uploadLabel = t("appearance.wallpaper.upload");
  const clearLabel = t("appearance.wallpaper.clear");
  const scope = galleryEnvironmentScope();

  return (
    <div
      className="appearance-backdrop-block"
      data-testid="appearance-wallpaper"
      data-settings-anchor={landmarkAnchor("appearance", "wallpaper")}
    >
      <p className="appearance-section-label appearance-wallpaper-heading">
        {wallpaperLabel}
        <FieldHint
          text={t(SETTINGS_TEXTS.appearanceWallpaper.desc)}
          label={wallpaperLabel}
        />
      </p>
      <div className="appearance-wallpaper-actions">
        <button
          type="button"
          className="settings-btn settings-btn-icon"
          title={uploadLabel}
          aria-label={uploadLabel}
          aria-busy={loadingId === "upload"}
          data-testid="appearance-wallpaper-upload"
          onClick={() => input.current?.click()}
        >
          <WallpaperActionIcon />
        </button>
        <button
          type="button"
          className="settings-btn settings-btn-icon"
          title={clearLabel}
          aria-label={clearLabel}
          disabled={!state.wallpaper && loadingId === null}
          data-testid="appearance-wallpaper-remove"
          onClick={() => void removeWallpaper()}
        >
          <WallpaperActionIcon clear />
        </button>
      </div>
      <input
        ref={input}
        type="file"
        accept="image/*"
        hidden
        data-testid="appearance-wallpaper-input"
        onChange={(event) => {
          void upload(event.currentTarget.files?.[0]);
          event.currentTarget.value = "";
        }}
      />
      <div
        className="appearance-wallpaper-gallery"
        data-testid="appearance-wallpaper-grid"
      >
        {photos.map((photo) => {
          const selected =
            state.gallery?.scope === scope && state.gallery.name === photo.name;
          return (
            <div
              className="appearance-wallpaper-tile"
              key={`${scope}/${photo.name}`}
              data-testid={`appearance-wallpaper-tile-${photo.name}`}
            >
              <button
                type="button"
                className={`appearance-wallpaper-thumb${selected ? " is-selected" : ""}${loadingId === photo.name ? " is-loading" : ""}`}
                title={photo.name}
                aria-label={photo.name}
                aria-pressed={selected}
                aria-busy={loadingId === photo.name}
                data-testid={`appearance-wallpaper-thumb-${photo.name}`}
                onClick={() => void pickPhoto(photo)}
              >
                <GalleryThumbnail photo={photo} />
              </button>
              <GalleryDeleteButton photo={photo} onDelete={deletePhoto} />
              <span className="appearance-wallpaper-caption" title={photo.name}>
                {photo.name}
              </span>
            </div>
          );
        })}
      </div>
      {visibleGalleryStatus ? (
        <p
          className="settings-field-desc appearance-backdrop-status"
          role="status"
          data-testid="appearance-wallpaper-gallery-status"
        >
          {t(`appearance.wallpaper.gallery.${visibleGalleryStatus}`)}
        </p>
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
      {refusal ? (
        <p
          className="settings-field-desc appearance-backdrop-status"
          role="status"
          data-testid="appearance-wallpaper-status"
        >
          {t(REFUSAL_KEY[refusal])}
        </p>
      ) : null}
    </div>
  );
}
