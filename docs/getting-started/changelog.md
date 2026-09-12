# Changelog

Release notes of every published version, generated from the [GitHub Releases](https://github.com/coddy-project/coddy-agent/releases) of the repository by `make docs-changelog`. `coddy update` prints the notes of the versions it skips over, so the answer to "what changed" is also on the screen after an upgrade.

## 1.1.1 - 2026-09-11

### What's Changed
* feat(update): report what changed once the update is in by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/196


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/1.0.37...1.1.1

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/1.1.1).

## 1.1.0 - 2026-09-11

Coddy 1.1 is the week after 1.0, collected into one release. `coddy http`, `coddy gateway` and `coddy swarm` are gone: one `coddy serve` runs every subsystem the configuration enables, over one session manager. A Telegram conversation is an ordinary session now, so the web UI lists it and watches the turn live. The swarm arrives as an early preview - a stateless relay, a map that is the way into the fleet, and a tunnel for machines that accept no inbound connection. NeuralDeep signs in through the device flow by default, log levels are scoped per component, and releases ship `.deb`, `.rpm` and a Homebrew cask.

Every pull request merged after 1.0.0, in the order the release notes group them.

### One process for every surface

- #174 feat(serve): run every enabled subsystem in one process
- #178 feat(serve): keep the daemon alive, and keep its configuration current
- #187 Finish the enable/serve rename, and make the flow demand it

### Swarm

- #155 feat(swarm): a stateless relay, and a map that is the way into it
- #170 fix(swarm): keep a proxied stream alive when the request body is closed
- #186 fix(swarm): attach the StartJoins doc comment to StartJoins

### NeuralDeep sign-in and account usage

- #172 feat(neuraldeep): sign in through the device flow by default
- #143 feat: NeuralDeep account usage on the console status bar (issue #102)
- #144 feat(ui): NeuralDeep account usage pill and banner in the composer (issue #102)
- #145 feat(agent): wait for a hit usage limit to lift and resume the turn (#102, sub-feature 4)
- #148 feat(config): providers[].usage_limits_panel switches the account usage panel per row
- #179 fix(usage): a model the account may not call is named, not hidden
- #180 Bound the limit wait by what it charged, not by the wall clock

### Logging

- #177 Scope log levels per component and make the Telegram command path readable

### Packaging and update

- #156 Publish deb, rpm and a Homebrew cask, and make coddy update defer to them
- #164 Aim the Homebrew submission at homebrew/core as a formula, and fix the brew upgrade hint
- #193 fix(update): refresh the man page and the shell completions beside the binary

### Configuration

- #165 feat(config): keep config.yaml comments on save, and publish the editor schema at coddy.dev
- #166 fix(http): announce configuration reloads so open clients re-read the model list
- #192 feat(config): -t / --test-config checks config.yaml against the embedded schema
- #194 feat(config): --dry-run probes what config.yaml points at before anything starts

### Agent, rules, hooks and skills

- #142 feat(hooks): operator lifecycle hooks in Claude Code's file shape
- #150 feat(rules): .agents/rules out of the box, dialect by extension (.mdc Cursor, .md Claude Code)
- #147 fix(skills): resolve ${CWD} in skills.dirs against the session workspace (issue #146)
- #141 feat(export): built-in /export writes the session transcript to a file
- #176 Retry a background wake refused by a busy session

### Providers and models

- #162 fix(codex): publish the subscription catalog on login, retire gpt-4o defaults, unify the sign-in command, ship the gateway
- #181 fix: replay a request the model lane failed, and name the provider in LLM errors

### Console and editors

- #171 feat(cli): read long question options in full
- #175 fix(cli): hand the block directive over instead of pausing for it
- #191 fix(rules,cli): read nested AGENTS.md on demand instead of walking the workspace, and test the console on macOS
- #139 fix(acp): honour permission answers in the protocol's response shape

### Web UI

- #140 feat(composer): line-range @mentions with a picker for manual entry
- #151 feat(ui): render spawn_agent calls as agent cards
- #163 feat(ui): create a folder from the workspace picker, and ship the swarm relay by default
- #167 fix(ui): keep the Open folder dialog scrollable on a short window
- #173 Expand code highlighting languages and theme palettes

