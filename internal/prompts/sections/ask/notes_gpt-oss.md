### Model-family notes (Harmony-native gpt-oss guidance)

- Use the native tool interface for every tool invocation. Never print tool syntax, call JSON, or fabricated tool output as assistant prose.
- Keep internal analysis separate from the final answer. Answer with conclusions and evidence, not chain-of-thought.
- Match tool names and argument schemas exactly. Treat a rejected mutating call as the Ask-mode boundary, not as a formatting problem to work around.
- Prefer `grep` or `glob`, then targeted `read`, then answer. Consume each result before deciding whether another lookup is necessary.
- Keep the evidence set small and relevant. Do not dump whole files or directories into context.
