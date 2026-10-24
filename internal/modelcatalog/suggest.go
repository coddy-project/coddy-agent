package modelcatalog

import "github.com/EvilFreelancer/coddy-agent/internal/config"

// Suggestion is what the settings screen offers a models[] row from one
// catalog entry.
type Suggestion struct {
	// ContextWindow is the catalog's window for max_context_tokens: the part a
	// prompt may take, limit.input when the catalog states one, else
	// limit.context. A window the provider's own listing reports wins over it
	// on the settings screen.
	ContextWindow int `json:"context_window,omitempty"`
	// MaxOutputTokens is limit.output as the catalog states it.
	MaxOutputTokens int `json:"max_output_tokens,omitempty"`
	// MaxTokens is the max_tokens the row is offered, 0 when none fits.
	MaxTokens int `json:"max_tokens,omitempty"`
	// Multimodal reports that the model reads images.
	Multimodal bool `json:"multimodal"`
}

// Suggest turns an entry into the values a row is offered. reportedWindow is
// the window the provider's listing reports for the model (0 when none), and
// thresholdPercent the compaction threshold (compaction.threshold_percent).
//
// max_tokens is as large as an answer may be while every request still fits.
// A model with a separate budget for its input (limit.input) answers within
// limit.output whatever the prompt holds. A model whose prompt and answer
// share one window gets at most the part of the window above the compaction
// threshold: before a compaction the prompt takes up to threshold_percent of
// it, and Anthropic refuses a request whose prompt and max_tokens together
// exceed the window. At the default 80 that is a fifth of the window.
func Suggest(e Entry, reportedWindow, thresholdPercent int) Suggestion {
	s := Suggestion{
		ContextWindow:   e.Context,
		MaxOutputTokens: e.Output,
		Multimodal:      e.Image,
	}
	if e.Input > 0 {
		s.ContextWindow = e.Input
	}
	if e.Output <= 0 {
		return s
	}
	if e.Input > 0 {
		s.MaxTokens = e.Output
		return s
	}
	window := s.ContextWindow
	if reportedWindow > 0 {
		window = reportedWindow
	}
	if thresholdPercent <= 0 || thresholdPercent > 100 {
		thresholdPercent = config.CompactionDefaultThresholdPercent
	}
	s.MaxTokens = min(e.Output, window*(100-thresholdPercent)/100)
	return s
}