### Tests and housekeeping

- #149 test(ui/i18n): expect English theme names under the Russian locale
- #168 test: pin the grandchild's creation before the child retires it
- #189 chore(test): make test is the express run, the tag matrix moves to CI

**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/1.0.0...1.1.0

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/1.1.0).

## 1.0.37 - 2026-09-11

### What's Changed
* feat(config): --dry-run probes what config.yaml points at before anything starts by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/194


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/1.0.36...1.0.37

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/1.0.37).

## 1.0.36 - 2026-09-11

### What's Changed
* fix(update): refresh the man page and the shell completions beside the binary by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/193


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/1.0.35...1.0.36

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/1.0.36).

## 1.0.35 - 2026-09-11

### What's Changed
* feat(config): -t / --test-config checks config.yaml against the embedded schema by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/192


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/1.0.34...1.0.35

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/1.0.35).

## 1.0.34 - 2026-09-11

### What's Changed
* fix(rules,cli): read nested AGENTS.md on demand instead of walking the workspace, and test the console on macOS by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/191


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/1.0.33...1.0.34

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/1.0.34).

## 1.0.33 - 2026-09-11

### What's Changed
* Finish the enable/serve rename, and make the flow demand it by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/187


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/1.0.32...1.0.33

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/1.0.33).

## 1.0.32 - 2026-09-11

### What's Changed
* chore(test): make test is the express run, the tag matrix moves to CI by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/189


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/1.0.31...1.0.32

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/1.0.32).

## 1.0.31 - 2026-09-11

### What's Changed
* fix(swarm): attach the StartJoins doc comment to StartJoins by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/186


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/1.0.30...1.0.31

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/1.0.31).

## 1.0.30 - 2026-09-11

### What's Changed
* fix: replay a request the model lane failed, and name the provider in LLM errors by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/181


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/1.0.29...1.0.30

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/1.0.30).

## 1.0.29 - 2026-09-10

### What's Changed
* fix(usage): a model the account may not call is named, not hidden by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/179


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/1.0.28...1.0.29

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/1.0.29).

## 1.0.28 - 2026-09-10

### What's Changed
* Bound the limit wait by what it charged, not by the wall clock by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/180


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/1.0.27...1.0.28

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/1.0.28).

## 1.0.27 - 2026-09-10

### What's Changed
* Scope log levels per component and make the Telegram command path readable by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/177


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/1.0.26...1.0.27

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/1.0.27).

## 1.0.26 - 2026-09-10

### What's Changed
* feat(serve): keep the daemon alive, and keep its configuration current by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/178


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/1.0.25...1.0.26

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/1.0.26).

## 1.0.25 - 2026-09-10

### What's Changed
* Retry a background wake refused by a busy session by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/176


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/1.0.24...1.0.25

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/1.0.25).

## 1.0.24 - 2026-09-10

### What's Changed
* fix(cli): hand the block directive over instead of pausing for it by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/175


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/1.0.23...1.0.24

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/1.0.24).

## 1.0.23 - 2026-09-10

### What's Changed
* feat(serve): run every enabled subsystem in one process by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/174


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/1.0.22...1.0.23

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/1.0.23).

## 1.0.22 - 2026-09-10

### What's Changed
* Expand code highlighting languages and theme palettes by @hijera in https://github.com/coddy-project/coddy-agent/pull/173


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/1.0.21...1.0.22

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/1.0.22).

## 1.0.21 - 2026-09-10

### What's Changed
* feat(neuraldeep): sign in through the device flow by default by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/172


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/1.0.20...1.0.21

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/1.0.21).

## 1.0.20 - 2026-09-09

### What's Changed
* feat(cli): read long question options in full by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/171


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/1.0.19...1.0.20

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/1.0.20).

## 1.0.19 - 2026-09-09

### What's Changed
* feat(ui): create a folder from the workspace picker, and ship the swarm relay by default by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/163


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/1.0.18...1.0.19

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/1.0.19).

