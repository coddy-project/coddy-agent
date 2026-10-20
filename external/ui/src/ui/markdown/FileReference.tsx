import {
  createContext,
  useContext,
  type MouseEvent,
  type ReactNode,
} from "react";

import { relativeFilePath } from "../files/api";
import { openWorkspaceFile } from "../files/fileBus";
import {
  useWorkspaceEntryKind,
  useWorkspaceFilesScope,
} from "../files/workspaceEntries";
import { useT } from "../i18n/I18nProvider";
import { sessionFilesHash } from "../scheduler/hashRoute";
import { MarkdownStreamingContext } from "./DiagramBlock";
import type { FileReference } from "./fileReferences";

/**
 * Set by every link the Markdown draws, around its text: a path in code inside
 * a link is the link's label, and the link alone decides where it goes. Read
 * as a link of its own it nested one anchor in another, opened a file twice,
 * and took an address of the web for a workspace file.
 */
export const InsideLinkContext = createContext(false);

/** A file of the chat's workspace a reference resolved to. */
export interface FileTarget {
  path: string;
  line: number | undefined;
}

/**
 * The workspace file a path of an answer names, once the server has said it
 * is a file there; null until then, for anything else, while the answer is
 * still arriving (its paths are not finished yet) and outside a chat.
 */
export function useFileTarget(ref: FileReference | null): FileTarget | null {
  const scope = useWorkspaceFilesScope();
  const streaming = useContext(MarkdownStreamingContext);
  const rel =
    scope && ref ? relativeFilePath(ref.path, "", scope.workspacePath) : null;
  const usable = typeof rel === "string" && rel !== "";
  const kind = useWorkspaceEntryKind(
    scope,
    usable ? rel : null,
    usable && !streaming,
  );
  return usable && kind === "file" ? { path: rel, line: ref?.line } : null;
}

/**
 * A link that opens a workspace file in a tab of the Files window. Its address
 * is the window's own (`#/s/<id>/files?path=&line=`), so a middle click or
 * "open in a new tab" opens the file there too.
 */
export function FileLink(props: {
  target: FileTarget;
  className?: string;
  children?: ReactNode;
}) {
  const { t } = useT();
  const scope = useWorkspaceFilesScope();
  const { path, line } = props.target;
  const open = (event: MouseEvent<HTMLAnchorElement>) => {
    if (
      event.button !== 0 ||
      event.metaKey ||
      event.ctrlKey ||
      event.shiftKey ||
      event.altKey
    )
      return;
    event.preventDefault();
    openWorkspaceFile(path, line);
  };
  return (
    <a
      href={sessionFilesHash(scope?.sessionId ?? "", path, line ?? 1)}
      className={["md-file-link", props.className].filter(Boolean).join(" ")}
      title={t("markdown.openFile", { path })}
      data-testid="md-file-link"
      onClick={open}
    >
      {props.children}
    </a>
  );
}

/**
 * A reference drawn as a link once it names a workspace file, and as
 * `fallback` (what the text was) until then or otherwise.
 */
export function FileReferenceLink(props: {
  reference: FileReference | null;
  fallback: ReactNode;
  children?: ReactNode;
}) {
  const target = useFileTarget(props.reference);
  if (!target) return <>{props.fallback}</>;
  return <FileLink target={target}>{props.children}</FileLink>;
}
