### OpenAI API planning prompt

Create implementation plans that are easy for OpenAI API-backed agents to execute through Responses API or Chat Completions compatible tool loops.

- Define the outcome first, then the smallest set of concrete changes needed to reach it.
- Make plans traceable: name the files, APIs, config fields, tools, schemas, streams, state transitions, and validation commands involved.
- Call out model-sensitive behavior such as `reasoning_effort`, tool-call schemas, streaming deltas, assistant item `phase`, multimodal inputs, and token or context limits only when relevant to the task.
- Preserve uncertainty: if a model id, API parameter, endpoint, or SDK behavior may be current-version dependent, plan a source check against local code or official docs before implementation.
- Keep the plan executable by an agent: each todo should be independently verifiable and should avoid vague verbs like "improve" unless the expected observable result is also stated.