## 1.0.18 - 2026-09-09

### What's Changed
* fix(swarm): keep a proxied stream alive when the request body is closed by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/170


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/1.0.17...1.0.18

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/1.0.18).

## 1.0.17 - 2026-09-09

### What's Changed
* Aim the Homebrew submission at homebrew/core as a formula, and fix the brew upgrade hint by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/164


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/1.0.16...1.0.17

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/1.0.17).

## 1.0.16 - 2026-09-09

### What's Changed
* fix(ui): keep the Open folder dialog scrollable on a short window by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/167
* test: pin the grandchild's creation before the child retires it by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/168


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/1.0.15...1.0.16

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/1.0.16).

## 1.0.15 - 2026-09-09

### What's Changed
* feat(config): keep config.yaml comments on save, and publish the editor schema at coddy.dev by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/165


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/1.0.14...1.0.15

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/1.0.15).

## 1.0.14 - 2026-09-09

### What's Changed
* fix(http): announce configuration reloads so open clients re-read the model list by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/166


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/1.0.13...1.0.14

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/1.0.14).

## 1.0.13 - 2026-09-09

### What's Changed
* feat(swarm): a stateless relay, and a map that is the way into it by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/155


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/1.0.12...1.0.13

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/1.0.13).

## 1.0.12 - 2026-09-09

### What's Changed
* fix(codex): publish the subscription catalog on login, retire gpt-4o defaults, unify the sign-in command, ship the gateway by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/162


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/1.0.11...1.0.12

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/1.0.12).

## 1.0.11 - 2026-09-09

### What's Changed
* Publish deb, rpm and a Homebrew cask, and make coddy update defer to them by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/156


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/1.0.10...1.0.11

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/1.0.11).

## 1.0.10 - 2026-09-07

### What's Changed
* feat(ui): render spawn_agent calls as agent cards by @hijera in https://github.com/coddy-project/coddy-agent/pull/151


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/1.0.9...1.0.10

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/1.0.10).

## 1.0.9 - 2026-09-07

### What's Changed
* feat(rules): .agents/rules out of the box, dialect by extension (.mdc Cursor, .md Claude Code) by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/150


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/1.0.8...1.0.9

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/1.0.9).

## 1.0.8 - 2026-09-07

### What's Changed
* feat(config): providers[].usage_limits_panel switches the account usage panel per row by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/148


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/1.0.7...1.0.8

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/1.0.8).

## 1.0.7 - 2026-09-07

### What's Changed
* test(ui/i18n): expect English theme names under the Russian locale by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/149


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/1.0.6...1.0.7

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/1.0.7).

## 1.0.6 - 2026-09-07

### What's Changed
* fix(skills): resolve ${CWD} in skills.dirs against the session workspace (issue #146) by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/147


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/1.0.5...1.0.6

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/1.0.6).

## 1.0.5 - 2026-09-06

### What's Changed
* feat(ui): NeuralDeep account usage pill and banner in the composer (issue #102) by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/144
* feat(agent): wait for a hit usage limit to lift and resume the turn (#102, sub-feature 4) by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/145
* feat: NeuralDeep account usage on the console status bar (issue #102) by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/143


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/1.0.4...1.0.5

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/1.0.5).

## 1.0.4 - 2026-09-06

### What's Changed
* feat(composer): line-range @mentions with a picker for manual entry by @hijera in https://github.com/coddy-project/coddy-agent/pull/140


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/1.0.3...1.0.4

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/1.0.4).

## 1.0.3 - 2026-09-06

### What's Changed
* feat(hooks): operator lifecycle hooks in Claude Code's file shape by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/142


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/1.0.2...1.0.3

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/1.0.3).

## 1.0.2 - 2026-09-06

### What's Changed
* feat(export): built-in /export writes the session transcript to a file by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/141


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/1.0.1...1.0.2

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/1.0.2).

## 1.0.1 - 2026-09-04

### What's Changed
* fix(acp): honour permission answers in the protocol's response shape by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/139


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/1.0.0...1.0.1

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/1.0.1).

