import { useCallback, useState } from "react";
import { Markdown } from "../markdown/Markdown";
import { useT } from "../i18n/I18nProvider";
import { relativeFilePath } from "./api";
import { FileImage } from "./FileImage";
import { openWorkspaceFile } from "./fileBus";

function WorkspaceMarkdownImage(props: {
  sessionId: string;
  base: string;
  src?: string | undefined;
  alt?: string | undefined;
  version: string;
}) {
  const { t } = useT();
  const [externalAllowed, setExternalAllowed] = useState(false);
  const src = props.src || "";
  if (/^https?:\/\/|^\/\//i.test(src))
    return externalAllowed ? (
      <img src={src} alt={props.alt || ""} referrerPolicy="no-referrer" />
    ) : (
      <button
        type="button"
        className="files-action"
        onClick={() => setExternalAllowed(true)}
      >
        {t("files.loadExternalImage")}
      </button>
    );
  if (/^[a-z][a-z0-9+.-]*:/i.test(src))
    return <span>{t("files.imageUnavailable")}</span>;
  let decoded = src;
  try {
    decoded = decodeURIComponent(src.split(/[?#]/)[0] || "");
  } catch {
    /* literal file name */
  }
  const path = relativeFilePath(decoded, props.base);
  return path ? (
    <FileImage
      sessionId={props.sessionId}
      path={path}
      alt={props.alt}
      version={props.version}
    />
  ) : (
    <span>{t("files.imageUnavailable")}</span>
  );
}

export function FileMarkdown(props: {
  sessionId: string;
  path: string;
  text: string;
  version?: string;
}) {
  const image = useCallback(
    (p: { src?: string | undefined; alt?: string | undefined }) => (
      <WorkspaceMarkdownImage
        key={p.src}
        {...p}
        sessionId={props.sessionId}
        base={props.path}
        version={props.version || ""}
      />
    ),
    [props.sessionId, props.path, props.version],
  );
  const link = useCallback(
    (href: string) => {
      if (/^[a-z][a-z0-9+.-]*:|^\/\/|^#/i.test(href)) return false;
      let decoded = href.split(/[?#]/)[0] || "";
      try {
        decoded = decodeURIComponent(decoded);
      } catch {
        /* literal path */
      }
      const path = relativeFilePath(decoded, props.path);
      if (!path) return true;
      const fragment = href.split("#")[1] || "";
      const line = /^L(\d+)/i.exec(fragment);
      openWorkspaceFile(path, line ? Number(line[1]) : undefined);
      return true;
    },
    [props.path],
  );
  return <Markdown text={props.text} renderImage={image} onLink={link} />;
}
