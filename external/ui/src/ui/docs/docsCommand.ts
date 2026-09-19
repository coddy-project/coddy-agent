/**
 * `/docs [page or words]` typed in the composer, the console's help command:
 * in the web UI it opens the documentation reader rather than going to the
 * agent. Returns what follows the command, "" for the command alone, or null
 * when the draft is not that command.
 */
export function parseDocsCommand(text: string): string | null {
  const m = /^\/docs(?:\s+([\s\S]*))?$/.exec(text.trim());
  return m ? (m[1] ?? "").trim() : null;
}

/**
 * Whether `/docs <arg>` opens the page the server resolved it to, or searches:
 * the console's rule (external/cli/docs_modal.go). A reference names a page -
 * it has a `/`, a `#` or a scheme - and so does the page's exact title; one
 * word such as "proxy" is a search even when some page is called that.
 */
export function docsCommandOpensPage(arg: string, resolvedTitle: string): boolean {
  if (/[/#:]/.test(arg)) {
    return true;
  }
  return arg.trim().toLowerCase() === resolvedTitle.trim().toLowerCase();
}
