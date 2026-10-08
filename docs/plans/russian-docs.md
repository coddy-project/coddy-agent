# Plan: the documentation in Russian (issue #203)

Status: v2 after one cross-review round of the plan (coddy on neuraldeep/qwen3.8-27b-noreason,
coddy on devin/swe-2, coddy on codex/gpt-5.6-sol, cursor agent on auto; all four answered, verdict
"approve with changes"). Round-1 changes are marked `[rev]`. Implemented on this branch; the
implementation notes are in section 6.
Branch: `feat/russian-docs`.

## 1. Goal

Every page of the documentation exists in Russian, inside this repository, in a file with the same
name and the same path as the English page, and stays current: a change that moves an English page
cannot land without the Russian page moving with it. The person reads the documentation in the
language Coddy already knows they use (the web UI's locale, the terminal's locale), with no
language switcher in the reader, and the search works in that language.

Issue #203 first asked for a separate, simplified Russian resource. The operator narrowed it on
2026-10-08: a translation of the documentation itself, built into the workflow, with the site
following the same tree.

## 2. Decisions

### 2.1 Tree

- `docs/ru/<group>/<page>.md` is the Russian page of `docs/<group>/<page>.md`, for every page of
  `docs/nav.yaml` that lives under `docs/`. The three pages the map lists from the repository root
  (`../CONTRIBUTING.md`, `../DESIGN.md`, `../AGENTS.md`) stay English: they are contributor and
  agent material, and the Russian hub links to them as they are.
- `docs/ru/nav.yaml` carries the Russian title and summary of every group and page, keyed by group
  id and page path, each entry with the English title and summary it translates (`en:`), so a
  changed English title shows up as stale like a changed page `[rev]`. Order and membership come
  from `docs/nav.yaml` alone.
- `docs/ru/README.md` is the Russian hub: a translated introduction, stamped like a page `[rev]`,
  plus the generated `nav` block.
- `docs/ru/config-descriptions.yaml` carries the Russian text of every `description` of
  `internal/config/config.schema.json`, each entry with the English text it translates.
- `docs/plans/` is not translated. `docs/assets/` is shared: Russian pages embed the same
  screenshots (`../../assets/...`).

### 2.2 What a Russian page is

- The same headings as the English page, in the same order and at the same levels. Headings are
  translated; their count and levels are not touched. This is what lets one address name a section
  in both languages (2.4).
- Code blocks, commands, flags, config keys, paths, identifiers, HTTP routes and literal program
  output stay as they are. Prose, captions, table text and comments in prose are translated. A
  control of the web UI is named by its label in `external/ui/src/ui/i18n/messages/ru.ts`.
- Links between pages keep the English relative path (`../features/mcp.md`) and use the anchors
  GitHub gives the Russian headings (`#общие-серверы`), written unescaped. Assets and repository
  files get one more `../`.
- Generated blocks keep their markers; `make docs` fills them.
- The last line is a stamp written by docsgen: `<!-- docsgen:source sha256=<16 hex> -->`, the
  digest of the English page the translation follows (LF line ends, trailing blanks trimmed, the
  bodies of generated blocks removed, so a regenerated table does not age a translation).

### 2.3 Checks (`make docs-check`: CI's Documentation job and the pre-commit hook)

The source set is the pages of `docs/nav.yaml` under `docs/` (so neither `docs/plans/` nor
`docs/ru/`), plus the hub `docs/README.md`. `CheckNav` and the hub and llms walkers skip `docs/ru/`;
the checks below own it `[rev]`.

1. Every page of the source set has its Russian page, and `docs/ru/` holds nothing else but those
   pages, `README.md`, `nav.yaml` and `config-descriptions.yaml`.
2. `docs/ru/nav.yaml` names exactly the groups and pages of `docs/nav.yaml`, each with a one-line
   title and summary; a Russian page starts with `# <its Russian title>`.
3. Heading parity: the Russian page has the English page's sequence of heading levels, and no
   Russian heading's own anchor equals the shared anchor of another heading (the shared anchor wins
   in `Resolve`, so such a heading could not be addressed by its own) `[rev]`.
4. Freshness: the stamp equals the digest of the current English page; a mismatch fails on its own.
   As a best effort, the report names the last commit whose English page had the stamped digest and
   the command that shows what changed since (`git diff <commit> -- docs/<page>`); a shallow clone
   (CI) gets a note instead `[rev]`. The report says to update the translation; it does not offer the
   restamp as the next step `[rev]`.
5. Russian typography outside code: no em or en dash, no guillemets or curly quotes, no horizontal
   rule, and none of the words `.claude/rules/russian-wording.md` forbids. The release notes of the
   Russian changelog are exempt (they are copied as published) `[rev]`.
6. Every schema description has a Russian entry whose `en` equals the current English text, and
   every `docs/ru/nav.yaml` entry's `en` equals the current English title and summary `[rev]`.
7. Links and anchors: the existing check already walks `docs/ru`; it learns to unescape a fragment.