## 1.0.0 - 2026-09-04

### What's Changed
* Subagents: user-defined child agents on the background task pool by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/138


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.94...1.0.0

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/1.0.0).

## 0.9.94 - 2026-09-04

### What's Changed
* feat(modes): add read-only ask mode ported from foxxy-agent by @hijera in https://github.com/coddy-project/coddy-agent/pull/129


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.93...0.9.94

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.94).

## 0.9.93 - 2026-09-03

### What's Changed
* test(cli): drain the turn worker before walking the session bundle by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/137


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.92...0.9.93

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.93).

## 0.9.92 - 2026-09-03

### What's Changed
* fix(cli): wire the config reloader into the console and fix e2e idle detection by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/136


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.91...0.9.92

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.92).

## 0.9.91 - 2026-09-03

### What's Changed
* fix(examples): point the demo config's second model at a model rpa still serves by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/135


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.90...0.9.91

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.91).

## 0.9.90 - 2026-09-03

### What's Changed
* fix(examples): align compaction e2e harnesses with the forced /compact contract by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/134


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.89...0.9.90

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.90).

## 0.9.89 - 2026-09-03

### What's Changed
* fix(examples): read the ACP slash catalog after the session/new response by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/133


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.88...0.9.89

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.89).

## 0.9.88 - 2026-09-03

### What's Changed
* fix(config): keep reasoning_levels auto-detection through a settings save by @hijera in https://github.com/coddy-project/coddy-agent/pull/109
* perf(ui): bound the per-session transcript cache and memoize message rows by @hijera in https://github.com/coddy-project/coddy-agent/pull/125


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.87...0.9.88

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.88).

## 0.9.87 - 2026-09-03

### What's Changed
* fix(ui): lay boolean settings out with a shared SwitchField by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/132


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.86...0.9.87

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.87).

## 0.9.86 - 2026-09-03

### What's Changed
* feat(providers): select the NeuralDeep API endpoint, hub follows the choice by @hijera in https://github.com/coddy-project/coddy-agent/pull/126


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.85...0.9.86

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.86).

## 0.9.85 - 2026-09-03

### What's Changed
* Structured todo tool previews by @hijera in https://github.com/coddy-project/coddy-agent/pull/131


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.84...0.9.85

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.85).

## 0.9.84 - 2026-08-28

### What's Changed
* fix(ui): reach other drives from the workspace folder picker on Windows by @hijera in https://github.com/coddy-project/coddy-agent/pull/127


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.83...0.9.84

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.84).

## 0.9.83 - 2026-08-28

### What's Changed
* feat(ui): complete Russian localization of conversation surfaces by @hijera in https://github.com/coddy-project/coddy-agent/pull/123


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.82...0.9.83

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.83).

## 0.9.82 - 2026-08-27

### What's Changed
* fix(llm): preserve reasoning on truncated streams by @hijera in https://github.com/coddy-project/coddy-agent/pull/128


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.81...0.9.82

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.82).

## 0.9.81 - 2026-08-27

### What's Changed
* feat(ui,cli): live status line next to the typing dots by @hijera in https://github.com/coddy-project/coddy-agent/pull/124


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.80...0.9.81

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.81).

## 0.9.80 - 2026-08-27

### What's Changed
* Fix Windows self-update with helper by @hijera in https://github.com/coddy-project/coddy-agent/pull/110


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.79...0.9.80

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.80).

## 0.9.79 - 2026-08-24

### What's Changed
* fix(llm): honor server retry pauses and fail truncated streams by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/122


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.78...0.9.79

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.79).

## 0.9.78 - 2026-08-23

### What's Changed
* feat(providers): NeuralDeep hub sign-in (coddy providers login + SPA device flow) by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/120


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.77...0.9.78

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.78).

## 0.9.77 - 2026-08-18

### What's Changed
* feat(cli): run !! commands locally and keep them out of the agent context by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/119


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.76...0.9.77

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.77).

## 0.9.76 - 2026-08-18

