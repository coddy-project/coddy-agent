//go:build !memory

package config

// MemoryFeatureCompiled is true when the binary is built with -tags memory (long-term memory copilot).
func MemoryFeatureCompiled() bool { return false }
