# Reviewer CLIs

Every reviewer is a console code agent run once, non-interactively, on the brief. The templates
live in `scripts/agents.tsv`, the single table the helper and both detection scripts read; this page
explains them. `{bin}` is the detected binary, `{model}` is written into the roster, `{brief}` and
`{out}` are filled in for every run.

Rules every template follows, and why:

- **The brief never travels as an argument** (Koda is the one exception, see below). Linux caps one
  argument at 128 KB (`MAX_ARG_STRLEN`),
  Windows a whole command line at 32 767 characters. A 138 KB brief passed as `"$(cat brief)"`
  fails with `Argument list too long` before the model is called (Cursor Agent and Coddy, both
  checked on 2026-10-03). Templates read the brief from stdin or from a file flag.
- **Stdin is closed unless the template feeds it.** Coddy, Devin and OpenCode read stdin when it is
  not a terminal, and an inherited open pipe hangs them. The helper gives every reviewer `/dev/null`.
- **Read-only.** Each template picks the CLI's own read-only mode (plan, ask, a read-only sandbox, a
  permission mode that approves reads only). The brief's first line forbids tools as well, and by
  default the reviewers run in an empty directory of the run, so even a CLI that auto-approves
  everything has nothing of yours to touch.
- **The answer is stdout.** `> {out}` captures the final message; progress and logs go to stderr,
  which the helper keeps next to the review (`reviews/<name>.err`).

Status: **verified** means a real review ran through the template on the version named;
**unverified** means the flags come from the CLI's `--help` and no signed-in run was possible.

## claude (Claude Code)

```
claude -p --model {model} --output-format text --permission-mode plan < {brief} > {out}
```

- Read-only: `--permission-mode plan`. The workspace trust dialog is skipped in `-p` mode.
- Models: no listing command. Aliases `fable`, `opus`, `sonnet`, `haiku` follow the newest model of
  each family; full ids such as `claude-opus-5-5` work too.
- Verified on 2.1.285: `haiku` reviewed a 3 KB brief in 32 s.
- Recover a verdict from a session that ran out of time: in the run's `work` directory,
  `claude -p --continue "Do not call tools. Write your final review now."`.

## codex (OpenAI Codex CLI)

```
codex exec -m {model} --sandbox read-only --skip-git-repo-check - < {brief} > {out}
```

- `-` reads the prompt from stdin. `--skip-git-repo-check` because the reviewer runs outside a
  repository. Approvals are off in `exec`; the sandbox keeps it read-only.
- Models: `codex debug models` prints the raw catalog as JSON (half a megabyte); `crossreview.py
  models codex` reduces it to the visible slugs. Reasoning effort is not a flag: append
  `-c model_reasoning_effort=high` to the stored command by hand.
- A usage limit is per account: when it is hit, every Codex model fails at once, and so do the
  `codex/*` models inside Coddy, which share the login.
- Verified on 0.157.1 (one-word probe, 16 s).
- Recover: in the `work` directory, `codex exec resume --last "Write your final review now."`.

## coddy (Coddy Agent)

```
coddy --model {model} --mode ask --no-stdin -i {brief} > {out}
```

- `-i` reads the prompt from a file; `--no-stdin` keeps whatever is on stdin out of the prompt.
  Both need Coddy 1.1.62 or newer; an older build reads `-p -i` as the prompt `-i`. If you write a
  `-p` form by hand, put `--model` before `-p`, which takes the next argument as its value.
- Read-only: `--mode ask`.
- Models: `provider/model` ids from `models[].model` of `~/.coddy/config.yaml` (the helper reads
  them). One Coddy reaches several vendors at once: `codex/gpt-5.6-sol` (ChatGPT sign-in),
  `devin/swe-2`, `neuraldeep/qwen3.8-27b`, Anthropic or any OpenAI-compatible endpoint, which makes
  it the most flexible single reviewer CLI. The reasoning level comes from `reasoning_default` in
  the config, not from a flag; for a review, point `--config` at a copy with the level raised.
- Local models: a reply cut off at `max_tokens` arrives as an empty review. Raise `max_tokens`
  (32768 to 60000) in a config copy, prefer the `-noreason` variants, and keep their briefs short.
- Verified on 1.2.52: `codex/gpt-5.6-luna` reviewed a 3 KB brief in 32 s.
- Recover: `coddy sessions list` in the `work` directory gives the session id, then
  `coddy --model {model} --mode ask --session-id <id> -p "Do not call tools. Write your final review now."`.

## cursor (Cursor Agent)

