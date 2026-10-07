---
name: crossreview
description: >
  Run when the user invokes /crossreview (or /crossreview:setup to choose the reviewers) or asks
  for a cross-review, a quorum review or a second opinion from other agents or models: fan a code
  review (uncommitted work, a branch, a commit range, files, a plan or a document) out to several
  console code agents (Claude Code, Codex, Coddy, Cursor Agent, Devin, OpenCode, Gemini CLI, Qwen
  Code, Kimi, Koda) that review it in parallel and blind to each other, then, as the orchestrator,
  verify every finding against the code and decide alone what matters and what to fix. Works from
  any agent with a shell on Linux, macOS and Windows; the first run detects the installed CLIs,
  asks which agents and which of their models to use, and keeps the reviewer roster either for
  the project (in the agent's folder of the repository) or for all projects (in the agent's home).
metadata:
  version: 2.0.0
  author: Pavel Rykov <paul@drteam.rocks>
  homepage: https://github.com/EvilFreelancer/crossreview
---

# Crossreview: independent reviewers, one orchestrator

One brief is written, every reviewer gets that brief and nothing else, they all answer, and then
the **orchestrator** alone decides: which findings are real, which matter, what to fix now and what
not to fix. The reviewers advise; the last word is the orchestrator's: the subagent you hand the
run to in step 3, or you. Their value is in the disagreement of different models, so pick
reviewers from different vendors.

Three rules hold for the whole run:

1. **Reviewers are blind to each other.** A reviewer never sees another reviewer's answer: not in
   its brief, not in a later round, not as a summary. Internal reviewers (your own subagents)
   included.
2. **Every reviewer gets to answer.** Wait until each one has answered or ended (failed, timed
   out). Retry the ones that failed for a passing reason before you write the report. Stop one
   early only when the user says so.
3. **You decide, not the majority.** One reviewer with evidence outweighs four without it. Your
   verdict may differ from every reviewer's.

`<skill>` below is the directory holding this SKILL.md; agents usually say where it is when they
load a skill (Claude Code's "Base directory", Coddy's "Skill directory"). If yours did not, take
the first of these that holds `scripts/crossreview.py`, and do not search the whole disk:
`${CODDY_HOME:-~/.coddy}/skills/crossreview`, then `crossreview` under `~/.claude/skills`,
`~/.codex/skills`, `~/.cursor/skills`, `~/.agents/skills`, `~/.config/opencode/skills`,
`~/.config/devin/skills`, and under the workspace's `.claude/skills`, `.agents/skills`,
`.cursor/skills`, `.opencode/skills`, `.coddy/skills`.

The helper is `python3 <skill>/scripts/crossreview.py` (`py -3` or `python` on Windows), standard
library only, the same on Linux, macOS and Windows; every subcommand has `--help`. Without Python,
see the last section.

**Who you are.** Every helper command that reads or writes a roster takes `--host <you>`:
`claude` (Claude Code), `codex`, `coddy`, `cursor`, `opencode`, `devin`, `gemini`, `qwen`, `kimi`,
or your own name. The rosters are yours, kept where you keep your settings: the global one in your
home folder (`~/.claude/crossreview.json`, `${CODDY_HOME:-~/.coddy}/crossreview.json`,
`${CODEX_HOME:-~/.codex}/...`, `~/.cursor/...`), the local one in your folder of the project
(`.claude/crossreview.json`, `.coddy/...`, `.codex/...`, `.cursor/...`). An agent not on the list
uses `~/.agents` and `.agents`. Claude Code is recognised without the flag.

