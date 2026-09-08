# Code review record

Four rounds against the implementation, by Codex (gpt-5.6-sol) and Cursor. Thirty-seven
findings, every one of them real; the interesting ones are collected here because several
describe mistakes worth not repeating.

| Round | Reviewer | Verdict | Found |
|---|---|---|---|
| 1 | Cursor | APPROVE_WITH_CHANGES | 9 |
| 2 | Codex | REWORK | 11 |
| 3 | Codex (re-check) | REWORK | 5 unresolved + 5 new |
| 4 | Cursor (fresh) | APPROVE_WITH_CHANGES | 7 |
| 4 | Codex (re-check) | REWORK | 4 unresolved + 2 weak tests |

## The ones worth remembering

**The documentation described controls the code did not have.** SSRF validation, the mount
refusing node eviction, and the hardened listener were all written down as true before they
were true. Writing a design and then implementing it leaves that gap open by default.

**A config block that is not in the DTO is deleted by the first save.** The swarm section
round-tripped through nothing, so one write from the settings screen would have taken the
relay's credentials with it. Nothing failed until somebody saved.

**Judging an escaped path is not judging the path.** `%2e%2e` passed every check of the
escaped form and became `..` the moment anything decoded it, which reached the node's control
plane. Decode, then judge.

**A watchdog can be worse than the failure it watches for.** The node-side liveness check
counted only inbound bytes, so a long streamed answer to a quiet client - the exact case the
tunnel exists for - would have been cut off at two and a half minutes.

**Breaking working state before building its replacement.** A renewal closed the live
transport and then tried to build a new one. Twice, in different files, the same shape.

**A test that passes for the wrong reason is worse than none.** Two here did: one compared
edge labels that happened to be distinct, the other started from an already-escaped string.
Both were rewritten and then checked by breaking the code they cover.
