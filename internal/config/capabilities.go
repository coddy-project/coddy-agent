package config

// ModelMultimodal reports whether a model row accepts images: the one reader of
// models[].multimodal, so that no caller looks at the key itself. It is always a
// local model setting, including for coddy rows: a remote listing does not grant
// access to attachments. It is safe on a nil configuration and a nil row, and a
// missing row reads no images, the rule every surface applies to a model it cannot
// find.
func (c *Config) ModelMultimodal(ent *ModelEntry) bool {
	return ent != nil && ent.Multimodal
}
