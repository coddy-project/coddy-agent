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

One platform adds a tag of its own on top of that set. `DARWIN_TAGS` (`Makefile`, printed by `make print-darwin-tags`) is `FULL_TAGS` plus `desktop.native`, the Cocoa/WKWebView window, which needs cgo and the macOS SDK and so cannot be in the cross-compiled set. `scripts/build-darwin-archives.sh` is the only thing that builds with it - the release workflow and the macOS job of `tests-on-pr.yaml` both call that script rather than spelling the tags again. It stays derived from `FULL_TAGS`, so a tag added to the shipped set reaches macOS by itself; `TestDarwinReleaseCarriesTheNativeWindow` (`external/desktop/build_tags_test.go`) fails when the two drift apart, when `desktop.native` leaks into `FULL_TAGS`, or when either workflow stops going through the script.

A subcommand that reaches the packages for the first time with a new tag also takes the man page and both completions in the same commit (the packaging invariants in `AGENTS.md`). Check the shipped combination itself, not only each tag alone: `make build TAGS="$(FULL_TAGS)"`, then the packages with the same tags.
