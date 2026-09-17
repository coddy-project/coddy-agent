package llm

import "fmt"

// RequestOptions are the generation options one caller asked for on one
// request - the max_tokens, temperature and reasoning_effort of a direct POST
// /v1/chat/completions - as opposed to the values configured on the model.
// The zero value asks for nothing and leaves the configured values in place.
type RequestOptions struct {
	// MaxTokens caps the output of this request; nil keeps the model's max_tokens.
	MaxTokens *int
	// Temperature replaces the model's temperature; nil keeps it. Zero is a
	// temperature like any other and is sent as 0, and a reasoning level next
	// to it does not keep it off the request: whether a model takes both is
	// the backend's to answer.
	Temperature *float64
	// ReasoningEffort is the reasoning level of this request, already checked
	// against the levels the model offers (the caller's own, or the model's
	// reasoning_default); empty sends no reasoning parameter at all, which is
	// not the same request as the level "none".
	ReasoningEffort string
}

// Validate reports the first option a provider of providerType cannot send as
// asked, so the caller refuses the request before anything reaches the
// provider instead of the value being dropped on the way.
func (o RequestOptions) Validate(providerType string) error {
	if o.MaxTokens != nil {
		if *o.MaxTokens < 1 {
			return fmt.Errorf("max_tokens must be a positive integer")
		}
		if providerType == "codex" {
			// The Codex backend answers "Unsupported parameter: max_output_tokens".
			return fmt.Errorf("max_tokens is not supported by a codex model: the Codex backend takes no output cap")
		}
	}
	if o.Temperature != nil {
		if providerType == "codex" {
			// The Codex backend answers "Unsupported parameter: temperature".
			return fmt.Errorf("temperature is not supported by a codex model: the Codex backend takes no temperature")
		}
		highest := 2.0
		if providerType == "anthropic" {
			highest = 1
		}
		if t := *o.Temperature; t < 0 || t > highest {
			return fmt.Errorf("temperature must be between 0 and %g", highest)
		}
	}
	if o.MaxTokens != nil && providerType == "anthropic" {
		// Extended thinking needs room for an answer beyond its budget, and the
		// provider raises a cap that leaves none; a cap the caller set is kept or
		// refused, never raised behind the caller's back.
		if budget := anthropicThinkingBudget(o.ReasoningEffort, *o.MaxTokens); budget >= anthropicMinThinkingBudget && int64(*o.MaxTokens) <= budget {
			return fmt.Errorf("max_tokens must exceed %d at reasoning level %q: extended thinking takes that budget first", budget, o.ReasoningEffort)
		}
	}
	return nil
}

// Apply writes the options onto the input a provider is built from. The input
// is the caller's own copy, so the configuration it was resolved from stays
// untouched.
func (o RequestOptions) Apply(in *ProviderInput) {
	if o.MaxTokens != nil {
		in.MaxTokens = *o.MaxTokens
	}
	if o.Temperature != nil {
		in.Temperature = *o.Temperature
		in.TemperatureSet = true
	}
	if o.ReasoningEffort != "" {
		in.ReasoningEffort = o.ReasoningEffort
	}
}
