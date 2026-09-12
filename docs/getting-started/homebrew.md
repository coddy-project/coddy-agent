# Homebrew

Coddy installs through Homebrew today, and is not yet in one of Homebrew's own repositories. This
page separates those two things: what a user runs now, and what the submission to Homebrew needs.

## Which repository Coddy belongs in

Homebrew has two official taps and routes software between them by package type.
[Adding Software to Homebrew](https://docs.brew.sh/Adding-Software-to-Homebrew#choose-a-package-type)
sends open-source command-line software that Homebrew can build from source to **homebrew/core** as a
formula, and reserves **homebrew/cask** for native macOS applications and for proprietary or
binary-only software.

Coddy is MIT-licensed, command-line only, and builds from source with Go and Node, so the submission
target is **[homebrew/core](https://github.com/Homebrew/homebrew-core) as a formula**, not
homebrew/cask. [Acceptable Casks](https://docs.brew.sh/Acceptable-Casks#appropriate-package-type)
states the same rule from the other side, and adds that being turned down by homebrew/core does not
by itself make software eligible for homebrew/cask.

This is a correction to the original plan in
[#160](https://github.com/coddy-project/coddy-agent/issues/160), which aimed the submission at
homebrew/cask.

Our own **cask** does not go away. It installs the prebuilt macOS archives of a release - no Go, no
Node, no build - and it is what the release asset and the landing page point at. A cask is the right
shape for that job; it is only the wrong shape for Homebrew's own repository.

## Installing today

```bash
brew install --cask https://github.com/coddy-project/coddy-agent/releases/latest/download/coddy.rb
```

Every release publishes **`coddy.rb`** beside the archives, rendered with the checksums of the two
macOS archives of that same tag. This installs and upgrades by hand: Homebrew cannot see new
versions of a cask it did not get from a tap, so a new release means running the command again.

To let **`brew upgrade`** track releases before Coddy is in homebrew/core, put the same cask in a tap
of ours (`brew tap coddy-project/coddy`). That is a repository of its own and is not set up yet; a
tap is also the documented home for anything Homebrew's official repositories decline
([Package Acceptance Policy](https://docs.brew.sh/Package-Acceptance-Policy#scope-and-third-party-taps)).

## The formula

```bash
make brew-formula VERSION=1.0.13
```

**`scripts/build-homebrew-formula.sh`** fills **`packaging/homebrew/coddy-formula.rb.tmpl`** with the
version and the SHA-256 of that tag's source archive and writes **`dist/formula/coddy.rb`**. It
downloads the archive to hash it, so the version has to be a published tag.

The formula builds what the release binaries carry - **`http ui scheduler memory cli gateway swarm`** - so a
`brew install` and a release archive are the same feature set. That is why **`node`** is a build
dependency beside **`go`**: the embedded SPA is generated rather than committed, and **`go:embed`**
needs those files to exist before the binary that carries them is linked. It runs **`npm ci`**, not
**`npm install`**, because [Acceptable Formulae](https://docs.brew.sh/Acceptable-Formulae#versioned-and-verifiable-sources)
forbids resolving a moving dependency set during a build.

Its **`test do`** block runs **`coddy -v`**, **`coddy sessions list`** and **`coddy skills list`** -
three commands that need no network and no configuration, and that fail if the binary cannot reach
**`~/.coddy`**.

**`coddy update`** already refuses to overwrite a Homebrew-owned file and prints the brew command
instead, which is how Coddy satisfies [Acceptable Formulae's rule on self-updating
software](https://docs.brew.sh/Acceptable-Formulae#self-updating-software). A formula install lands in
the **`Cellar`** and is upgraded with **`brew upgrade coddy`**; the cask lands in the **`Caskroom`**
and takes **`brew upgrade --cask coddy`**. Coddy tells the two apart from the path of its own
executable - see [update.md](update.md#installations-owned-by-a-package-manager).

## Preflight

```bash
make brew-check VERSION=1.0.13
```

**`scripts/check-homebrew-submission.sh`** answers the parts of Homebrew's acceptance criteria that
can be read from outside: whether the **`coddy`** token is free in both repositories, whether the
repository clears the notability threshold, whether the release exists, and whether the rendered
formula parses, names that release, pins a checksum and carries no **`bottle`** block (BrewTestBot
adds those, submitters do not). It exits non-zero when something blocks the submission and prints the
macOS-only steps at the end.

### Notability is the open blocker

[Package Acceptance Policy](https://docs.brew.sh/Package-Acceptance-Policy#notability) sets two
thresholds, and which one applies depends on who opens the pull request:

| Submitter | Threshold |
|---|---|
| Anyone unaffiliated with the project | 30 forks, 30 watchers **or** 75 stars |
| The repository owner, submitting their own project | 90 forks, 90 watchers **or** 225 stars |

At 1.0.13 the repository has 139 stars, 14 forks and 2 watchers. That clears the first row and not
the second, so **a self-submission is not eligible yet** and a submission by someone else is. Run the
preflight for the current numbers; it does this arithmetic and says how far short the repository is.

Neither threshold is a promise. Homebrew's policy is explicit that meeting the documented criteria
does not guarantee acceptance, and equally that missing one criterion does not force a rejection:
where the metrics misrepresent actual use, the exception is something to argue in the pull request
rather than a box to tick.

## Submitting

Everything past this point needs a host with Homebrew installed, and macOS is the one to use:
**`brew audit`** and **`brew style`** are what the reviewers run, and the formula has to be proven on
the platform it is primarily for. The preflight prints these commands with the paths filled in.

```bash
brew tap --force homebrew/core
cd "$(brew --repository homebrew/core)"
git checkout -b coddy origin/HEAD
cp /path/to/coddy-agent/dist/formula/coddy.rb Formula/c/coddy.rb
brew install --build-from-source --verbose --debug coddy
brew test coddy
brew audit --strict --online --new coddy
brew style coddy
```

Then push the branch to a fork of **Homebrew/homebrew-core** and open the pull request against it.
The fork is of Homebrew's repository, not of ours;
[How to Open a Homebrew Pull Request](https://docs.brew.sh/How-To-Open-a-Homebrew-Pull-Request) is the
procedure Homebrew expects, and **`brew bump-formula-pr`** is for later version bumps, not for a new
formula.

[homebrew/core's CONTRIBUTING](https://github.com/Homebrew/homebrew-core/blob/master/CONTRIBUTING.md)
allows a pull request prepared with an AI or an LLM under conditions that shape how this work gets
handed over ([Responsible AI Usage](https://docs.brew.sh/Responsible-AI-Usage) has the reasoning):

- the pull request must disclose that an AI or LLM was used, and which tool or model;
- the submitter must have reviewed everything generated before asking Homebrew to review it;
- no commit may attribute the work to an AI or LLM as author, co-author, committer or signatory,
  including through an `Assisted-by` or `Co-developed-by` trailer;
- the submitter answers maintainer questions and review comments themselves, without an AI or LLM;
- a non-maintainer may have only one AI-assisted pull request open at a time.

So the formula, this page and the scripts around them are prepared here, and the submission itself is
opened and defended by a person. Make one pull request per package, and do not squash commits after
pushing an update to it.

## After acceptance

The formula's **`livecheck`** block tracks GitHub releases, so Homebrew's own automation opens the
version bumps and the submission is one-time work rather than per-release work. What changes on our
side is small: **`brew install coddy`** starts working by name, the docs stop pointing at the release
asset, and a formula install reports **`brew upgrade coddy`** from **`coddy update`** - which it
already does.
