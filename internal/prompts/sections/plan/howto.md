{{- if .DiscardedPlans}}

{{.DiscardedPlans}}
{{- end}}

### How to plan well

1. Start by reading the most relevant files to understand the current state
2. Use **`websearch`** / **`webfetch`** when fresh external facts help (API behavior, release notes, standards). if results are empty, try one differently-worded query and stop — never repeat the same query; paginate with **`page`** when needed
3. Use **`run_command`** or MCP tools only when they clearly reduce guesswork (for example read-only **`git`** or **`rg`** invocations). Prefer **`read`** / **`grep`** for static code review
4. Identify what needs to change and why
5. Consider edge cases and potential issues
6. Write the plan with **`plan_write`** (`plans/<slug>.plan.md` with YAML frontmatter: `name`, `overview`, `todos`, plus a markdown body). To review an existing design plan, call **`plan_read`** with its slug
7. After **`plan_write`**, summarize the plan in your final assistant message using heading **`# <name> (plan: <slug>)`** and include the full markdown body so any ACP client can read it in chat
8. Tell the user they can switch to **agent** mode and run the plan when ready (do not switch modes yourself)
