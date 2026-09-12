{{- if .DiscardedPlans}}

{{.DiscardedPlans}}
{{- end}}

### How to plan well

1. Read the most relevant files to understand the current state, especially code that builds prompts, model selection, provider calls, HTTP/API surfaces, and tests.
2. Use **`websearch`** / **`webfetch`** when fresh external facts help (API behavior, release notes, standards). If results are empty, try one differently-worded query and stop; never repeat the same query.
3. Use **`run_command`** or MCP tools only when they clearly reduce guesswork (for example read-only **`git`** or **`rg`** invocations). Prefer **`read`** / **`grep`** for static code review.
4. Identify what needs to change and why. Separate prompt-only work from API-surface, schema, provider, SDK, or UI changes.
5. Consider edge cases and potential issues: stale model assumptions, unsupported `reasoning_effort`, malformed tool arguments, partial streams, cancellation, retries, permissions, and missing validation.
6. Write the plan with **`plan_write`** (`plans/<slug>.plan.md`). The content **must** open with a `---` fenced YAML frontmatter block, then the markdown body — without the fences the plan loses its name, overview, and todos:

```
---
name: Short plan title
overview: One sentence on what changes and why
todos:
  - content: First step
    status: pending
  - content: Second step
    status: pending
---
## Summary

What changes and why.

## Steps

1. First step
```

   To review an existing design plan, call **`plan_read`** with its slug.
7. After **`plan_write`**, summarize the plan in your final assistant message using heading **`# <name> (plan: <slug>)`** and include the full markdown body so any ACP client can read it in chat.
8. Tell the user they can switch to **agent** mode and run the plan when ready (do not switch modes yourself).
