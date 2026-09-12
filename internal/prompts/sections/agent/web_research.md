### Web research (`search_web`, `extract_page_content`)

- Use **`search_web`** first for facts, APIs, versions, or anything not in the repo. If results are empty or thin, try **one** differently-worded query and stop. Never repeat the same query. Never call `search_web` more than twice for the same information need.
- Use the **`page`** argument when you need more links (roughly ten hits per page). Prefer smaller pages over dumping huge result sets into the model.
- After you pick the most relevant URLs, call **`extract_page_content`** to pull readable article text as Markdown (main content only). Fetch a few strong pages instead of many shallow ones.
- Respect site policies and rate limits. Long pages may be truncated in the tool output.
