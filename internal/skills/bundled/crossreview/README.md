# crossreview

An agent skill for **cross-review**: the agent you are working with sends one review brief to several
other console code agents at once, Claude Code, Codex, Coddy, Cursor Agent, Devin, OpenCode and
others, each on the model you picked, collects every answer, checks each finding against the code
and decides what is worth fixing. One model reviewing its own work misses what a different model
from a different vendor notices; this skill makes asking four of them as cheap as asking one.

It is a portable skill: install it in Claude Code, Codex, Cursor, OpenCode, Coddy, Devin, Kimi or
any agent that reads `SKILL.md`, on Linux, macOS or Windows.

## How a review goes

```
                 brief.md (the diff, the intent, the answer format)
                            |
     +-------------+--------+------+-------------+--------------+
     v             v               v             v              v
  claude -p    codex exec     agent -p       coddy -i      devin -p      ...  each alone, read-only,
  (sonnet)     (gpt-5.6-sol)  (auto)         (devin/swe-2) (swe-2-high)       blind to the others
     |             |               |             |              |
     +-------------+--------+------+-------------+--------------+
                            v
          the orchestrator (your agent): collects every answer,
          verifies each finding in the code, decides: fix / not worth fixing / rejected / open
```

You invoke the skill, and the agent you talk to asks what it must (which reviewers, what the change is
for), writes the brief and hands the rest to a subagent: Coddy's built-in `crossreview` coordinator, a
`general-purpose` agent in Claude Code, a task in OpenCode. The subagent runs the reviewers, collects
every answer, checks the findings and decides, and its report comes back as the answer, so none of
that work fills your conversation. An agent without subagents does the same steps itself.

Three rules hold for every run:

- **Reviewers are blind to each other.** Each one gets the brief and nothing else, in every round,
  so they do not anchor on each other's opinions;
- **Every reviewer gets to answer.** The run waits for all of them; one that failed for a passing
  reason is retried before the report is written;
- **The orchestrator has the last word.** Findings are not counted as votes: the agent driving the
  review reads the code at every `path:line`, rejects what it can disprove, and decides what to fix
  now and what is not worth fixing.

## Works best with Coddy Agent

