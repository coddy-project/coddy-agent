//go:build http && unix

package httpserver

import "syscall"

const workspaceNonblockFlag = syscall.O_NONBLOCK
