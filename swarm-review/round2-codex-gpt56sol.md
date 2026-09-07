## Q1: Blocker/major resolution
1. REMAINS (v2 §3.6) — composite identity is defined, but ACP `session/load` carries only bare `sessionId`, and console `--session-id` explicitly chooses the first match, so duplicate IDs remain ambiguous.
2. RESOLVED (v2 §3.7) — request-scoped `apiPathFor` routing replaces global base mutation.
3. REMAINS (v2 §3.3) — CWD-only queries still fail because non-node-matching nodes receive `q`, whose leaf filtering removes rows before the relay can inspect their CWD.
4. RESOLVED (v2 §§2.1, 3.3) — M1 removes cross-node pagination and exposes per-node drill-down.
5. REMAINS (v2 §3.4) — loop-header handling is fixed, but diamond deduplication by `(terminal relay UUID, session_id)` incorrectly collapses equal session IDs belonging to different agents below that relay.
6. REMAINS (v2 §3.2) — stable names and SSRF controls are resolved, but replacement authenticated by the same shared pairing credential lets any holder of that credential claim an existing node name.
7. RESOLVED (v2 §3.5) — pairing defaults, plaintext rejection, header replacement, query stripping, and secret-canary coverage address the credential-handling finding.
8. RESOLVED (v2 §4) — a live Playwright suite now covers routing, grouping, search, switching, deep links, and parallel turns.
9. REMAINS (v2 §5) — the vertical spike and per-stage OpenAPI are fixed, but Makefile/CI tag rows still arrive only at stage 9 instead of alongside each tagged package.
10. RESOLVED (v2 §§2.1, 3.7) — M1 no longer serves the existing SPA from the relay.

## Q2: Milestone split
Accept. M1 is a coherent, independently useful vertical slice and removes federation, topology, relay-hosted SPA, and global pagination from the critical path without forcing later wire-shape migration. Keeping `node_path`, `kind`, and transport concepts in M1 is appropriate preparation for M2.

## Q3: New blockers in v2
- The proposed ACP `_meta` label does not solve routing: repository `SessionLoadParams` has no `_meta`, and the returned bare `sessionId` cannot distinguish duplicate IDs.
- M2 diamond deduplication must use terminal node identity plus session ID—not terminal relay UUID plus session ID.
- CWD search must query unfiltered leaf rows or extend leaf search; matching CWD after filtered fan-out is ineffective.
- Node replacement needs node-specific proof or an explicit administrative takeover flow; equality with a fleet-wide pairing token is not credential-bound node ownership.

## Q4: Final verdict
REWORK — the architecture is much stronger, but composite identity still collapses at ACP/console load boundaries and two specified aggregation algorithms remain incorrect.
tokens used
115 701
