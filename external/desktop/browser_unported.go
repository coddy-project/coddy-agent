//go:build desktop && (!linux || android)

package desktop

// The window is ported to Linux and nothing else for now. The launcher, the
// shell and the port are plain Go and run anywhere, so this file keeps the
// package building on every platform; what is missing is the part that is
// genuinely per-platform and cannot be written blind:
//
//   - where each browser lives. On macOS a Chromium-family browser is a bundle
//     under /Applications, not a name on PATH, and --app has to reach the
//     executable inside it; on Windows it is a path under Program Files or the
//     user's AppData, found through the registry or a handful of known places.
//   - what a window costs there. macOS wants a signed and notarised
//     application before Gatekeeper will let a window open without a warning,
//     and Windows wants a signed binary before SmartScreen will; neither is
//     proven by a cross-compile, only by a build on that machine.
//
// A run on an unported platform is not an error: findBrowser finds no
// candidate, `auto` reports that it has no window to wait on, and Coddy serves
// with its address printed. Porting one of these is adding a file beside this
// one with that platform's list, and a test on a runner of that platform.
func browserCandidates() []string { return nil }