```
agent -p --trust --mode ask --model {model} --output-format text < {brief} > {out}
```

- The binary is tried as `agent` first (it can be a wrapper that carries a proxy), then
  `cursor-agent`, then `cursor agent`. Whichever answered is kept in the roster's `binary` and
  `command`; the command is authoritative.
- `--trust` is required in print mode in a directory the CLI has not seen. Read-only: `--mode ask`.
- Models: `agent --list-models`; `auto` lets Cursor choose.
- Verified on 2026.10.01: on a 138 KB brief through stdin, `--mode ask` and `--mode plan` both
  answered in 58 s; the same brief as an argument failed with `Argument list too long`. The reply
  sometimes ends with a stray `<|eos|>`, and it may follow the language of the user's Cursor rules.
- Recover: in the `work` directory, `agent -p --trust --continue "Write your final review now."`.

## devin (Devin CLI)

```
devin -p --model {model} --prompt-file {brief} --permission-mode auto --respect-workspace-trust false < /dev/null > {out}
```

- `--prompt-file` reads the brief. `--permission-mode auto` approves read-only tools only.
  `--respect-workspace-trust false` is required: print mode cannot show the trust prompt and fails
  in an untrusted directory. `< /dev/null` because Devin reads stdin otherwise.
- Models: `devin models list`. `swe-2-high`, `swe-2-medium` and `swe-2-max` are free.
- With repository access `swe-2-high` can spend a whole hour checking hypotheses and never write
  the answer; from the brief alone it answers in minutes. When `high` stalls, `medium` usually does
  not.
- Verified on 3000.11.3: `swe-2-medium` reviewed a 3 KB brief in 10 s.
- Recover: the session id is in `~/.local/share/devin/cli/logs/` (the line `for session <id>`);
  `devin -r <id> --model swe-2-high -p "Do not call tools. Write your final review now." --permission-mode auto --respect-workspace-trust false < /dev/null`.

## opencode (OpenCode)

```
opencode run -m {model} --agent plan < {brief} > {out}
```

- Without a message argument `opencode run` reads the prompt from stdin. `--agent plan` is the
  read-only agent.
- Always pass `-m`: the configured default may be a model the account cannot use, and the run then
  hangs without output.
- In a checkout that has a `.opencode/` directory, OpenCode installs its plugin dependencies there
  and rewrites `.opencode/package.json`. Another reason to keep reviewers in the empty `work`
  directory.
- Models: `opencode models` (`provider/model`).
- Verified on 1.18.16: `neuraldeep/qwen3.8-27b-noreason` reviewed a 3 KB brief in 32 s.
- Recover: `opencode run --continue "Write your final review now."` in the `work` directory.

## qwen (Qwen Code)

```
qwen -m {model} --approval-mode plan -o text < {brief} > {out}
```

- Stdin is the prompt when no positional prompt is given. `--approval-mode plan` is read-only.
- Needs an auth type configured first (`No auth type is selected` otherwise).
- Unverified (0.10.6 installed, not signed in).

## gemini (Gemini CLI)

```
gemini -m {model} --approval-mode plan -o text < {brief} > {out}
```

- Same flags as Qwen Code, its fork. Unverified; if your version rejects `--approval-mode plan`,
  drop it from the stored command: headless Gemini CLI refuses tools that need approval anyway.

## kimi (Kimi Code CLI)

```
kimi --quiet --plan -m {model} < {brief} > {out}
```

- `--quiet` is `--print --output-format text --final-message-only`. Print mode implies `--yolo`
  (every action approved), so `--plan` and the empty `work` directory are what keep it harmless.
- Stdin is read as the prompt in print mode (checked).
- Unverified (1.30.0 installed, the key on this machine is rejected with 401).

## koda

```
koda "$(cat {brief})" > {out}
```

- Kept from Coddy's original list, and the one exception to the rule above: Koda takes the prompt
  only as an argument. The helper refuses it up front, with the reason in the status, when the brief
  is longer than one argument may be (120 KB here, 30 KB on Windows). There is no model placeholder:
  put the CLI's model flag into the stored command by hand.
- No read-only mode is known, so rely on the brief's tool ban and the empty working directory.
- Unverified.

## Adding an agent

Add a row to `scripts/agents.tsv` (agent, candidate binaries, a word its `--version` or `--help`
prints, the arguments that list its models or `-`, the POSIX template, the PowerShell template),
then a section here, then run the tests. Keep the brief out of argv, close stdin, pick the
read-only mode, and verify with `crossreview.py probe --reviewer <agent>:<model>` before calling it
verified.