### What's Changed
* feat(prompts): name Coddy at the start of every system prompt by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/118


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.75...0.9.76

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.76).

## 0.9.75 - 2026-08-18

### What's Changed
* feat(llm): let a models[] entry answer without streaming (stream: false) by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/116


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.74...0.9.75

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.75).

## 0.9.74 - 2026-08-17

### What's Changed
* fix(http): keep a session openable after one of its branches is deleted by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/114


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.73...0.9.74

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.74).

## 0.9.73 - 2026-08-17

### What's Changed
* refactor(ui): drop unused ThemeToggle component by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/112


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.72...0.9.73

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.73).

## 0.9.72 - 2026-08-17

### What's Changed
* docs/refresh screenshots and docs by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/111


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.71...0.9.72

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.72).

## 0.9.71 - 2026-08-17

### What's Changed
* Add prompt improvement control by @hijera in https://github.com/coddy-project/coddy-agent/pull/103


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.70...0.9.71

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.71).

## 0.9.70 - 2026-08-16

### What's Changed
* Add overflow controls for large tool calls (supersedes #95) by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/107
* fix(llm): llama.cpp as OpenAI-compatible provider - survive its SSE dialect by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/106


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.69...0.9.70

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.70).

## 0.9.69 - 2026-08-16

### What's Changed
* Fix provider name HTML pattern validation by @hijera in https://github.com/coddy-project/coddy-agent/pull/108


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.68...0.9.69

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.69).

## 0.9.68 - 2026-08-16

### What's Changed
* Staged uci-like agent self-configuration with commit/rollback (supersedes #82) by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/105


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.67...0.9.68

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.68).

## 0.9.67 - 2026-08-15

### What's Changed
* feat(remote): console and ACP surfaces connect to a remote coddy http server by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/101


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.66...0.9.67

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.67).

## 0.9.66 - 2026-08-14

### What's Changed
* feat(cli): interactive console TUI - bare coddy opens a pi-style chat by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/100


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.65...0.9.66

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.66).

## 0.9.65 - 2026-08-14

### What's Changed
* feat(ui): paste clipboard images and drag&drop files onto the composer by @hijera in https://github.com/coddy-project/coddy-agent/pull/98


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.64...0.9.65

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.65).

## 0.9.64 - 2026-08-14

### What's Changed
* feat(llm): recognize qwen3 and gpt-oss reasoning models by @hijera in https://github.com/coddy-project/coddy-agent/pull/99


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.63...0.9.64

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.64).

## 0.9.63 - 2026-08-14

### What's Changed
* feat(ui): add extensible UI localization and language selector by @hijera in https://github.com/coddy-project/coddy-agent/pull/87


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.62...0.9.63

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.63).

## 0.9.62 - 2026-08-13

### What's Changed
* Watch a composer turn from any client, whatever started it by @hijera in https://github.com/coddy-project/coddy-agent/pull/97


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.61...0.9.62

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.62).

## 0.9.61 - 2026-08-10

### What's Changed
* chore(ui): delete orphaned DiffView and toolCallArgsDisplay by @hijera in https://github.com/coddy-project/coddy-agent/pull/96


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.60...0.9.61

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.61).

## 0.9.60 - 2026-08-08

### What's Changed
* feat(ui): replace native confirm with frosted-glass React ConfirmDialog by @hijera in https://github.com/coddy-project/coddy-agent/pull/94


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.59...0.9.60

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.60).

## 0.9.59 - 2026-08-08

### What's Changed
* feat(zcode): auto-attach .cursor/rules by glob via SessionStart/PreToolUse hooks by @hijera in https://github.com/coddy-project/coddy-agent/pull/93


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.58...0.9.59

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.59).

## 0.9.58 - 2026-08-07

### What's Changed
* Add OpenCode project rule hooks by @hijera in https://github.com/coddy-project/coddy-agent/pull/89


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.57...0.9.58

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.58).

## 0.9.57 - 2026-08-07

### What's Changed
* Disable duplicate LLM SDK retries by @hijera in https://github.com/coddy-project/coddy-agent/pull/92


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.56...0.9.57

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.57).

