# Vendor-neutral agent instructions

Status: approved design, informed by an exhaustive memory audit and a four-reviewer cross-review. This plan deliberately does not choose a new canonical scoped-rule directory. That decision belongs to the separate [rule and policy delivery R&D](rule-policy-delivery-rnd.md).

## 1. What

Make the repository's standing contributor instructions describe Coddy's engineering and product contracts without treating Anthropic, Claude Code, Cursor, Codex, or any other coding-agent host as the conceptual owner of those contracts.

This change is a conservative cleanup of the instruction surfaces that already exist:

- root `AGENTS.md` and its `CLAUDE.md` compatibility symlink;
- root `DESIGN.md`;
- the tracked `.cursor/rules/*.mdc` and `.claude/rules/*.md` representations;
- the repository-specific Codex, OpenCode, and ZCode adapters and their documentation;
- the bundled `rpa-gen-rules` skill that currently teaches the same layout.

The change also closes one confirmed current defect: the Codex, ZCode, and OpenCode adapters ignore a scoped rule whose Cursor `globs` frontmatter is a YAML list. In the current tree that silently omits `provider-proxy.mdc` for edits under `internal/llm`.

It does not migrate policy into `.agents/rules`, `.coddy/rules`, a manifest, or another newly invented format. The supported delivery mechanisms and the right canonical location must be measured first.

## 2. Evidence and current state

The design follows a review of all 230 files and 6,255 lines in the project's external memory directory, the current instruction files, the rule loaders, the host adapters, their documentation, and the recent commits that introduced three-layer `AGENTS.md` / `DESIGN.md` loading.

The relevant current facts are:

- root `AGENTS.md` is 52,364 bytes and root `DESIGN.md` is 299,183 bytes;
- Coddy always loads the root `AGENTS.md` and `DESIGN.md`, and loads nested documents on demand;
- Coddy's project rule discovery chain is `.coddy/rules`, `.agents/rules`, `.cursor/rules`, `.claude/rules`, `.codex/rules`, and it reads the first populated directory rather than merging them;
- `.agents/rules` is supported by Coddy, but it is not a shared native mechanism for the external hosts in scope;
- `.coddy/rules` is a repository-owned Coddy mechanism, but external hosts do not natively consume it;
- `.codex/rules.md` has no runtime consumer. It is a manually maintained human-readable index. Current Codex documentation already says the older advisory arrangement based on that file was skipped;
- the Codex and ZCode hooks and the OpenCode plugin currently read `.cursor/rules` through three separate parsers;
- `.claude/rules` and `.cursor/rules` are intended to carry equivalent policy, but four pairs already differ in body references or workflow ownership wording;
- root `AGENTS.md` says `.cursor/rules` is the single source and forbids hand-copying into `.claude`, while both workflow mirrors instruct contributors to copy `.claude` changes into `.cursor`;
- root `DESIGN.md` calls `.claude/rules/russian-wording.md` the rule and `.cursor` its mirror, contradicting the root instructions in the opposite direction;
- path-scoped delivery is not an enforcement boundary. Hosts differ in when or whether a matching rule reaches the model before the first mutation.

The memory contains many durable lessons, but also old PR state, vendor-prefixed branches, temporary worktrees, model quotas, reviewer timing, local ports, and superseded commands. Standing instructions take the invariant, not the chronology.

## 3. Design principles

### 3.1 Project policy is expressed in repository terms

General instructions use terms such as `coding-agent host`, `reviewer`, `browser automation`, `workspace`, and `scratch directory`. A product or provider name remains only when it identifies a real API, file format, compatibility surface, executable, or host-specific setup requirement.

Provider-specific product behavior is not generalized away. Anthropic request semantics, Codex authentication, Devin usage data, Cursor native rule syntax, or Claude Code compatibility files remain explicit in their owning code and documentation.

### 3.2 Compatibility is not canonical ownership

The repository must not claim that a no-op index or a compatibility directory is a universal policy source. Until the R&D chooses an architecture, `.cursor/rules` and `.claude/rules` are two compatibility representations of the same policy. Neither representation may carry unique body text.

This is an interim constraint, not the final delivery design.

### 3.3 Critical constraints do not depend only on scoped model context

A path-scoped rule may arrive late, be skipped by an untrusted adapter, or be unsupported by a host. Security boundaries, project-local trust, package dependency direction, destructive-operation restrictions, and other non-negotiable invariants remain in always-loaded instructions, code, tests, or CI as appropriate.

### 3.4 Observable contracts replace product analogies

An external product may be recorded as historical inspiration, but the normative text specifies Coddy's own geometry, state, copy, focus, accessibility, and responsive behavior. A contributor must be able to implement and test the contract without opening the referenced product.