**Requirements.** A shell and permission to write files: in a read-only mode (plan, ask) say so
and stop. Reviewers call their model APIs and write their own session files, so a sandbox that
blocks the network or writes outside the workspace (Codex's default) has to let the helper's
commands out, or every reviewer fails.

**Never recurse.** If `CROSSREVIEW_DEPTH` is set in your environment, you are a reviewer: answer
the brief directly and do not start another crossreview.

## 1. Reviewers

Reviewers named in the request ("crossreview with cursor auto and coddy on codex/gpt-5.6-sol")
are a one-off list: pass each as `--reviewer agent:model` to the run and leave the roster alone.

Otherwise run `crossreview.py roster --host <you>`: it takes your local roster of this project when
there is one, else your global one, and you act on its exit code:

- **0**: use it. Relay its warnings (a brief passed as an argument breaks past 128 KB) and offer
  `crossreview.py init --host <you> --scope <the same> --import <path> --refresh --force` to move
  the entries to the current templates.
- **4**: the local roster is not approved: it may have come with the clone, or it changed since,
  and it would run the commands it prints. Show them to the user and ask; only after a clear yes run
  the `crossreview.py trust ...` line it printed. On a no, use `--scope global` from then on.
- **2**: no roster. Set one up.

Setup, also whenever the user invokes `/crossreview:setup`, even if a roster exists:

1. `crossreview.py detect` prints one line per installed reviewer CLI. Offer only those.
2. Ask which agents to use, multiple choice: with your question tool if you have one, otherwise in
   plain text, then end the turn and wait. Never choose for the user.
3. For each chosen agent run `crossreview.py models <agent>` and offer the ids it prints
   (multiple choice); when it prints none, ask for the id as free text. Each (agent, model) pair
   is one reviewer.
4. Ask where to keep the roster: **local**, for this project only, in your folder of it (commit
   the file to give the team the same reviewers; each teammate approves it once), or **global**,
   for all of your projects, in your home folder.
5. `crossreview.py init --host <you> --scope local|global <agent:model>...` writes it (`--force`
   over an existing one, `--add` to extend it, `--import <file>` to start from the reviewers
   another agent already has). `--min-reviewers N` raises the quorum, `--timeout S` the time each
   reviewer gets. A local roster written this way counts as approved.
6. `crossreview.py probe --host <you>` sends every reviewer a one-word brief. Report each failure
   with the reason it gives (not signed in, usage limit, unknown model) and offer to drop or fix
   the entry.

An internal reviewer is a subagent of a host agent on another model, written
`internal/<host>:<model>` (`internal/claude:sonnet`, `internal/coddy:devin/swe-2`). Only the host
it names runs it, read-only; other hosts skip it.

## 2. Brief

Take the scope from the request: the uncommitted work by default, or a branch against its base, a
commit range, the staged changes, whole files, a plan or a document.

```
crossreview.py brief --out <tmp>/brief.md --intent "<what the change is meant to do>" \
    [--base main | --range A..B | --staged | --files F... | --doc PLAN.md] [--exclude PATHSPEC...]
```

- Write `--intent` from the conversation: a reviewer judges a change by what it was meant to do.
- The brief carries the whole diff, and its first line forbids tools: reviewers answer from the
  brief alone, which is fast and keeps them away from your files. When correctness depends on code
  outside the diff, write the brief with `--tools read` and give `run` a `--cwd`: a detached
  worktree (`git worktree add --detach`), never the live checkout.
- Leave out lockfiles, generated code and snapshots with `--exclude`. Past 150 KB split the review
  by layer (backend, frontend, tests): small local models stall on long briefs.
- `--notes-file` adds your own notes for every reviewer alike: facts about the code, decisions you
  already took in an earlier round. Write them yourself; never paste or paraphrase a reviewer's
  answer and never say who raised what.

## 3. Hand the run to the orchestrator

Steps 1 and 2 were yours: the setup questions and the intent of the brief need the conversation.
Steps 4 to 6 are the orchestrator's, and whenever your host can start a subagent they run in one,
so the reviews, the checking of the code and the decisions stay out of your context. Do not run
`crossreview.py run` yourself in that case:

- **Coddy**: its built-in subagent `crossreview` is the orchestrator. It is hidden, so it is not
  among the agents you see listed, but `spawn_agent` accepts it: call it with
  `agent: "crossreview"` in the foreground and a prompt naming the brief file, the roster file and
  `<skill>`.
- **Claude Code**: the Task tool (called Agent in some versions) with the `general-purpose` agent.
  **OpenCode**: the task tool. Any other host whose subagents have a shell: the same. Start it in
  the foreground with a prompt that says it is the crossreview orchestrator, that it reads
  `<skill>/SKILL.md` and carries out steps 4 to 6, and that names the brief file, `--host <you>`,
  and the roster file or the one-off `--reviewer` specs.

The roster file is the path `crossreview.py roster --host <you>` printed; for one-off reviewers in
Coddy, write one with `crossreview.py init --host coddy --path <tmp>/roster.json agent:model ...`.
The subagent's report is the final word: relay it to the user as it is, and you are done. Carry
out steps 4 to 6 yourself only when your host has no subagents, when the roster holds internal
reviewers of yours that your subagents could not start, or when you are that subagent.

## 4. Run and collect every answer

```
crossreview.py run --host <you> --brief <tmp>/brief.md [--reviewer agent:model ...] [--scope global]
crossreview.py wait <run> --max 240
```

`run` prints every exact command, then starts them in the background. Repeat `wait` while it exits
3. Keep `--max` under your shell tool's time limit; the reviewers run detached, so a cut-off wait
loses nothing. Each reviewer has a time limit (45 minutes unless the roster says otherwise).

When the run has finished, look at every reviewer that did not answer:

- failed for a passing reason (network, rate limit, a stream reset): `crossreview.py retry <run>
  <name>`, then `wait` again;
- timed out: retry with a longer `--timeout`, or recover the answer from its session with the
  resume command of that CLI in `references/agents.md`;
- not signed in, out of quota, unknown model: tell the user, it will not answer this time.

`run` marks the internal reviewers that belong to you as `host`: start one read-only subagent
per entry on its model while the CLIs run, with the brief file as its whole prompt, and write each
answer to the path the status names.

## 5. Decide

`crossreview.py collect <run>` prints every answer, and the error of every reviewer that failed.

1. **Split into findings.** One finding per problem, with every reviewer that raised it.
2. **Verify each one yourself.** Read the code at `path:line`, follow the callers, reproduce it
   with a test or a command when you can. Reviewers working from a diff take a removed line for
   current code and miss guards elsewhere.
3. **Decide each one.** Exactly one of:
   - **fix**: a real defect worth fixing now;
   - **not worth fixing**: real, but too minor, out of scope or costlier than it is worth; say why;
   - **rejected**: not a defect; give the line or the run that disproves it;
   - **open**: you could not settle it; say what would.
4. **Set the severity yourself** after verification, not by the loudest reviewer or the count.

## 6. Report

1. `## Verdict`: approve, approve with changes or needs rework, and one sentence why. Yours.
2. `## Reviewers`: who answered (agent, model, time), who did not and why, retries included.
3. `## Fix`: by severity, each with `path:line`, the problem, the fix, who raised it, how you
   verified it.
4. `## Not worth fixing`, `## Rejected`, `## Open`: briefly, each with its reason or evidence.
5. `## Quorum`: answered N of M. Under `min_reviewers` write **insufficient quorum** and call the
   verdict advisory.

Apply the fixes only if the user asked for that. A second round reviews the fixed change with a
fresh brief, and its reviewers again see nothing of the first round's answers.

## Reference

- `references/agents.md`: every reviewer CLI, its template, how it is kept read-only, known
  failures, and how to recover an answer from a reviewer that timed out.
- `references/roster.md`: the roster format, where each agent keeps it, the approvals of a
  project's roster.
- `references/brief.md`: the brief format, for writing one by hand.

## No Python

`sh <skill>/scripts/detect-agents.sh` (`powershell -ExecutionPolicy Bypass -File
<skill>\scripts\detect-agents.ps1` on Windows) prints the same detection lines. Write the roster
by hand (`references/roster.md`) and the brief by hand (`references/brief.md`). For each reviewer
substitute the quoted paths of the brief and of its output file for `{brief}` and `{out}`, put
`CROSSREVIEW_DEPTH=1 ` in front of the command (`$env:CROSSREVIEW_DEPTH = '1'; ` in PowerShell) so
the reviewer knows not to start a crossreview of its own, and start it with your own background
shell tool in an empty directory, each under a time limit; wait for all of them, then continue at
step 5.