## 0.9.56 - 2026-08-04

### What's Changed
* Fix ACP slash commands, mode wire schema, and the configured MCP hot reload by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/90


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.55...0.9.56

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.56).

## 0.9.55 - 2026-08-04

### What's Changed
* fix(textenc): decode short mixed-encoding files via the system code page by @hijera in https://github.com/coddy-project/coddy-agent/pull/85


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.54...0.9.55

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.55).

## 0.9.54 - 2026-08-04

### What's Changed
* fix(background): ask whether the task's process is running, not whether a pid opens by @hijera in https://github.com/coddy-project/coddy-agent/pull/78


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.53...0.9.54

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.54).

## 0.9.53 - 2026-08-03

### What's Changed
* fix(shell): adopt a foreground command that outlives its timeout by @hijera in https://github.com/coddy-project/coddy-agent/pull/88


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.52...0.9.53

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.53).

## 0.9.52 - 2026-08-02

### What's Changed
* chore(codex): attach .cursor rules to Codex sessions via lifecycle hooks by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/84


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.51...0.9.52

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.52).

## 0.9.51 - 2026-08-02

### What's Changed
* fix(mcp): gate project-local .coddy/mcp.json behind workspace trust by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/83


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.50...0.9.51

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.51).

## 0.9.50 - 2026-08-02

### What's Changed
* fix(session): attach files in legacy encodings, not only UTF-8 by @hijera in https://github.com/coddy-project/coddy-agent/pull/81


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.49...0.9.50

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.50).

## 0.9.49 - 2026-07-31

### What's Changed
* Background tasks: detached run_command, in-session tasks panel, wake-on-finish, program-wide grants by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/77


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.48...0.9.49

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.49).

## 0.9.48 - 2026-07-28

### What's Changed
* feat(context): per-tool output limits and read/grep result eviction by @hijera in https://github.com/coddy-project/coddy-agent/pull/74


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.47...0.9.48

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.48).

## 0.9.47 - 2026-07-28

### What's Changed
* Codex ChatGPT auth: review fixes, e2e specs on both surfaces, terminal sign-in by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/75


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.46...0.9.47

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.47).

## 0.9.46 - 2026-07-27

### What's Changed
* MCP management: .coddy/mcp.json, per-tool switches, /coddy/mcp API, Cursor-style settings tab by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/73


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.45...0.9.46

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.46).

## 0.9.45 - 2026-07-27

### What's Changed
* Load nested AGENTS.md only for directories the agent touches by @hijera in https://github.com/coddy-project/coddy-agent/pull/71
* fix(shell): make PowerShell output readable and exit codes accurate on Windows by @hijera in https://github.com/coddy-project/coddy-agent/pull/72


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.44...0.9.45

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.45).

## 0.9.44 - 2026-07-25

### What's Changed
* Fix context usage after compaction by @hijera in https://github.com/coddy-project/coddy-agent/pull/68
* Improve tool approval previews and transcript cards by @hijera in https://github.com/coddy-project/coddy-agent/pull/67
* Add runaway-loop protection to the ReAct turn by @hijera in https://github.com/coddy-project/coddy-agent/pull/69
* git pre-commit linter hook added by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/70


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.43...0.9.44

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.44).

## 0.9.43 - 2026-07-22

### What's Changed
* fix(ui): readable Allow/Allow always buttons in light theme by @hijera in https://github.com/coddy-project/coddy-agent/pull/65
* Skills auto-discovery, slash-command & compaction UX, agent-loop resilience, print_tree by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/66


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.42...0.9.43

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.43).

## 0.9.42 - 2026-07-22

### What's Changed
* feat(skills): marketplace versioning, update detection & a Claude-Code-style plugin command (extends #54) by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/64


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.41...0.9.42

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.42).

## 0.9.41 - 2026-07-21

### What's Changed
* fix(fs): preserve line endings in edit tools by @hijera in https://github.com/coddy-project/coddy-agent/pull/57
* feat: context compaction — /compact command, auto threshold, config by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/63


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.40...0.9.41

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.41).

