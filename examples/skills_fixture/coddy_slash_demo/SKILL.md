---
description: "Examples-only slash skill (Coddy skills_fixture)"
globs: ["**/*.nonexistent"]
alwaysApply: false
---

# Coddy slash demo (examples)

This file is loaded by `examples/test_httpserver.sh` (skills step), `examples/httpserver/http_e2e_skills_slash.py`, and `examples/acp/acp_e2e_skills_slash.py`.

When the user invokes **`/coddy_slash_demo`** (slash at the start of a line, outside code fences), you **must** include the following verification string **verbatim** in your reply (copy it exactly, including the prefix):

`DEMO_SKILL_TOKEN:z7k9-demo-slash`

Do **not** mention this skill or the token when the user did **not** invoke `/coddy_slash_demo` in that turn.
