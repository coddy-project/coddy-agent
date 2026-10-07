# Explicit reasoning-off capability

## Goal

Replace the automatic `off` inference for NeuralDeep Qwen3 models with an explicit per-model capability. Reasoning remains enabled by default, and a model exposes `off` only when its configuration sets `allow_reasoning_off: true`.

## Scope

The setting belongs to `models[]`, rather than `providers[]`. One provider profile can serve models with different reasoning controls, and the person who configures a logical model is responsible for confirming that its deployment honours the provider-specific request shape.

The configuration field is:

```yaml
models:
  - model: neuraldeep/qwen3.8-27b
    reasoning_levels: [low, medium, high]
    allow_reasoning_off: true
```

It is a boolean and defaults to `false`. Omitting it must preserve ordinary reasoning-level detection without exposing `off`.

## Configuration and settings UI

`config.ModelEntry` gains `AllowReasoningOff bool` with YAML key `allow_reasoning_off`. The JSON schema, UI schema, sample configuration and configuration reference describe it as an advanced per-model control.

The logical-model form renders it as a boolean switch in the Reasoning fieldset. Its copy makes the risk explicit: it only enables the selector and does not prove that the configured upstream honours the request. The switch defaults to disabled for newly created model entries and for existing configurations that omit the key.

## Choice resolution

`ReasoningLevelsFor` continues to return only real provider/model tiers. `ReasoningChoicesFor` appends the pseudo-level `off` only when both conditions hold:

1. the model resolves at least one reasoning tier;
2. `allow_reasoning_off` is true.

The existing automatic `openai` or `neuraldeep` Qwen3 inference is removed. An explicit empty `reasoning_levels: []` still disables the selector altogether, including `off`.

The current request mappings remain unchanged once `off` is selected. Qwen3 uses `chat_template_kwargs.enable_thinking: false`, Anthropic omits the thinking block, and Codex maps the pseudo-level to `none`. The configuration switch is the operator's acknowledgement that the selected provider/model deployment supports this mapping.

## API and client behavior

`GET /v1/models` must publish the effective selectable choices, including `off` only for a model that enables `allow_reasoning_off`. Thus the web UI selector of a new chat shows the item directly from the models response.

Session snapshots, slash-command choices, ACP config options, session PATCH, prompt metadata and `switch_model` continue to validate through `ReasoningChoicesFor`. A configuration reload changes the choices for later reads. An existing stored session with `SelectedReasoning: off` on a model whose flag is now disabled falls back to the model default as the current invalid-level handling already requires.

The web UI's session-snapshot compatibility path remains harmless but is no longer needed to make a newly configured model expose `off`.

## Tests

Tests must cover these outcomes before implementation:

- a Qwen3 NeuralDeep model does not offer `off` by default;
- setting `allow_reasoning_off: true` adds `off` after its normal levels;
- an explicit empty reasoning-level list suppresses all choices even when the flag is true;
- the setting survives YAML, JSON DTO and Settings save round trips;
- `/v1/models` exposes `off` only when the flag is enabled;
- the session setter rejects `/nothink` when the flag is disabled and accepts it when enabled;
- the web UI shows `Off` for a new chat only when the model listing includes it;
- the Settings form renders and persists the advanced switch.

The current request-mapping tests for Qwen3, Anthropic and Codex remain, proving that opting in preserves their existing wire forms.

## Documentation and issue follow-up

Update `config.example.yaml`, the generated configuration reference source, the configuration guide, session-settings documentation, HTTP API narrative and the bundled configure skill. Regenerate documentation and verify the site schema and documentation layers.

After all checks and cross-review pass, add a final comment to issue #402 describing the new opt-in configuration and close it as completed.
