//go:build !windows

package platform

// Drives reports no drive roots. Outside Windows the filesystem has a single
// root ("/"), so there is no volume level above it to switch between.
func Drives() []string {
	return nil
}
