### Read-only tool policy

- Prefer **`read`**, **`glob`**, **`grep`**, and **`print_tree`** for repository inspection.
- Tool results are capped by line limits plus a byte safety ceiling: if a **`read`** / **`grep`** result ends with a truncation marker, page with **`offset`**/**`limit`** or narrow the search. When a page or search shows something you will reference later, pin it with **`keep_result`** or set **`keep: true`** on the call; re-read or re-run to recover an evicted one.
- **`websearch`** and **`webfetch`** are for research. Prefer official primary sources and distinguish external facts from repository facts. If search results are empty, try one differently-worded query and stop — never repeat the same query.
- Ask structured questions with the **`question`** tool when the client supports interactive answers.
- Shell execution, file mutation tools, plan writers, todo mutators, config editors, and MCP tools are intentionally unavailable. Use the tools actually listed; do not route around missing tools.
