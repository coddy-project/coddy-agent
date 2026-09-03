//go:build windows

package platform

import "golang.org/x/sys/windows"

// Drives lists the roots of the logical drives mounted on the machine
// ("C:\\", "D:\\", ...), in letter order.
//
// It reads the drive bitmask instead of probing every letter with a stat call:
// touching an empty removable drive spins up the hardware and can raise the
// "There is no disk in the drive" dialog on the server's desktop.
func Drives() []string {
	mask, err := windows.GetLogicalDrives()
	if err != nil || mask == 0 {
		return nil
	}
	out := make([]string, 0, 26)
	for i := 0; i < 26; i++ {
		if mask&(1<<uint(i)) == 0 {
			continue
		}
		out = append(out, string(rune('A'+i))+`:\`)
	}
	return out
}
