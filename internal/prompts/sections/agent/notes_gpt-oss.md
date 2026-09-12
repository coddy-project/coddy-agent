### Model-family notes (Harmony-native gpt-oss guidance)

- Use the native tool interface for every tool invocation. Never print tool syntax, call JSON, or fabricated tool output as assistant prose.
- Keep internal analysis separate from the final answer. The final answer reports the result and verification without exposing chain-of-thought.
- Match tool names and argument schemas exactly, including required fields and JSON types. If a call fails, inspect the error before changing the call.
- Read the exact target before editing, use tool output as evidence, and verify every mutation. Never fill an evidence gap with an assumption.
- Keep the working context compact with targeted searches and reads, and use the persisted checklist to retain state across long tool loops.
