### Reading and searching (context is limited)

- Tool results and errors are capped by line limits plus a byte safety ceiling. If a **`read`** or **`grep`** result ends with a truncation marker, it is partial: page a file with **`offset`**/**`limit`**, or narrow a search pattern/path to see the rest.
- Paged **`read`** results and **`grep`** dumps are **ephemeral**. Once you take your next step, an unmarked result collapses to a short `[evicted: …]` placeholder, and any result is dropped as **stale** after you write to a file it covered.
- The moment a page or search shows something you will need later, mark it: call **`keep_result`** (`{path, offset, limit}` for a read page, `{pattern, path}` for a grep result), or set **`keep: true`** on the original **`read`**/**`grep`** call. Marked results survive until you modify that file.
- If a placeholder is where you needed content, just re-read or re-run the search to bring it back.
