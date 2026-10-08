package config

// BoolPtr returns a pointer to a copy of v. It is how a tri-state key of a
// configuration row is written in code: nil is "absent" and a pointer to false
// is a written false, the two states a plain bool cannot tell apart
// (ModelEntry.Multimodal, ModelEntry.AllowReasoningOff).
func BoolPtr(v bool) *bool { return &v }

// ModelMultimodal reports whether a model row accepts images: the one reader of
// models[].multimodal, so that no caller looks at the key itself. A written value
// is the answer, false included. An absent key reads as false for every provider
// type except coddy, where the remote's listing decides (listing.go): a model the
// listing does not carry, or no listing yet, reads no images. It is safe on a nil
// configuration and a nil row, and a missing row reads no images, the rule every
// surface applies to a model it cannot find.
func (c *Config) ModelMultimodal(ent *ModelEntry) bool {
	if ent == nil {
		return false
	}
	if ent.Multimodal != nil {
		return *ent.Multimodal
	}
	if lm, ok := c.listedFor(ent); ok {
		return lm.Multimodal
	}
	return false
}
