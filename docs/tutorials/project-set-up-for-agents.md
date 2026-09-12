# A project set up for agents

A repository can carry everything an agent needs to work in it well: the conventions, the workflows, the tool servers and the guard rails. All of it lives in files under the checkout, so it is versioned with the code and arrives with every clone. This is the tree the steps below produce, and what each file does:

```text
my-project/
  AGENTS.md                       # project docs preamble, in every prompt
  .coddy/
    rules/
      go-style.md                 # a rule, attached once a Go file comes into play
    skills/
      release-notes/
        SKILL.md                  # /release-notes, project-local
    mcp.json                      # project MCP servers, held until approved
    hooks.json                    # lifecycle hooks, held until approved
    hooks/
      gofmt.sh                    # the script hooks.json runs
    agents/
      reviewer.md                 # a subagent definition, held until approved
```

1. **`AGENTS.md` at the root.** It is read into every prompt unconditionally, as the project docs preamble ([Project docs preamble](../features/rules.md#project-docs-preamble)), and so is a `DESIGN.md` beside it. Keep it a short map: where things live, how to build and test, what a change must carry. Nested `AGENTS.md` files deeper in the tree are read on demand, the moment a tool enters their folder, and never walked for.

2. **Rules in `.coddy/rules/`.** A `.md` file is a Claude Code rule (`paths` gates it, and it is unconditional without them); a `.mdc` file is a Cursor rule (`description`, `globs`, `alwaysApply`). Both dialects are accepted in every rule folder, and `.coddy/rules` wins over `.agents/rules`, `.cursor/rules`, `.claude/rules` and `.codex/rules` when two files share a name ([Rule file formats](../features/rules.md#rule-file-formats)). A gated rule enters the prompt the first time a matching file is attached or touched by a tool, then sticks for the session.

   Everything in this tutorial is the project talking. What *you* want from every session, in every repository - the language to answer in, the commands you never want run - belongs to you rather than to this checkout: put it in `~/.coddy/AGENTS.md` and `~/.coddy/rules/` instead, where it applies everywhere and nobody else has to agree with it ([Your own instructions and rules](../features/rules.md#your-own-instructions-and-rules)).

   ```markdown
   ---
   description: Go coding standards
   paths:
     - "**/*.go"
   ---

   Write comments in English. Wrap errors with fmt.Errorf("context: %w", err).
   Run `make lint` before you report a change as done.
   ```

   `coddy rules list` prints the catalog with the source folder, the dialect each file was read with, whether it is in every prompt and what activates the others.

3. **A skill in `.coddy/skills/<name>/SKILL.md`.** `${CWD}/.coddy/skills` is the highest-priority entry of the default `skills.dirs`, so a project skill overrides one of the same name from `~/.coddy/skills` or `~/.agents/skills` ([Skills](../features/skills.md)). The directory name is the slash command, `/release-notes` here; writing the file is the last recipe on this page. Run `coddy skills list` inside the project to see it listed for that directory.

4. **MCP servers in `.coddy/mcp.json`** ([MCP servers](../features/mcp.md)). The file has Cursor's shape, one `mcpServers` object keyed by name; a URL-only entry is a streamable HTTP server, and `${CWD}` in `args` is the session workspace.

   ```json
   {
     "mcpServers": {
       "filesystem": {
         "command": "npx",
         "args": ["-y", "@modelcontextprotocol/server-filesystem", "${CWD}"]
       },
       "docs": { "url": "https://mcp.example.com/mcp" }
     }
   }
   ```

   Because the file arrives with the checkout, the repository - not the operator - would be choosing the command a session starts. Under the default `mcp.project_trust: ask` these entries stay cold until approved for that workspace ([Workspace trust for project-local servers](../features/mcp.md#workspace-trust-for-project-local-servers)):

   ```bash
   coddy mcp list                # scope, trust state and the command of every merged server
   coddy mcp trust filesystem    # prints the declaration once more, then records the receipt
   ```

   The receipt in `~/.coddy/mcp-trust.json` is keyed by the workspace and a digest of the declaration, so rewriting the entry asks again; `config.yaml` and `~/.coddy/mcp.json` are yours and are never gated.

5. **Hooks in `.coddy/hooks.json`** ([Hooks](../features/hooks.md)). The file uses Claude Code's shape. This one runs `gofmt` after every edit and tells the model what it changed:

   ```json
   {
     "hooks": {
       "PostToolUse": [
         {
           "matcher": "edit|write|apply_patch",
           "hooks": [{ "type": "command", "command": ".coddy/hooks/gofmt.sh" }]
         }
       ]
     }
   }
   ```

   ```bash
   #!/bin/sh
   # .coddy/hooks/gofmt.sh - runs in the session cwd with the call's JSON on stdin
   path=$(jq -r '.tool_input.path // ""')
   case "$path" in
     *.go)
       if out=$(gofmt -l "$path" 2>&1) && [ -n "$out" ]; then
         gofmt -w "$path"
         printf '{"hookSpecificOutput":{"hookEventName":"PostToolUse","additionalContext":"gofmt reformatted %s"}}' "$path"
       fi
       ;;
   esac
   exit 0
   ```

   Hooks run with your permissions, before any permission prompt, so a file that came with a clone is parsed and listed but runs nothing until it is approved ([Project files and trust](../features/hooks.md#project-files-and-trust)); the first turn that finds it records a notice in the session. Review the commands, then:

   ```bash
   coddy hooks list
   coddy hooks trust .coddy/hooks.json
   ```

6. **A subagent in `.coddy/agents/reviewer.md`** ([Definition files](../features/subagents.md#definition-files)). Only `description` is required; `name` defaults to the file stem. `tools`, `disallowed_tools` and `permission_mode` can only narrow what the parent could do, and the body is the child's role.

   ```markdown
   ---
   name: reviewer
   description: Reviews a diff or a module for defects and reports findings with file paths and lines.
   tools: read, glob, grep, print_tree, run_command
   permission_mode: ask
   max_turns: 20
   ---
   You review code. Read before you judge, cite `path:line` for every finding,
   separate defects from style remarks, and end with a short verdict.
   ```

   Project definitions follow `subagents.project_trust`, with receipts of their own ([Scopes and project trust](../features/subagents.md#scopes-and-project-trust)):

   ```bash
   coddy agents list
   coddy agents trust reviewer
   ```

7. **Approve once per clone, or set a policy.** The three approvals above are per workspace and per file digest, so a fresh clone or an edited file asks again. For a checkout you own, `mcp.project_trust`, `hooks.project_trust` and `subagents.project_trust` set to `allow` in `config.yaml` skip the receipts; `deny` never reads the project files at all. Under `--remote` the approvals belong to the server, where the code would run (next recipe).
