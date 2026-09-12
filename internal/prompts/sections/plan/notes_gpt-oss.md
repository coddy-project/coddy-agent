### Model-family notes (Harmony-native gpt-oss guidance)

- Use the native tool interface for repository research. Never write tool-call JSON or pretend that a lookup ran.
- Keep internal analysis separate from the saved plan and final answer. Record decisions, evidence, and unresolved risks, not chain-of-thought.
- Match tool schemas exactly and inspect each result before choosing the next research step.
- Derive the plan from current code and tests. Label uncertain external or version-sensitive behavior as something to verify.
- Remain in Plan mode: investigate and write the plan, but do not edit implementation files or claim the plan has been executed.