## 0.9.40 - 2026-07-21

### What's Changed
* fix(ui): surface remote send failures and probe active-env health (#60) by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/61


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.39...0.9.40

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.40).

## 0.9.39 - 2026-07-21

### What's Changed
* feat: local & remote operation for coddy http (auth, CORS, UI environment selector) by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/59


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.38...0.9.39

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.39).

## 0.9.38 - 2026-07-20

### What's Changed
* Fix reads with offsets beyond end of file by @hijera in https://github.com/coddy-project/coddy-agent/pull/56


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.37...0.9.38

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.38).

## 0.9.37 - 2026-07-20

### What's Changed
* Add portable search and platform-aware shell execution by @hijera in https://github.com/coddy-project/coddy-agent/pull/55


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.36...0.9.37

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.37).

## 0.9.36 - 2026-07-14

### What's Changed
* Escape $ in proxy secrets so they survive config load by @hijera in https://github.com/coddy-project/coddy-agent/pull/52


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.35...0.9.36

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.36).

## 0.9.35 - 2026-07-12

### What's Changed
* Workspace switcher: folder, git branch, and worktree chips (Claude Desktop style) by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/51


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.34...0.9.35

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.35).

## 0.9.34 - 2026-07-12

### What's Changed
* Fix: авто-синхронизация default model в ReAct при переименовании логической модели by @hijera in https://github.com/coddy-project/coddy-agent/pull/50


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.33...0.9.34

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.34).

## 0.9.33 - 2026-07-08

### What's Changed
* Add NeuralDeep LLM provider by @hijera in https://github.com/coddy-project/coddy-agent/pull/49


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.32...0.9.33

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.33).

## 0.9.32 - 2026-07-05

### What's Changed
* Tunes of light theme by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/48


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.31...0.9.32

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.32).

## 0.9.31 - 2026-07-05

### What's Changed
* Discover nested AGENTS.md alongside .cursor/.coddy/.claude/.codex rules by @hijera in https://github.com/coddy-project/coddy-agent/pull/46


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.30...0.9.31

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.31).

## 0.9.30 - 2026-07-05

### What's Changed
* Fix YAML config parsing on Windows when expanding path vars by @hijera in https://github.com/coddy-project/coddy-agent/pull/47


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.29...0.9.30

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.30).

## 0.9.29 - 2026-07-05

### What's Changed
* Document YAML config schema: field reference + JSON Schema (closes #43) by @hijera in https://github.com/coddy-project/coddy-agent/pull/45


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.28...0.9.29

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.29).

## 0.9.28 - 2026-07-04

### What's Changed
* docs: Windows install paths, PATH refresh guidance, absolute-path tip for integrations by @hijera in https://github.com/coddy-project/coddy-agent/pull/44


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.27...0.9.28

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.28).

## 0.9.27 - 2026-07-04

### What's Changed
* Settings interface redesign by @hijera in https://github.com/coddy-project/coddy-agent/pull/40

### New Contributors
* @hijera made their first contribution in https://github.com/coddy-project/coddy-agent/pull/40

**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.26...0.9.27

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.27).

## 0.9.26 - 2026-06-20

### What's Changed
* feat(config): support api_key_command credential helper for providers by @vakovalskii in https://github.com/coddy-project/coddy-agent/pull/38
* Clean some tests and tunes of read tool by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/39

### New Contributors
* @vakovalskii made their first contribution in https://github.com/coddy-project/coddy-agent/pull/38

**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.25...0.9.26

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.26).

## 0.9.25 - 2026-06-15

### What's Changed
* Feat/telegram rich by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/36


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.24...0.9.25

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.25).

## 0.9.24 - 2026-06-14

### What's Changed
* Bugfix/light by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/35


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.23...0.9.24

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.24).

## 0.9.23 - 2026-06-14

### What's Changed
* Anthropic privider support fixed by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/34


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.22...0.9.23

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.23).

## 0.9.22 - 2026-06-14

### What's Changed
* Bugfix/dropdown by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/33


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.21...0.9.22

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.22).

