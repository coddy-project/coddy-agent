---
description: The shipped build tag set is spelled in several places that move together
paths:
  - "Makefile"
  - "Dockerfile"
  - ".github/workflows/**"
  - "packaging/homebrew/**"
  - "docs/contributing/build.md"
  - "docs/getting-started/docker.md"
---
# Shipped build tags

The set of build tags that ships (`http ui scheduler memory cli gateway swarm metrics browser desktop` today) is not read from one place. A change to it - a tag added, removed or renamed - updates every copy in the same commit:

- `Makefile`, `FULL_TAGS` (and the examples in its header comment); `make test` and `TEST_TAG_SETS` derive from it;
- `Dockerfile`, `ARG BUILD_TAGS` (comma-separated, in its own order);
- `.github/workflows/docker-build-push.yaml`, `BUILD_TAGS`;
- `.github/workflows/release-binaries.yaml`, `GO_BUILD_TAGS`;
- `.github/workflows/tests-on-pr.yaml`, `PKG_TAGS` of the packages job (deliberately without `ui`, so the job needs no npm);
- `packaging/homebrew/coddy-formula.rb.tmpl`, the `tags` array (the cask installs a built archive and names no tags);
- `make check-windows`, which spells its tag combinations by hand;
- the build commands in `docs/contributing/build.md`, `README.md` and `AGENTS.md`;
- `docs/getting-started/docker.md`, the image's default set and its tag list under "What the image contains by default".

One platform carries more than the others, and still from that one set. The Cocoa/WKWebView window of `coddy desktop` needs no tag and no compiler: its constraint is `desktop && darwin`, and `external/desktop/native_stub.go` holds the exact complement, so every macOS build compiles the window and every other platform compiles the stub. It reaches Cocoa and WebKit through the Objective-C runtime (`github.com/ebitengine/purego`) rather than a C bridge, so `CGO_ENABLED=0 GOOS=darwin go build` produces it and the release cross-compiles the two darwin archives on the Linux runner beside every other target. `TestDarwinReleaseCrossCompilesWithTheRest` and `TestTheNativeWindowFollowsMacOS` (`external/desktop/build_tags_test.go`) fail when a tag of its own comes back into the Makefile, when the file partition stops following the platform, when the window gains a cgo file, or when the release stops cross-building darwin.

A subcommand that reaches the packages for the first time with a new tag also takes the man page and both completions in the same commit (the packaging invariants in `AGENTS.md`). Check the shipped combination itself, not only each tag alone: `make build TAGS="$(FULL_TAGS)"`, then the packages with the same tags.
