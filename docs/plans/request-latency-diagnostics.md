# Request latency diagnostics

The operator approved instrumentation and a bounded synthetic experiment after the session investigation of 2026-10-31. This change measures causes before optimizing them. It changes neither context eviction/compaction policy nor the active model/reasoning configuration.

## Boundaries

`internal/llm` owns a context-scoped, bounded HTTP timing collector. Existing provider clients retain their proxy-aware shared transports and connection pools. With tracing absent the wrapper delegates immediately. With tracing present it composes `net/http/httptrace` callbacks and records numeric milestones only. Bodies, headers, URLs, credentials and socket addresses are never inspected or recorded. Missing observations remain absent. A snapshot holds at most eight request records plus total/dropped counts and does not retain a request or response.

The public API is `WithHTTPTrace(ctx) (context.Context, *HTTPTrace)` and `(*HTTPTrace).Snapshot() HTTPTraceSnapshot`. Each request records relative start time, known ContentLength (-1 unknown), optional SDK preparation/connection/write/first-byte milliseconds, response-header duration, response status/protocol and connection reuse. Codex marks the start of its SDK call after building parameters. Its SDK preparation interval includes JSON encoding and client preparation; it is not a pure serialization measurement. Redirects must not reuse a preparation timestamp as if each redirect were a fresh SDK encode.

The agent enables diagnostics only at its debug logging level. Session id and a process-monotonic call id correlate stage, completion, usage and dispatch records. Helpers outside `react.go` measure context refresh, outbound projection/pairing, meaningful first chunk, first reasoning/text/tool arrival, model-call time and completion-to-first-tool dispatch. Unavailable ACP/HTTP stages are not invented. Existing stream callback, cancellation, retry, prompt-cache and durability behavior stays unchanged.

## Experiment

A Go example under `examples/diagnostics/request-latency` loads an explicitly selected config read-only and creates providers through `llm.NewProvider`, preserving OAuth ownership and proxy routing. It generates neutral synthetic source records and one fixed analysis task, never reads a stored session, and prints metadata/timings only.

Live execution requires an explicit flag. The planner limits work to 12 requests, a finite total input budget and a per-call timeout. It disables provider retries and stops on the first refusal/error. Context size is varied with reasoning fixed; reasoning is varied with short context fixed. Adjacent identical requests observe cache reuse. The first sample of a unique synthetic prefix is not labelled as a proven cold-cache purge. Actual token usage and cache readings accompany the nominal fixture sizes.

## Verification

Tests precede production changes. Transport tests cover off-mode transparency, body/header identity, composed callbacks, proxy routing, cancellation/error, independent concurrent collectors and bounded immutable snapshots. Agent BDD and unit tests cover opt-in logging, correlation, stage availability and no prompt/result disclosure. Example tests cover the explicit live guard, fixed experimental factors, work/window limits and stop-on-error.

Run prompt-cache tests, full shipped-tag tests, race tests, lint and documentation checks. Update the English and Russian troubleshooting guide and the examples index. Review spec compliance and code quality independently. A live experiment must report its real limits; errors or unavailable comparisons are not a successful causal result.
