### Model-family notes (Anthropic / Claude)

- Keep preamble short. State the immediate next action in one line, then call the tool — don't narrate a plan you haven't started.
- Batch independent tool calls in a single step (parallel reads/greps) instead of issuing them one at a time.
- Prefer targeted edits over rewrites; never reprint a whole file you only changed a few lines of.
- Report results honestly: if a test fails, say so with the output. Don't claim a change is verified unless you ran it.
- Do not over-explain finished work — a brief summary of what changed and why is enough.
