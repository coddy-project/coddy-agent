# R&D: rule and policy delivery across coding-agent hosts

Status: approved research task. This record was split from the vendor-neutral instruction cleanup after review showed that the proposed neutral `.agents` authoring format and manifest were unsupported assumptions.

## 1. Question

Where should Coddy's repository-wide and path-scoped contributor policy live, and how should each supported coding-agent host receive it without silent omissions, manual drift, or dependence on Anthropic-specific paths?

Do not assume that `.agents/rules`, a generated manifest, a project hook, or a plugin works merely because the repository contains code or documentation for it. Test observable model context and tool behavior in each target host.

The research must explicitly determine whether `.coddy/rules` is the simplest repository-owned canonical source for this repository, or whether using it would merely move the same adapter problem into another vendor-specific directory.

## 2. Facts that start the research

- Coddy supports `.coddy/rules` and gives it the highest project-directory precedence;
- Coddy also supports `.agents/rules`, but that is not evidence that external hosts consume it natively;
- `.agents/policy` and `.agents/rules-manifest.json` have no current consumer and must not be proposed as standards without evidence;
- `.codex/rules.md` has no runtime consumer and is a negative control, not a candidate delivery mechanism; the probe may create it in an isolated fixture after the tracked index has been deleted;
- root `AGENTS.md` is the only Codex instruction source guaranteed by the operator for this work;
- the current Codex and ZCode hooks and the OpenCode plugin are repository-specific adapters whose real guarantees need black-box probes;
- Cursor and Claude Code have tracked native rule representations, but their exact supported versions and activation timing are external facts;
- Coddy intentionally reads the first populated project rule directory rather than merging all vendor trees.

## 3. Target hosts

Test the concrete versions used by the project and record those versions:

- Coddy;
- Cursor Agent;
- Claude Code;
- OpenAI Codex through the supported project/operator surface; when operator policy forbids the standalone Codex CLI, the probe must run through Coddy rather than bypass that policy;
- OpenCode;
- ZCode.

A vendor-specific mechanism is an adapter. It is not proof of a cross-host standard.

## 4. Mechanisms to probe

- root `AGENTS.md`;
- the root `CLAUDE.md` compatibility symlink;
- nested `AGENTS.md` and `DESIGN.md`;
- `.coddy/rules`;
- `.agents/rules`;
- `.cursor/rules`;
- `.claude/rules`;
- `.codex/rules.md` as a negative control;
- the current Codex and ZCode hooks;
- the current OpenCode plugin;
- native host mechanisms documented by the exact tested versions.

Do not add a new manifest or authoring format during the research. A prototype may be built in scratch space only after the native capability matrix is complete and only to compare a justified option.

## 5. Observable probe matrix

Use canary text that is visible in the host transcript or captured model request. A file existing on disk is not a passing result.

For each host and mechanism, verify:

- always-on delivery;
- path-scoped delivery for a read;
- path-scoped delivery for a create, edit, delete, and rename;
- whether delivery happens before the first mutation, after it, or only on a later turn;
- multiple paths in one operation;
- relative and absolute paths;
- worktrees and symlinks;
- Windows separators, drive roots, and case behavior where the host runs there;
- an opaque shell command that writes a file;
- compaction and context restoration;
- a new session, clear, and workspace switch;
- a rule changed while the session is running;
- malformed and missing rules;
- required trust or approval and the user-visible failure mode;
- duplicate delivery and context size.

If a host cannot expose a path before a mutation, record the limitation rather than pretending a parser can solve it. Determine which invariants must therefore remain in root instructions, code, or CI.

## 6. Options to compare

### 6.1 `.coddy/rules` as repository-owned canonical policy

Measure:

- direct support and behavior in Coddy;
- what must be generated or adapted for Cursor and Claude Code;
- how Codex, OpenCode, and ZCode could receive it;
- whether the `.coddy` namespace is acceptable for the Coddy repository but too product-specific as a general convention;
- migration and backwards-compatibility cost.

### 6.2 Root and nested `AGENTS.md` / `DESIGN.md` as the common baseline

Measure whether universal policy can live in root documents and path-local guidance in nested documents. Identify which hosts load nested documents reliably, whether they do so before mutation, and the standing-context cost when only root loading is guaranteed.

### 6.3 Current `.cursor` and `.claude` representations with strict parity

Evaluate this as the low-risk baseline:

- native behavior;
- manual drift cost;
- whether a repository-owned parity/generation tool is sufficient;
- how the current bridges handle frontmatter and lifecycle;
- the `provider-proxy` YAML-list omission as a regression probe.

### 6.4 A neutral authoring format plus generated adapters

Evaluate only after the native mechanisms are known. Account for:

- no current consumer for the source format;
- stale generated files during an interactive session;
- native mirror syntax and generated markers;
- trust and setup friction;
- deletion and rename handling;
- compaction lifecycle;
- context determinism;
- whether the custom infrastructure is more complex than `.coddy/rules` plus thin adapters.

Reject this option if its benefits do not exceed the simpler alternatives.

## 7. Decision criteria

- works on a fresh clone without hidden setup where the host allows it;
- does not silently lose critical policy;
- provides policy before a sensitive mutation or leaves enforcement outside model context;
- gives one policy change one authoritative authoring location;
- retains useful native host behavior;
- keeps context bounded and deterministic;
- can be tested in CI or by a reproducible manual gate;
- preserves Coddy's compatibility with arbitrary user repositories;
- makes trust and project-executed code explicit;
- accounts for Linux, macOS, and Windows behavior;
- is simpler than the alternatives it replaces.

## 8. Deliverables

1. A versioned host capability matrix with exact host versions;
2. raw probe inputs, outputs, and captured request/transcript evidence;
3. a list of confirmed current defects and unsupported assumptions;
4. a decision record comparing all four options;
5. a recommended canonical location, or an explicit decision to keep the current layout;
6. a migration and rollback outline;
7. a separate implementation specification reviewed through `/crossreview`.

The capability matrix must state `supported`, `unsupported`, `requires trust/setup`, or `unknown`. Unknown remains a blocker and is not converted into optimistic prose.

## 9. Non-goals

- migrating rules during the research;
- deleting working compatibility files before the decision;
- inventing a manifest or schema before proving it is needed;
- changing Coddy's public rule-discovery precedence;
- merging all rule directories in Coddy;
- treating historical model, CLI, or quota availability as architecture;
- implementing the vendor-neutral cleanup plan in this research task.

## 10. Exit criteria

The R&D is complete only when:

- every target host has an observable passing probe or a documented unsupported result for each relevant mechanism;
- root and nested document behavior is known;
- pre-mutation timing and compaction behavior are known;
- the recommended architecture is justified against the simpler `.coddy/rules` option;
- no recommendation depends on `.codex/rules.md`;
- a maintainer can reproduce the decisive probes without the original research session;
- the final decision has passed an independent `/crossreview`.