The pre-commit hook reports a stale stamp as a warning and CI fails on it, so a work-in-progress
commit of an English page is allowed and the pull request is not `[rev]`.

`make docs` regenerates the Russian hub, the Russian generated blocks (config tables with the
Russian descriptions and headers, the same CLI help screens) and the Russian changelog, offline from
the English changelog file: a Russian title and introduction, the release sections copied as they
are `[rev]`. It never writes a stamp. A stamp is written by
`go run ./cmd/docsgen -stamp docs/ru/<page>.md ...` (`make docs-stamp PAGES="..."`), only for pages
named, and it refuses a page whose Russian text (stamp and generated blocks aside) is the same as at
`HEAD`, unless `-unchanged` says the English change needs no Russian one, in which case it prints
the English diff it accepts `[rev]`. The changelog carries no stamp.

### 2.4 One address for a section in both languages

A section's canonical anchor is the GitHub anchor of the English heading. The Russian heading at
the same position answers to it. So `coddy:features/mcp#shared-servers`, `@coddy:...`,
`#/docs/...` in the web UI and `coddy docs show ...` name the same section whatever the language,
the ASCII-only grammar of `@` mentions stays as it is, and a link the agent writes in English opens
the Russian section for a Russian reader.

Inside the binary, a Russian page's own links (`#общие-серверы`) are rewritten to the canonical
anchor of the target page. `Resolve` accepts a section by either anchor. Headings served to a client
carry the Russian text and the canonical anchor.

### 2.5 The library and the search (`internal/docs`)

- `docs/embed.go` embeds `ru/nav.yaml`, `ru/config-descriptions.yaml` is not needed at runtime,
  and the Russian group folders.
- `docs.Default()` stays the English library; `docs.For(lang)` returns the library of a language,
  English for any language without a translation. A Russian page missing from the tree falls back
  to the English page (the check makes that a development-only state).
- Repository paths of images and files are computed from the file's real path, not its slug.
- The tokenizer folds `ё` to `е`, drops Russian stop words and stems Cyrillic words with the
  Snowball Russian algorithm (implemented here, no new dependency); Latin words keep the current
  English stemming. Both kinds of words live in one index, so a Russian page is found by its
  Russian words and by the identifiers it quotes.
- Budgets that are about how much text reaches a reader (snippets, `Read`, a mention) count
  characters, not bytes, so a Russian page is not cut at half the length of an English one.

### 2.6 Which language a surface shows

One normaliser, `docs.Lang`, reads every spelling of a locale (`ru`, `ru-RU`, `ru_RU.UTF-8`,
`ru_RU.UTF-8@mod`; `C`, `POSIX`, empty and untranslated languages are English) for HTTP, the CLI and
the console alike `[rev]`.

- **Web UI** (and the Telegram Mini App, which is the web UI): the app's locale (`?lang=`, the
  `coddy_ui_lang` cookie, `navigator.language`) goes as `lang=` on `/coddy/docs`, `/coddy/docs/page`,
  `/coddy/docs/search` and `/coddy/mentions`; the reader refetches when the locale changes. A turn
  sent from the web UI carries the locale in its metadata (`metadata.lang`, next to
  `metadata.surface`) `[rev]`.
- **HTTP**: those routes take `lang`; a language with no translation, or none, is English. The
  responses say which language was served (`lang`).
