# The reviewer roster

The roster is a JSON file listing the reviewers. `crossreview.py init` writes it, and it can be
edited by hand: it is ordinary configuration, read fresh on every run. Each agent has its own, for
one project or for all of them (below).

```json
{
  "version": 1,
  "min_reviewers": 2,
  "timeout": 2700,
  "reviewers": [
    {"kind": "cli", "agent": "cursor", "binary": "agent", "model": "auto",
     "command": "agent -p --trust --mode ask --model auto --output-format text < {brief} > {out}"},
    {"kind": "cli", "agent": "coddy", "binary": "coddy", "model": "codex/gpt-5.6-sol",
     "command": "coddy --model codex/gpt-5.6-sol --mode ask --no-stdin -i {brief} > {out}",
     "timeout": 2400},
    {"kind": "cli", "agent": "devin", "binary": "devin", "model": "swe-2-high",
     "command": "devin -p --model swe-2-high --prompt-file {brief} --permission-mode auto --respect-workspace-trust false < /dev/null > {out}",
     "enabled": false},
    {"kind": "internal", "host": "claude", "model": "sonnet"},
    {"kind": "internal", "host": "coddy", "definition": "explore", "model": "devin/swe-2", "reasoning": "high"}
  ]
}
```

| Field | Meaning |
|-------|---------|
| `version` | Always `1`. |
| `min_reviewers` | The quorum: fewer answers than this and the report says **insufficient quorum**. Default 2. |
| `timeout` | Seconds each reviewer gets, default 2700. A reviewer's own `timeout` wins. |
| `reviewers[].kind` | `cli` (an external CLI started by the helper) or `internal` (a subagent of a host agent). Default `cli`. |
| `reviewers[].name` | Label in the status and the report, also the file name of the review. Default `<agent>-<model>`. Reduced to letters, digits, `.`, `_` and `-`. |
| `reviewers[].agent` | Row of `scripts/agents.tsv` the entry came from; used for warnings and `init --refresh`. |
| `reviewers[].binary` | The binary detection found (`agent`, `cursor-agent`, a path). Informational: the command is what runs. |
| `reviewers[].model` | The model written into the command; a `{model}` left in `command` is filled with it. Letters, digits and `._:/@+=,[]-` only. |
| `reviewers[].command` | What runs, with `{brief}` and `{out}` left as placeholders. Authoritative: it runs exactly as written, so a wrapper, a proxy alias or an extra flag (`-c model_reasoning_effort=high`) belongs here. |
| `reviewers[].enabled` | `false` keeps the entry without running it. |
| `reviewers[].host` | For `internal`: the agent that runs it as its own subagent (`claude`, `coddy`, `opencode`). Other hosts skip it; Coddy's when it is missing. |
| `reviewers[].definition`, `reasoning` | For Coddy's internal reviewers: the subagent definition (only `explore` is honoured) and the reasoning level. |

The helper runs a command without a shell when it has the template shape: arguments, then an
optional `< file` and `> {out}`, or the PowerShell form `Get-Content -Raw {brief} | ...`. Anything
else (pipes, `;`, `&&`, `2>`) goes to `sh -c`, or to PowerShell on Windows, with the paths quoted.

## Where it lives

Each agent keeps its own rosters, where it keeps its other settings, and `/crossreview:setup` asks
which of the two to write:

- **local**: `<project>/<agent folder>/crossreview.json`, for this project only. Commit it and the
  team reviews with the same set;
- **global**: `<agent home>/crossreview.json`, for every project of yours.

| Agent (`--host`) | Global | Local |
|------------------|--------|-------|
| `claude` | `${CLAUDE_CONFIG_DIR:-~/.claude}/crossreview.json` | `.claude/crossreview.json` |
| `codex` | `${CODEX_HOME:-~/.codex}/crossreview.json` | `.codex/crossreview.json` |
| `coddy` | `${CODDY_HOME:-~/.coddy}/crossreview.json` | `.coddy/crossreview.json` |
| `cursor` | `~/.cursor/crossreview.json` | `.cursor/crossreview.json` |
| `opencode` | `${XDG_CONFIG_HOME:-~/.config}/opencode/crossreview.json` | `.opencode/crossreview.json` |
| `devin` | `${XDG_CONFIG_HOME:-~/.config}/devin/crossreview.json` | `.devin/crossreview.json` |
| `gemini`, `qwen`, `kimi` | `~/.gemini/...`, `~/.qwen/...`, `~/.kimi/...` | `.gemini/...`, `.qwen/...`, `.kimi/...` |
| anything else | `~/.agents/crossreview.json` | `.agents/crossreview.json` |

`~` is the user's home, `%USERPROFILE%` on Windows. The Coddy rows are where the first version of
this skill, which lived inside Coddy, kept its rosters, so they keep working as they are.
`$CROSSREVIEW_HOME` stands in for the home folder of every agent at once, for one set shared by
all of them.

The helper takes the first of these that exists:

1. `--roster FILE` on the command line;
2. `$CROSSREVIEW_ROSTER`;
3. the local roster of the project (the git checkout of the current directory, else the directory
   itself);
4. the global roster.

`--scope local` or `--scope global` narrows `roster`, `run` and `probe` to one of the two.

## A project's roster and its approval

A local roster can arrive with a clone, and it runs commands, so opening a repository must not be
enough to run them. One that `init --scope local` wrote is approved as it is written, because the
user asked for it. Any other - a teammate's committed file, a fresh clone, a file changed since -
is used only after the user approves that exact file: `crossreview.py roster` exits 4 and prints
the commands and the file's SHA-256, the agent shows them to the user, and on a clear yes runs the
`crossreview.py trust ... --sha256 <digest>` line it printed, which refuses when the file no longer
has the digest that was shown. The approval is recorded in the agent's home, in
`crossreview-trust.json`:

```json
{"workspace": "/abs/repo", "roster": "/abs/repo/.claude/crossreview.json", "sha256": "...", "approved": "2026-10-03T01:20:00"}
```

Any change to the file changes its digest and asks again. A roster named with `--roster` or
`$CROSSREVIEW_ROSTER` that lies inside the current git checkout is treated as a project's roster
too: it came with the clone however it was named. The global roster needs no approval: it is in
the user's own home.
