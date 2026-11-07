//go:build !linux && !android

package llm

import "os"

const kimiSecureStoreAvailable = false

func openKimiRoot(string, bool) (*os.Root, error) { return nil, ErrKimiStoreUnsupported }
func kimiCheckPrivateFile(os.FileInfo) error      { return ErrKimiStoreUnsupported }
func kimiTryFlock(*os.File) (bool, error)         { return false, ErrKimiStoreUnsupported }
func kimiUnlock(f *os.File)                       { _ = f.Close() }
