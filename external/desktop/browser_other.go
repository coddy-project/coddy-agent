//go:build desktop && !darwin && !windows && (!linux || android)

package desktop

// Every platform without a browser list of its own: the BSDs, Plan 9, and
// Android, which satisfies the linux constraint but has no browser to start
// this way under Termux.
//
// The launcher, the shell and the port are plain Go and run anywhere, so a run
// here is not an error: findBrowser finds no candidate, `auto` reports that it
// has no window to wait on, and Coddy serves with its address printed. Naming
// a browser by hand still works - CODDY_DESKTOP_BROWSER is read before this
// list is consulted - so a machine with a Chromium-family browser somewhere
// unusual gets a window today.
//
// Porting one of these is adding a file beside this one with that platform's
// list, and a test on a runner of that platform.
func browserCandidates() []string { return nil }