## 4. Root `AGENTS.md`

### 4.1 Rename and reframe host integration

Rename `Codex, OpenCode, Cursor and ZCode rules` to `Agent host integrations`.

The section starts with the common policy and then states only confirmed host mechanics:

- `AGENTS.md` is the repository-wide instruction entrypoint and the only guaranteed Codex instruction source in this plan;
- `CLAUDE.md` remains a symlink to `AGENTS.md` for compatibility and must never become a divergent regular file;
- Cursor has its native `.cursor/rules` representation;
- Claude Code has its native `.claude/rules` representation;
- Coddy has its documented first-populated-directory discovery chain and nested document loading;
- OpenCode and ZCode use repository adapters whose exact behavior is implementation-specific and tested separately;
- the current Codex hook is described as a repository adapter requiring trust, not as a native universal rule system. Its actual capabilities remain an R&D question.

Do not call one vendor directory the neutral source of truth.

### 4.2 Remove the no-op Codex index

Delete `.codex/rules.md` in the future implementation. Remove current-workflow references to it from:

- root `AGENTS.md`;
- `docs/contributing/codex-hooks.md`;
- `docs/contributing/zcode-hooks.md`;
- the bundled `internal/skills/bundled/rpa-gen-rules` README, skill body, references, and examples.

Historical `docs/plans/**` records keep the old name when it describes what a past change did. They are not current instructions and must not be rewritten merely to erase history.

The Codex adapter documentation must say that the hook reads the actual rule representation directly. It must not imply that the deleted index affects runtime behavior.

### 4.3 Generalize code-review ownership

Rename `Codex Code Review Rules` to `Code review invariants` and remove wording that makes one reviewer the owner of the section.

Keep the repository-specific constraints themselves:

- HTTP handlers and OpenAPI stay in sync;
- provider traffic follows the configured proxy path;
- lean packages do not import build-tagged packages;
- project-local executable configuration stays behind trust gates;
- packaging, documentation, and embedded UI artifacts stay synchronized.

These constraints apply regardless of which human or agent performs the review.

### 4.4 Keep cross-review policy, not the roster

If cross-review is mentioned, state only the durable protocol:

- reviewers receive the same self-contained brief and remain blind to one another;
- every configured reviewer is allowed to answer or fail explicitly;
- findings are hypotheses until reproduced or traced through the current code;
- the orchestrator decides each finding as fix, not worth fixing, rejected, or open;
- the decision is evidence-based, not a vote.

Exact reviewer products, model IDs, commands, quotas, timeout recovery, and roster location stay in the `/crossreview` skill and operator configuration.

### 4.5 Preserve compatibility facts without turning them into general policy

Phrases such as `Claude Code's file shape` are replaced by a contract-first description such as `the hook JSON shape implemented by internal/hooks`, followed by a compatibility note when useful. The same rule applies to skills, subagents, and plugin formats.

## 5. Root `DESIGN.md`

This plan does not split the file. A split is unsafe until the R&D proves that every target host receives the relevant nested `DESIGN.md` before editing its owning path.

### 5.1 Remove normative external-product comparisons

Rewrite the current comparisons without changing behavior:

- `mirroring Claude Desktop's workspace chips` becomes the direct placement contract for `.composer-context-row`;
- `Claude Desktop style` becomes the explicit Recent-menu row, selection, separator, and `Open folder...` contract already described in the same section;
- the usage section is specified as the final block of the context popover with no extra composer control;
- usage-banner copy and states are defined by the existing literal and localization contract rather than `mirrors Claude Desktop's limit notice`;
- `Cursor-style editing` becomes `inline JSON editing` followed by the existing controls and validation behavior.

Add one sentence near Design references: external products may explain provenance, but only the behavior specified in this file is normative.

### 5.2 Stop naming a vendor mirror as the wording authority

Keep the Russian wording contract in `DESIGN.md`, but replace the claim that `.claude/rules/russian-wording.md` is the rule and `.cursor` its mirror. The normative product contract is the wording in `DESIGN.md`, and `ruWording.test.ts` is the executable check over the Russian dictionary. Current host-rule representations repeat the contributor instruction until the R&D chooses their final ownership.

### 5.3 Promote two durable UI principles

Add concise cross-cutting principles near Foundations:

1. **One fact, one visual location.** A status, result, duration, or action is shown in the surface that owns it and is not repeated in nearby transcript rows, panels, or labels unless accessibility requires an equivalent hidden name.
2. **Measure browser geometry at the browser boundary.** A geometry-sensitive contract uses a live-browser check when jsdom cannot perform layout. Component and source-contract tests remain useful for DOM semantics, but they do not substitute for measured layout.