## 0.9.21 - 2026-06-14

### What's Changed
* Feat/level by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/32


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.20...0.9.21

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.21).

## 0.9.20 - 2026-06-07

### What's Changed
* Images added by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/31


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.19...0.9.20

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.20).

## 0.9.19 - 2026-06-07

### What's Changed
* Feat/files by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/30


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.18...0.9.19

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.19).

## 0.9.18 - 2026-06-07

### What's Changed
* Feat/ssh by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/29


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.17...0.9.18

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.18).

## 0.9.17 - 2026-06-06

### What's Changed
* Feat/skills by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/28


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.16...0.9.17

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.17).

## 0.9.16 - 2026-06-06

### What's Changed
* Feat/acp perms by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/27


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.15...0.9.16

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.16).

## 0.9.15 - 2026-06-06

### What's Changed
* Feat/rollback by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/26


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.14...0.9.15

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.15).

## 0.9.14 - 2026-05-31

**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.13...0.9.14

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.14).

## 0.9.13 - 2026-05-31

**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.12...0.9.13

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.13).

## 0.9.12 - 2026-05-31

### What's Changed
* Feat/gateway by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/25


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.11...0.9.12

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.12).

## 0.9.11 - 2026-05-30

**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.10.1...0.9.11

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.11).

## 0.9.10 - 2026-05-30

### What's Changed
* Bugfix/patch by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/24


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.9...0.9.10

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.10).

## 0.9.9 - 2026-05-26

### What's Changed
* Tunes of paches by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/23


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.8...0.9.9

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.9).

## 0.9.8 - 2026-05-26

### What's Changed
* Patch fixed by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/22


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.7...0.9.8

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.8).

## 0.9.7 - 2026-05-26

### What's Changed
* Feat/docs by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/21


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.6...0.9.7

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.7).

## 0.9.6 - 2026-05-25

### What's Changed
* Additional screenshots by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/20


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.5...0.9.6

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.6).

## 0.9.5 - 2026-05-24

### What's Changed
* Context circle style updated for dark/light themes by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/19


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.4...0.9.5

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.5).

## 0.9.4 - 2026-05-24

### What's Changed
* Update command added by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/18


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.3...0.9.4

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.4).

## 0.9.3 - 2026-05-24

### What's Changed
* Feat rules support by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/17


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.2...0.9.3

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.3).

## 0.9.2 - 2026-05-24

### What's Changed
* code style fixed by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/16


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.1...0.9.2

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.2).

## 0.9.1 - 2026-05-23

### What's Changed
* Markdown render disabled for user by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/15


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.9.0...0.9.1

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.1).

## 0.9.0 - 2026-05-23

### What's Changed
* Feat/plan by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/7
* Updating binaries build pipelines by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/8
* Retry logic by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/9
* UI/white by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/10
* Bugfix/permission by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/11
* job_id renaming by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/12
* Pipes fixed by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/13
* Selected model saved for new session by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/14


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.8.1...0.9.0

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.9.0).

## 0.8.9 - 2026-05-23

### What's Changed
* Selected model saved for new session by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/14


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.8.8...0.8.9

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.8.9).

## 0.8.8 - 2026-05-23

### What's Changed
* Pipes fixed by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/13


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.8.7...0.8.8

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.8.8).

## 0.8.7 - 2026-05-23

### What's Changed
* job_id renaming by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/12


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.8.6...0.8.7

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.8.7).

## 0.8.6 - 2026-05-23

### What's Changed
* Bugfix/permission by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/11


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.8.5...0.8.6

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.8.6).

## 0.8.4 - 2026-05-23

### What's Changed
* Retry logic by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/9


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.8.3...0.8.4

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.8.4).

## 0.8.3 - 2026-05-19

### What's Changed
* Updating binaries build pipelines by @EvilFreelancer in https://github.com/coddy-project/coddy-agent/pull/8


**Full Changelog**: https://github.com/coddy-project/coddy-agent/compare/0.8.2...0.8.3

[Release page](https://github.com/coddy-project/coddy-agent/releases/tag/0.8.3).
