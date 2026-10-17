# Homebrew

Coddy installs through Homebrew today, from a tap of its own, and is not yet in one of Homebrew's
own repositories. This page separates those two things: what a user runs now, and what the
submission to Homebrew needs.

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
Node, no build - and it is what our tap carries and the landing page installs. A cask is the right
shape for that job; it is only the wrong shape for Homebrew's own repository.

## Installing today

```bash
brew install coddy-project/coddy-agent/coddy
```

The tap [coddy-project/homebrew-coddy-agent](https://github.com/coddy-project/homebrew-coddy-agent) carries one
file, **`Casks/coddy.rb`**. Every release renders it with the checksums of its two macOS archives,
attaches it to the release as **`coddy.rb`** and commits it to the tap
(**`scripts/publish-homebrew-tap.sh`**), so **`brew upgrade --cask coddy`** sees each new release.
Given the full name, Homebrew adds the tap and trusts that one cask. Homebrew 7 loads nothing else
from a third-party tap until it is trusted; **`brew trust coddy-project/coddy-agent`** trusts the whole tap,
and the short name then works in every command.

The release asset on its own no longer installs. Outside **`HOMEBREW_DEVELOPER`** mode current
Homebrew loads a cask only out of a tap: it reads a URL as a cask name, so
**`brew install --cask <URL>`** ends at **`No Cask with this name exists`**
([#269](https://github.com/coddy-project/coddy-agent/issues/269)), and it rejects a local file with
**`Homebrew requires casks to be in a tap`**. The asset stays for anyone who wants the file; the tap
is the route.

After each release the **Homebrew tap** workflow runs that command on a macOS runner with current
Homebrew and checks the result; it also runs weekly and on pull requests that change the cask
([build.md](../contributing/build.md#homebrew-tap)).

The cask is written for Homebrew 7: the completions go through **`bash_completion`** and
**`zsh_completion`**, the one install step is a **`postflight_steps`** block rather than a Ruby
**`postflight`** block, and the **`url`** carries no **`verified:`**. That step removes
**`com.apple.quarantine`**, which Homebrew sets on every cask download: the binary is ad-hoc signed
and not notarised, and with the attribute in place Gatekeeper stops its first run. Notarisation needs
an Apple Developer ID, which the project does not have. The cask asks for macOS 12 or later, because
binaries built by Go 1.25 and later do not run on older releases.

The cask installs Coddy alone and recommends **`tmux`** in its caveats (**`brew install tmux`**).
A cask's **`depends_on`** has no optional form, so declaring tmux there would install it for
everyone, and Coddy runs without it; the deb and the rpm suggest it the same way.

The tap is also the documented home for anything Homebrew's official repositories decline
([Package Acceptance Policy](https://docs.brew.sh/Package-Acceptance-Policy#scope-and-third-party-taps)),
so it stays useful whatever the submission below decides.

## The formula

```bash
make brew-formula VERSION=1.0.13
```

**`scripts/build-homebrew-formula.sh`** fills **`packaging/homebrew/coddy-formula.rb.tmpl`** with the
version and the SHA-256 of that tag's source archive and writes **`dist/formula/coddy.rb`**. It
downloads the archive to hash it, so the version has to be a published tag.

The formula builds what the release binaries carry - **`http ui scheduler memory cli gateway swarm metrics`** - so a
`brew install` and a release archive are the same feature set. That is why **`node`** is a build
dependency beside **`go`**: the embedded SPA is generated rather than committed, and **`go:embed`**
needs those files to exist before the binary that carries them is linked. It runs **`npm ci`**, not
**`npm install`**, because [Acceptable Formulae](https://docs.brew.sh/Acceptable-Formulae#versioned-and-verifiable-sources)
forbids resolving a moving dependency set during a build.

The formula leaves **`tmux`** out. [Formula Cookbook](https://docs.brew.sh/Formula-Cookbook#specifying-other-formulae-as-dependencies)
does not allow the optional and recommended dependencies of a formula in homebrew/core, and keeps
caveats for what the Homebrew packaging itself changes, so the recommendation stays with the cask
and the pages here.

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
side is small: **`brew install coddy`** starts working on a Mac that has never added the tap, the docs lead with
it instead of the tap, and a formula install reports **`brew upgrade coddy`** from **`coddy update`** -
which it already does. How the users of the tap's cask move to the formula is decided with that
change.