These principles summarize the repeated memory lessons behind background-task status, tool-result duplication, chevron alignment, transcript virtualization, and responsive settings layout.

## 6. Interim rule mirror contract

The physical rule layout stays unchanged until the R&D decision.

### 6.1 Equivalent rule set

Add a repository check that treats `.cursor/rules/<id>.mdc` and `.claude/rules/<id>.md` as one pair:

- both directories contain the same rule IDs;
- the Markdown body is byte-identical after adapter-specific sibling references have been removed from the bodies;
- descriptions match;
- Cursor `alwaysApply` / `globs` and Claude `paths` normalize to equivalent `always` or `paths` activation;
- a malformed or unsupported frontmatter form fails the check with the rule path.

As part of the implementation, replace sibling references such as `@workflow.md` versus `@workflow.mdc` with host-neutral prose or links to public repository documentation so the policy bodies can be identical.

The workflow rule no longer says that one vendor tree is primary and must be copied into the other. Its interim wording is symmetrical: changing either representation requires updating the paired representation in the same change, and the parity check enforces that requirement.

### 6.2 Close the YAML-list omission

The Codex hook, ZCode hook, and OpenCode plugin must parse both scalar and YAML-list `globs` used by the tracked Cursor rules.

The regression case is black-box and repository-specific:

- target path `internal/llm/openai.go`;
- expected scoped rules include `core-modules`, `implementation-order`, and `provider-proxy`;
- the test runs the same observable adapter entrypoint used by the host, not only a helper parser;
- a missing `provider-proxy` rule fails the test.

Use one fixture set for the path and expected IDs where practical, while allowing Python and JavaScript harnesses to invoke their real adapters.

Add a `make test-agent-rules` target and include it in the normal project test gate. It runs standard-library Python adapter tests and the existing OpenCode tests without requiring a live third-party model.

### 6.3 Honest adapter documentation

Documentation must distinguish:

- native host instruction loading;
- repository hooks or plugins;
- trust or approval required for those adapters;
- known timing or path-extraction limitations;
- the guaranteed root `AGENTS.md` baseline.

Do not promise that a scoped rule is present before a first mutation unless a black-box host probe proves it. That stronger question belongs to the R&D.

## 7. Verification

The future implementation is complete only when:

1. `CLAUDE.md` is still a symlink to `AGENTS.md`;
2. root `AGENTS.md` has the host-neutral section and code-review heading described above;
3. `.codex/rules.md` is deleted and no current instruction or bundled skill requires it;
4. `.cursor/rules` and `.claude/rules` contain the same rule IDs and equivalent activation metadata;
5. paired rule bodies contain no unique policy text;
6. the Codex, ZCode, and OpenCode adapter probes all activate `provider-proxy` for `internal/llm/openai.go`;
7. the contradictory one-way Rules Sync wording is gone;
8. `DESIGN.md` no longer uses Claude Desktop or Cursor as the normative definition of a component;
9. `DESIGN.md` retains every existing measurable component contract and adds the two cross-cutting principles;
10. historical design records remain historical and are not bulk-rewritten;
11. `make test-agent-rules`, `make test-opencode-rules`, `make docs-check`, `make test`, and `make lint` are green;
12. a size report records the standing `AGENTS.md` plus `DESIGN.md` baseline, but this task makes no unsupported claim that context was reduced.

## 8. Migration order

The implementation should be one focused documentation-and-adapter change:

1. add the mirror and adapter regression tests, reproducing the current `provider-proxy` omission;
2. fix YAML-list parsing until those tests pass;
3. make the rule bodies and ownership wording symmetrical;
4. update `AGENTS.md` and `DESIGN.md`;
5. delete `.codex/rules.md` and update current docs and the bundled skill;
6. run the targeted adapter and documentation checks;
7. run the normal repository test and lint gates;
8. cross-review the resulting diff with a fresh blind brief.

Do not combine this work with a canonical-directory migration or a `DESIGN.md` split.

## 9. Non-goals

- choosing `.coddy/rules`, `.agents/rules`, `.cursor/rules`, or another path as canonical;
- inventing a neutral manifest or frontmatter schema;
- removing supported vendor or provider integrations;
- deleting the Codex hook before its behavior is researched;
- splitting root `DESIGN.md` before cross-host nested-document delivery is proven;
- moving model rosters into the repository;
- rewriting historical plans, old branch names, or past review records;
- implementing the changes in this specification-writing task.

## 10. Follow-up R&D

The separate [rule and policy delivery R&D](rule-policy-delivery-rnd.md) owns the architectural questions this conservative change leaves open. Its output is a decision record and a separately reviewed implementation proposal. No later migration may cite this plan as proof that a proposed host mechanism works.