The skill was born in [Coddy Agent](https://github.com/coddy-project/coddy-agent) and it is at its best
there:

- **Inside Coddy** the skill hands the fan-out to Coddy's built-in
  [`crossreview` coordinator subagent](https://coddy.dev/docs/features/subagents#built-ins). It runs the
  reviewer CLIs as background tasks in its own child session, so your conversation stays clean, adds
  internal reviewers on any model Coddy is signed into, and returns one verified report;
- **From any other agent**, `coddy` is the most flexible single reviewer you can add: one command shape
  reaches OpenAI Codex models through a ChatGPT sign-in, Devin SWE-2, Anthropic, NeuralDeep and any
  OpenAI-compatible endpoint, for example `coddy:codex/gpt-5.6-sol`, `coddy:devin/swe-2` or
  `coddy:neuraldeep/qwen3.8-27b`. See [coddy.dev](https://coddy.dev) to install it.

Coddy ships this skill in its standard delivery, so `/crossreview` is there on the first run.

## Install

The skill is distributed through the [rpa-skills](https://github.com/EvilFreelancer/rpa-skills)
catalog, a plugin marketplace for Claude Code and Codex, and installs as a plain skill folder
everywhere else.

**Claude Code, as a plugin:**

```text
/plugin marketplace add EvilFreelancer/rpa-skills
/plugin install crossreview@rpa-skills
```

**Codex, as a plugin:**

```bash
codex plugin marketplace add EvilFreelancer/rpa-skills
```

**Any agent, as a skill folder.** Clone the repository once and link it into every agent you use;
the directory name has to match the `name` field in `SKILL.md`:

```bash
git clone https://github.com/EvilFreelancer/crossreview.git ~/.local/share/crossreview
```

```bash
for d in ~/.claude/skills ~/.codex/skills ~/.cursor/skills ~/.agents/skills ~/.config/opencode/skills ~/.config/devin/skills ~/.kimi/skills; do
  mkdir -p "$d" && ln -sfn ~/.local/share/crossreview "$d/crossreview"
done
```

On Windows, copy the folder instead (or use `mklink /J`), for example into
`%USERPROFILE%\.claude\skills\crossreview` and `%USERPROFILE%\.cursor\skills\crossreview`.

**Requirements.** Python 3.8 or newer for the helper script, standard library only. Without Python the
skill still works through `scripts/detect-agents.sh` (or `detect-agents.ps1` on Windows) and the
agent's own background shell, as `SKILL.md` describes. And at least two of the reviewer CLIs below,
signed in.

## Quick start

Ask your agent in plain words, or with the slash command:

```text
/crossreview
/crossreview the branch against main
/crossreview docs/plan.md with cursor auto and coddy on codex/gpt-5.6-sol
/crossreview:setup
```

The first run detects the installed reviewer CLIs, asks which agents and which of their models to use,
and whether to keep that roster for this project or for all of your projects. A local roster goes into
the agent's folder of the repository (`.claude/crossreview.json`, `.cursor/crossreview.json`, ...) and can
be committed for the team; a global one into the agent's home (`~/.claude/crossreview.json`,
`~/.coddy/crossreview.json`, ...). Then it smoke-tests every reviewer with a one-word brief.
`/crossreview:setup` runs the same questions again to change it, and reviewers named in the request are
used once, without touching any roster.

The agent reports a verdict, who answered, the findings it confirmed and decided to fix, the ones not
worth fixing, the ones it rejected with the evidence, and whether the quorum was met.

## Reviewer CLIs

| Agent | Command the skill runs (read-only) | Checked on |
|-------|------------------------------------|--------|
| Claude Code | `claude -p --model M --permission-mode plan < brief` | a real review, 2.1.285 |
| Codex | `codex exec -m M --sandbox read-only - < brief` | one-word probe, 0.157.1 |
| Coddy | `coddy --model M --mode ask --no-stdin -i brief` | a real review, 1.2.52 |
| Cursor Agent | `agent -p --trust --mode ask --model M < brief` | a real review, 2026.10.01 |
| Devin | `devin -p --model M --prompt-file brief --permission-mode auto` | a real review, 3000.11.3 |
| OpenCode | `opencode run -m M --agent plan < brief` | a real review, 1.18.16 |
| Qwen Code | `qwen -m M --approval-mode plan -o text < brief` | flags from `--help` |
| Gemini CLI | `gemini -m M --approval-mode plan -o text < brief` | flags from `--help` of its fork |
| Kimi | `kimi --quiet --plan -m M < brief` | flags from `--help` |
| Koda | `koda "<brief>"` | kept from Coddy's original list |

The brief never travels as a command-line argument (a 138 KB brief that way fails with
`Argument list too long` before the model is even called; Koda, which takes nothing else, gets a
brief only while it fits), stdin is closed for every reviewer, and
each one runs in its CLI's read-only mode inside an empty directory. [references/agents.md](references/agents.md)
has the details of every CLI, its pitfalls and how to recover an answer from a reviewer that timed out.

## The helper

`scripts/crossreview.py` does the mechanical part the same way on Linux, macOS and Windows. The agent
drives it; you can too:

```bash
python3 scripts/crossreview.py detect                      # installed reviewer CLIs
python3 scripts/crossreview.py models cursor               # the models one of them offers
python3 scripts/crossreview.py init --host claude --scope global cursor:auto coddy:codex/gpt-5.6-sol
python3 scripts/crossreview.py probe --host claude         # does every reviewer answer?
python3 scripts/crossreview.py brief --out /tmp/brief.md --base main --intent "What the branch is for"
python3 scripts/crossreview.py run --host claude --brief /tmp/brief.md   # all of them, in the background
python3 scripts/crossreview.py wait <run> --max 240        # exit 3 while still running
python3 scripts/crossreview.py collect <run>               # every review, every failure reason
python3 scripts/crossreview.py retry <run> devin-swe-2-high --timeout 3600
```

Each reviewer runs as its own process group, detached from the shell that started the run, with its
own time limit; the status tells a review from an empty answer, a failure (with the likely reason:
not signed in, usage limit, unknown model, context too long), a timeout and a missing binary.

## Safety

- The roster is executable configuration. The global one is yours. A project's roster that you did not
  write with setup (a teammate's, one that came with a clone) runs only after you approve that exact
  file, and any change to it asks again;
- Reviewers get read-only modes, a brief that forbids tools, and an empty working directory, so even a
  CLI that auto-approves everything in print mode has nothing of yours to touch;
- A reviewer that has the skill installed itself will not start a crossreview of its own: the helper
  marks every reviewer process with `CROSSREVIEW_DEPTH` and refuses to fan out again inside one.

## Tests

```bash
python3 -m unittest discover -s tests -v
```

No reviewer CLI is called: agents are stub executables on a private `PATH`. GitHub Actions runs the
suite on Linux, macOS and Windows, the PowerShell twin under Windows PowerShell 5.1 and PowerShell 7,
and a check that the PowerShell templates move the brief and the review byte for byte.

## Source and attribution

Ported from the `crossreview` skill and coordinator subagent of
[Coddy Agent](https://github.com/coddy-project/coddy-agent), made agent-neutral. Part of
[rpa-skills](https://github.com/EvilFreelancer/rpa-skills), [Pavel Rykov](https://t.me/evilfreelancer)'s
agent-skills collection.

Licensed under the MIT License, see [LICENSE](LICENSE).
