# Coddy in CI

A pipeline step that asks the agent one question and reads the answer: a review of a pull request, a summary of a diff, a check that a document still matches the code. The console's print mode, `coddy -p` ([Console](../surfaces/console.md), "One-shot print mode"), runs one turn without a terminal, streams the assistant text to stdout and the diagnostics to stderr, and exits non-zero when something went wrong. The release binary carries the console; a lean build answers `-p` with a rebuild hint.

1. **Install the binary on the runner.** The one-line installer ([Install](../getting-started/install.md)) puts `coddy` into `~/.local/bin` and creates `~/.coddy/config.yaml` from the example file when there is none. Put that directory on `PATH` for the steps that follow.

   ```bash
   curl -fsSL https://coddy.dev/install.sh | bash
   export PATH="$HOME/.local/bin:$PATH"
   coddy -v
   ```

2. **Give the job a home of its own.** `CODDY_HOME` is where the config, the sessions and the logs live (default `~/.coddy`). Point it at a temporary directory, so the transcript of one job never leaks into the next and nothing is left behind when the runner is discarded. The loader reads `$CODDY_HOME/config.yaml` and, before that, `$CODDY_HOME/.env` for variables the shell did not set; `CODDY_CONFIG` names a config file somewhere else, and `--cwd` (or `CODDY_CWD`) sets the workspace of the session, which in a job is the checkout. Every session of the job lands under `$CODDY_HOME/sessions`.

   ```bash
   export CODDY_HOME="$(mktemp -d)"
   ```

3. **Write a config whose key comes from the environment.** A provider's `api_key` may be a `${VAR}` reference, expanded when the file loads, or it may be left empty, in which case the key is read at call time from `NAME_API_KEY` - the provider name in upper case with hyphens turned into underscores, so `openai` reads `OPENAI_API_KEY` and `team-gateway` reads `TEAM_GATEWAY_API_KEY`. Either way the secret stays in the CI secret store and never in a file. Pin the model and the turn cap: `agent.max_turns` is what bounds the cost of one step. With no `config.yaml` at all, `OPENAI_API_KEY` or `ANTHROPIC_API_KEY` alone gives a default provider and model, but a job should pin its own. [Configuration](../getting-started/configuration.md) has every key.

   ```yaml
   providers:
     - name: openai
       type: openai
       api_key: "${OPENAI_API_KEY}"   # or leave it empty: OPENAI_API_KEY is read when the model is called

   models:
     - model: "openai/gpt-5.4-mini"
       max_tokens: 16384

   agent:
     model: "openai/gpt-5.4-mini"
     max_turns: 20
   ```

4. **Gate the config before spending a token.** `coddy -t` checks the file a start would load against the embedded schema and the loader's rules, prints each problem with its line and a `fix:` line, and exits 1 on errors without starting anything ([Checking the file from the command line](../getting-started/configuration.md#checking-the-file-from-the-command-line)). `coddy --dry-run` runs the same check and then asks every provider for its model list, which exercises the address and the key in one request, so a wrong secret fails here on a named line instead of as a 401 in the middle of the review ([Dry run](../getting-started/configuration.md#dry-run-probing-what-the-file-points-at)). Both flags work in every build.

   ```bash
   coddy -t          # the file: schema and loader rules
   coddy --dry-run   # the file, then the providers it names
   ```

5. **Run the prompt.** `-p` takes the prompt. `--mode ask` restricts the turn to the read-only tools (`read`, `glob`, `grep`, `print_tree`, `websearch`, `webfetch`, `question`, `load_skill`: no shell, no file writes, no MCP tools), which is the right shape for a review and means no permission prompt can arise. `--model` picks one of the configured models, `--cwd` the workspace. The answer goes to stdout and everything else to stderr, so redirecting stdout gives a file you can post as a comment.

   ```bash
   git diff origin/main...HEAD > review.patch
   coddy -p "Review the change in review.patch. Read the files it touches for context, report defects with path:line, and end with one line: VERDICT: ok or VERDICT: needs work." --mode ask > review.md
   ```

   A step that should edit files runs in `agent` mode instead, and its permission requests are then answered without a human: `--permission-mode bypass` allows everything, `accept_edits` approves file writes and rejects shell commands, and `ask` (the default) rejects every request with a note on stderr. The question tool gets empty answers. A later step continues the same session with `coddy -c -p "..."`, so a second question sees the files and tool results of the first.

6. **Read the exit status.**

   | Command | Exit 0 | Exit 1 |
   |---|---|---|
   | `coddy -p "..."` | the turn finished and the answer was printed | the config did not load, the provider failed, a flag named an unknown model or mode, or the turn was cancelled |
   | `coddy -t` | the file is valid (warnings do not fail it) | the file has errors or does not exist |
   | `coddy --dry-run` | every probe passed | the file has errors or a probe failed |

   The status of `-p` reports the process, not the verdict: a turn that stopped at `agent.max_turns` still exits 0. Make the prompt end with an explicit line, as above, and let the pipeline grep for it.

7. **Decide what a checkout may execute.** `.coddy/mcp.json`, `.coddy/hooks.json` and `.coddy/agents/` arrive with the repository and are held under the default `ask` policy until an operator approves them, which nobody does on a runner. For a repository you own, pass `--mcp-project-trust allow` (the console, `acp` and `serve` all take it) or set `mcp.project_trust`, `hooks.project_trust` and `subagents.project_trust` to `allow` in the job's config. Leave them at `ask`, or set `deny`, for jobs that run on pull requests from strangers, so a fork cannot choose what the runner executes. [MCP servers](../features/mcp.md), [Hooks](../features/hooks.md) and [Subagents](../features/subagents.md) describe the three gates.

Put together as a GitHub Actions job:

```yaml
name: Review
on: [pull_request]

jobs:
  review:
    runs-on: ubuntu-latest
    env:
      CODDY_HOME: ${{ runner.temp }}/coddy
      OPENAI_API_KEY: ${{ secrets.OPENAI_API_KEY }}
    steps:
      - uses: actions/checkout@v4
        with:
          fetch-depth: 0

      - name: Install Coddy
        run: |
          curl -fsSL https://coddy.dev/install.sh | bash
          echo "$HOME/.local/bin" >> "$GITHUB_PATH"

      - name: Write the config
        run: |
          mkdir -p "$CODDY_HOME"
          cat > "$CODDY_HOME/config.yaml" <<'EOF'
          providers:
            - name: openai
              type: openai
              api_key: "${OPENAI_API_KEY}"
          models:
            - model: "openai/gpt-5.4-mini"
              max_tokens: 16384
          agent:
            model: "openai/gpt-5.4-mini"
            max_turns: 20
          EOF

      - name: Check the config and the provider
        run: coddy --dry-run

      - name: Review the change
        run: |
          git diff origin/main...HEAD > review.patch
          coddy -p "Review the change in review.patch. Read the files it touches for context, report defects with path:line, and end with one line: VERDICT: ok or VERDICT: needs work." --mode ask | tee review.md
          grep -q 'VERDICT: ok' review.md
```