- **Console** (F1, `/docs`, `@coddy:` completion, and its turns) and **`coddy docs`**: `CODDY_LANG`
  when set, then the terminal's locale, `LC_ALL`, then `LC_MESSAGES`, then `LANG`; `coddy docs --lang
  <en|ru>` overrides it for scripts `[rev]`. `CODDY_LANG` is also the override on a system without
  those variables (Windows).
- **The turn language** `[rev]`: a turn carries the language of the surface that started it, the way
  it carries `SurfaceSystemPrompt` (`session.PromptRunOpts`, held on the session state for the turn,
  never persisted): the web UI's `metadata.lang`, the console's locale, a Telegram user's
  `language_code`. A surface that knows none leaves it empty.
- **Precedence** `[rev]`, the same for a `@coddy:` mention in a prompt and for both agent tools:
  an explicit `lang` argument of the tool, then the turn language, then the script of the text the
  person wrote (a query, or the prompt without its mentions; Cyrillic means Russian), then English.
  The tool descriptions say so.

### 2.7 The site (`coddy-project.github.io`)

- `coddy.dev/ru/docs/<slug>` opens `docs/ru/<slug>.md` on GitHub, `coddy.dev/ru/docs/<slug>.md` its
  raw Markdown, `coddy.dev/ru/docs` the Russian hub; the generated `docs-redirect.js` handles both
  prefixes. A canonical (English) anchor in a Russian address is mapped to the Russian one through
  a generated `docs-anchors-ru.json`, fetched only for such an address.
- `ru/llms.txt` and `ru/llms-full.txt` index the Russian tree; the root `llms.txt` names them.
- The Russian landing (`scripts/build_ru.py`) rewrites `coddy.dev/docs/...` links to
  `coddy.dev/ru/docs/...`, and both landings' "Documentation" link goes to the hub address instead
  of the GitHub tree.
- `make site-docs` writes the new files. The site commit follows the merge, as today.

### 2.8 Workflow

- Step 10 of the paired workflow rule gains a bullet: a change to an English page carries the same
  change into its Russian page in the same pull request, restamped; a new page brings its Russian
  page and its `docs/ru/nav.yaml` entry; a schema description brings its Russian entry.
- The contract surfaces the change itself touches `[rev]`: `external/httpserver/openapi.go` and
  `docs/reference/http-api.md` (`lang` on `/coddy/docs*` and `/coddy/mentions`, `metadata.lang`),
  `docs/reference/tools.md` (`lang` of both tools), `docs/reference/environment-variables.md`
  (`CODDY_LANG`, `LC_ALL`, `LC_MESSAGES`, `LANG`), `packaging/man/coddy.1` and both completions
  (`coddy docs --lang`), `docs/reference/cli.md` regenerated, `docs/features/built-in-docs.md` (how
  the language is chosen), each with its Russian page; `make test-cache` because the tool
  definitions change.
- A new paired rule, `docs-translation` (`.claude/rules/`, `.cursor/rules/`), scoped to `docs/**`
  and the schema: the tree, the conventions of 2.2, the commands, and the writing rules for both
  languages, including the glossary that matches the web UI's Russian dictionary.
- `docs/contributing/documentation.md` (and its Russian page) gets a Translations section;
  `AGENTS.md` names `docs/ru` in the documentation contract.

## 3. Tests

- `features/builtin_docs.feature`: the reader, the HTTP routes, the tools and `coddy docs` in
  Russian (happy path).
- `internal/docs`: the stemmer, ё folding, Russian stop words, canonical anchors, link rewriting
  of a Russian page, fallback, `For`, character budgets, and every page of the map present in
  Russian in the embedded tree (a Russian group folder forgotten in `docs/embed.go` would otherwise
  ship English silently) `[rev]`.
- The language precedence: tool argument, turn language, script, English `[rev]`.
- `internal/docsgen`: each check of 2.3 on a fixture tree, the stamp, the Russian hub and
  generated blocks, the site files.
- `external/ui`: the reader sends the locale and refetches on a change.
- `cmd/coddy`: `--lang` and the locale variables.

## 4. Notes from the review

- The Snowball Russian stemmer is implemented here against hand-traced cases. It cuts some forms of
  a word further than others (архив is арх, архивы is архив), so the index also keeps a Russian word
  as written next to its stem, and a query stem finds it exactly or as a prefix. A conformance test
  against the Snowball project's Russian vocabulary needs that data file in the repository `[rev]`.
- Budgets count characters: bytes undercount Russian text and characters overcount it in tokens,
  both within about 1.5 times; characters keep a Russian page from being cut at half the length.
- The initial translation of every page lands in this pull request, produced by translation
  subagents against the conventions of 2.2 and checked by the checks of 2.3, then reviewed.

## 5. Out of scope

- Russian screenshots of the Russian interface (the pages reuse the English ones).
- The console's own strings (the console has no localization; only the documentation it shows
  follows the locale).
- Languages beyond English and Russian; the mechanism takes a third one by adding a tree and a map.
- The release notes stay as published, in English, inside the Russian changelog.
- Reordering sections of equal level in a translation, and explicit ids on Russian headings: the
  positional rule is enough while a translation keeps the order of its source.

## 6. Implementation notes

- **Links of a translation.** A translator copies the link targets of the English page, and
  `make docs` rewrites them for the translation's folder (`internal/docsgen/localize.go`): the
  meaning of the link on the English page decides (a documentation page becomes its translation,
  anything else the same file one `../` deeper), the anchor of an English heading becomes the anchor
  of the translated heading at the same position, and a link already written for the translation is
  left alone. The translators never computed a Russian anchor by hand.
- **The H1 rule.** Twenty-one English pages have an H1 longer than their title in the map; a
  translation keeps the translated H1 there, and only a page whose English H1 is its map title must
  start with its Russian map title.
- **Link targets with code in their text.** A link whose text is a code span (a file name in
  backticks) was neither rewritten by the binary's reader nor localized: both now find `](` outside
  code spans, whatever the link text holds.
- **The index keeps Russian word forms.** Snowball stems some forms of a word further than others
  (архив is арх, архивы is архив), so the index also keeps a Russian word as written next to its stem.
- **The initial translation.** Twenty translation subagents, one unit of about 87 KB each, the long
  pages split at section or table-row boundaries; each unit was checked mechanically against its
  source (headings, code blocks, inline code, link targets, comments, table rows, typography) before
  it was assembled. The English defects the translators reported were verified and fixed in both
  languages in the same pull request.

