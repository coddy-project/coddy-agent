//go:build !memory

package config

// memoryBuild tells the shared tests that the memory validation is a stub.
const memoryBuild = false
