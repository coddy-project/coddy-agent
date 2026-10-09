import { ImageLightbox } from "./ImageLightbox";
import { useApiImageSrc } from "../env/apiImage";

/**
 * An <img> of an image the server named. Through a relay or any remote
 * environment the bytes come through that environment (useApiImageSrc); until
 * they arrive the image has no src rather than a broken one.
 */
export function ApiImage(props: {
  src: string;
  alt: string;
  className: string;
  "data-testid": string;
}) {
  const src = useApiImageSrc(props.src);
  return (
    <img
      className={props.className}
      alt={props.alt}
      data-testid={props["data-testid"]}
      {...(src ? { src } : {})}
    />
  );
}

/** The original of an image the server named, enlarged, read the same way as its thumbnail. */
export function ApiImageLightbox(props: {
  src: string;
  alt: string;
  onClose: () => void;
}) {
  const src = useApiImageSrc(props.src);
  return src ? (
    <ImageLightbox src={src} alt={props.alt} onClose={props.onClose} />
  ) : null;
}
