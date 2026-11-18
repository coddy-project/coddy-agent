//go:build desktop && freebsd

package desktop

import (
	"encoding/binary"
	"math"
	"unsafe"

	"golang.org/x/sys/unix"
)

// FreeBSD answers for a process with struct kinfo_proc over the
// kern.proc.pid.<pid> sysctl; there is no /proc to read unless somebody
// mounted one, and a desktop machine usually has not.
//
// Only two fields of that structure are wanted, and they sit behind a fixed
// prologue the ABI has kept for twenty years: two ints, then eight kernel
// pointers, then ki_pid and ki_ppid as consecutive 32-bit pids. The offset is
// computed from the pointer width rather than written down, so the 32-bit
// architectures land on it too.
//
// The read checks itself rather than trusting that layout: the structure
// reports its own size in its first field, and the pid at the computed offset
// must be the pid that was asked for. If either disagrees - because the
// prologue moved in some future release - parentOf answers "unknown" and the
// walk up the process tree ends, which costs the frameless window and nothing
// else.
const (
	kinfoProcPidOffset  = 8 + 8*int(unsafe.Sizeof(uintptr(0)))
	kinfoProcPpidOffset = kinfoProcPidOffset + 4
)

func parentOf(pid int) (int, bool) {
	buf, err := unix.SysctlRaw("kern.proc.pid", pid)
	if err != nil || len(buf) < kinfoProcPpidOffset+4 {
		return 0, false
	}
	// ki_structsize is the size the kernel wrote, which has to cover the two
	// fields below and fit in what came back. The pid check after it is the
	// strong one; this only rejects a reply that is not a kinfo_proc at all.
	size := int(binary.NativeEndian.Uint32(buf[0:4]))
	if size < kinfoProcPpidOffset+4 || size > len(buf) {
		return 0, false
	}
	if readPid(buf[kinfoProcPidOffset:]) != pid {
		return 0, false
	}
	parent := readPid(buf[kinfoProcPpidOffset:])
	if parent <= 0 {
		return 0, false
	}
	return parent, true
}

// readPid reads one pid_t. The type is signed, and every pid this walk can
// use is above zero, so a word with the top bit set is not an answer - it is
// a sign that the structure is not the one these offsets were written for.
func readPid(b []byte) int {
	v := binary.NativeEndian.Uint32(b)
	if v > math.MaxInt32 {
		return -1
	}
	return int(v)
}
