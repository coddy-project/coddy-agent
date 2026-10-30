# Prompt suggestions: Claude Code source comparison

Audited on 2026-10-30 against Claude Code **2.1.314**. Coddy adapts this behavior;
it does not reproduce every upstream gate, context rule or language heuristic.
The original PR did not pin an upstream version or source hash. This audit records
a verified comparison target, not proof of which version the original port used.

## Sources and reproducibility

- The public [anthropics/claude-code tree at `b3f291d`](https://github.com/anthropics/claude-code/tree/b3f291d131924810e2b7713ededc144843a229d5)
  publishes plugin contracts and supporting code, but not the suggestion service's implementation.
- The [prompt-suggest contract](https://github.com/anthropics/claude-code/blob/b3f291d131924810e2b7713ededc144843a229d5/mods/types/claude-code.d.ts#L7313)
  specifies dim text in an empty prompt box, accepted into an editable draft by Tab or Right.
  The [runtime contract](https://github.com/anthropics/claude-code/blob/b3f291d131924810e2b7713ededc144843a229d5/mods/types/claude-code.d.ts#L2545)
  refuses it while the box holds text, a turn runs, or the surface is headless.
- The implementation was read from the embedded JavaScript in the installed
  `claude.exe`, which reports `2.1.314`; the [official npm release](https://www.npmjs.com/package/@anthropic-ai/claude-code/v/2.1.314)
  reports the same version. The inspected executable's SHA-256 is
  `66e00df32e6981f686a91f67287fcc86e6e1b80ba4973a7b7c4aa43c59986cfa`.
  This is the distributed bundle, not an unpublished TypeScript checkout.
- The suggestion service imports `chunk-t3a9dack.js` for back-off and the fork helpers
  `QX`, `dP` / `_sr` from `chunk-vjqmsfbq.js`. Its own module starts at byte 266381093
  in that executable. Minified names and offsets apply only to this exact build.

The audit extracted only the pure cleaner, validator and word-count helpers into an
isolated JavaScript VM, with suppression telemetry replaced by a category recorder.
The application, authentication and model calls were not started. The same inputs
were then passed to `session.ValidatePromptSuggestion` in Go. The extracted bundle
is not vendored into Coddy.

## Behavior comparison

| Area | Claude Code 2.1.314 | Coddy |
| --- | --- | --- |
| Purpose | Predict the user's next message; silence when no obvious next step exists | Same intent; product names in the instruction are generalized |
| Display | Dim suggestion in the empty prompt box; Tab/Right accepts it for editing | Same interaction in the console and web composer |
| Side call | Fork of the current context; suggestion instruction appended as a user message | Separate provider call with a suggestion system instruction and framed recorded conversation |
| Tools and storage | Tool execution denied; `skipTranscript: true`, `skipCacheWrite: true` | No tools supplied; no messages appended to the transcript |
| Context | `QX` passes the system prompt, user/system context, tools and `forkContextMessages`; `_sr` continues that prefix | Last 12 user/assistant messages, each capped at 2000 runes; these bounds are Coddy choices |
| Output budget | Suggestion call supplies no `maxOutputTokens`; the fork's override stays unset | 1024 output tokens including reasoning; visible text is checked separately |
| First offer | Suppressed until there are at least two assistant messages | Available after the first completed textual reply, as requested for the console |
| Back-off constants | 20 unused suggestions, a 14-day grace period, every 10th eligible turn | Same constants |
| Back-off bookkeeping | Session-local unused streak and eligible-turn counter; persisted global marker, install `firstStartTime`, environment override | Home-wide streak/counter in `prompt-suggestions.json`, grace measured from first use of this feature |
| Other gates | Rollout/config/environment, main interactive query, focus, permissions/elicitations, plan mode, quota/credits and a cache-cold check at 10000 tokens | Local default-on config, active-turn and editor/session checks; Anthropic-specific rollout, billing and cache gates are not copied |
| Cancellation | Abort controller and another abort check before placing a suggestion | Request generation/session checks; cancellation on a new turn, session change or config reload |
| Visible length | Fewer than 100 JavaScript UTF-16 code units | Fewer than 100 Unicode runes |
| Language validation | CJK-aware word counting, localized labels and additional localized refusal expressions | Whitespace word counting, a smaller localized filter set and additional Russian one-word replies |
| Sharing | Suggestion belongs to its host session | Concurrent console/web readers share the same request and revision cache |

The former 64-token cap was **not** an upstream suggestion-service limit. It was a
Coddy port parameter that exhausted `opencode/beam-free`'s reasoning budget before
visible text appeared. Keep the 1024-token fix and first-reply behavior; copying the
upstream two-message gate back would undo the requested console behavior.

## Findings and checks

The original comparison had 21 exact matches out of 26 inputs. The five differences
were two intentionally accepted Russian replies (`да`, `продолжай`), two Chinese
inputs that Coddy's word counter does not support, and a fullwidth bracket wrapper.
That small fixture set is not evidence of complete validator equivalence.

The source check found missing wrapper pairs `［…］` and `〔…〕`: Coddy could offer a
wrapped meta-response as user input. Both now return `meta_wrapped`, with regression
cases in `TestValidatePromptSuggestionRefusesCategories`. After the fix, 22 of the
26 audit inputs match; the remaining four are the documented language differences.

The shared English cases cover cleaning, normal replies, slash commands, empty/meta
responses, errors, word/length bounds, multiple sentences, formatting, evaluative
text and assistant voice. Existing console Gherkin and lifecycle tests cover the
visible behavior and the intentional first-reply/Russian extensions.
