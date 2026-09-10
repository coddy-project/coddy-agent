# NeuralDeep syntax highlighting audit

## Method

Run on 2026-09-10 against NeuralDeep (`qwen3.8-27b-noreason`). Requested one short fenced example for each of 63 language names. No language label was forced: the labels below are what the model returned. Initial HTTP 429 responses were retried sequentially with pauses; all 63 initial examples were eventually received. One additional URQ clarification returned `UNKNOWN`.

Responses are replayed through the real React Markdown renderer in offline tests. The tests check preserved source text, highlighted token spans for supported labels, and safe plain text for unsupported labels. Generated programs were not executed or compiled, so this is a rendering / language-label audit, not a semantic correctness certification. No credentials are stored in the fixtures or this report.

## Results

- Before this expansion (PR already including Vue/PostCSS): 30 of 63 response blocks contained syntax spans.
- After: 60 of 63 contain syntax spans; the other three remain readable plain text.
- Dedicated extra grammars: GDScript, HLSL, WGSL, COBOL, T-SQL, VBA, plus 20 additional bundled grammars.
- PL/SQL, OpenCL, and CUDA use SQL, C, and C++ base grammars; dialect-specific constructs can remain uncolored.
- NeuralDeep labelled the Transact-SQL example `sql`; that response correctly uses generic SQL. Explicit `tsql`, `transactsql`, `transact-sql`, and `t-sql` labels select the dedicated grammar.
- The Prolog response contains only unquoted atoms and comments. The bundled Prolog grammar colors its comments but leaves those atoms plain; variables, strings, and numbers are separately checked with a deterministic test.
- UnrealScript and TADS have no configured grammar. URQ was confused with a GraphQL query; after specifying Universal RipSoft Quest / UrqW, the model returned `UNKNOWN`. The misleading initial URQ response is retained as a plain-text regression fixture and is not evidence of URQ language support. [UrqW project and documentation](https://urqw.github.io/UrqW/).

## Per-language observations

| Requested language | Returned fence | Before: token spans | After: token spans | Notes |
| --- | --- | ---: | ---: | --- |
| React (JSX) | `jsx` | 9 | 9 | Grammar enabled |
| Java | `java` | 16 | 16 | Grammar enabled |
| TypeScript | `typescript` | 20 | 20 | Grammar enabled |
| PHP | `php` | 14 | 14 | Grammar enabled |
| Python | `python` | 9 | 9 | Grammar enabled |
| C++ | `cpp` | 15 | 15 | Grammar enabled |
| x86 assembly (Intel syntax) | `asm` | 0 | 35 | Grammar enabled |
| C# | `csharp` | 7 | 7 | Grammar enabled |
| Go | `go` | 9 | 9 | Grammar enabled |
| Rust | `rust` | 11 | 11 | Grammar enabled |
| C | `c` | 15 | 15 | Grammar enabled |
| XHTML | `xhtml` | 23 | 23 | Grammar enabled |
| XML | `xml` | 22 | 22 | Grammar enabled |
| XSLT | `xslt` | 0 | 29 | Grammar enabled |
| XSL | `xml` | 19 | 19 | Grammar enabled |
| Pascal | `pascal` | 0 | 14 | Grammar enabled |
| Object Pascal | `pascal` | 0 | 15 | Grammar enabled |
| GameMaker Language (GML) | `gml` | 0 | 9 | Grammar enabled |
| Common Lisp | `lisp` | 0 | 9 | Grammar enabled |
| Prolog | `prolog` | 0 | 2 | Comments only in this sample; atoms remain plain |
| Delphi | `delphi` | 0 | 12 | Grammar enabled |
| Swift | `swift` | 15 | 15 | Grammar enabled |
| Dart | `dart` | 0 | 12 | Grammar enabled |
| Ruby | `ruby` | 6 | 6 | Grammar enabled |
| Kotlin | `kotlin` | 11 | 11 | Grammar enabled |
| Ada | `ada` | 0 | 13 | Grammar enabled |
| Objective-C | `objectivec` | 7 | 7 | Grammar enabled |
| VBA | `vba` | 0 | 13 | Grammar enabled |
| MATLAB | `matlab` | 0 | 8 | Grammar enabled |
| Elixir | `elixir` | 0 | 13 | Grammar enabled |
| Scala | `scala` | 0 | 12 | Grammar enabled |
| SQL | `sql` | 13 | 13 | Grammar enabled |
| Perl | `perl` | 12 | 12 | Grammar enabled |
| Fortran | `fortran` | 0 | 16 | Grammar enabled |
| COBOL | `cobol` | 0 | 16 | Grammar enabled |
| Scheme | `scheme` | 0 | 10 | Grammar enabled |
| Tcl | `tcl` | 0 | 11 | Grammar enabled |
| F# | `fsharp` | 0 | 9 | Grammar enabled |
| OCaml | `ocaml` | 0 | 13 | Grammar enabled |
| Visual Basic .NET | `vbnet` | 13 | 13 | Grammar enabled |
| Haskell | `haskell` | 0 | 10 | Grammar enabled |
| SVG | `svg` | 21 | 21 | Grammar enabled |
| GDScript (Godot) | `gdscript` | 0 | 10 | Grammar enabled |
| Lua | `lua` | 12 | 12 | Grammar enabled |
| Bash | `bash` | 11 | 11 | Grammar enabled |
| POSIX sh | `sh` | 9 | 9 | Grammar enabled |
| Zsh | `zsh` | 9 | 9 | Grammar enabled |
| PowerShell | `powershell` | 0 | 17 | Grammar enabled |
| R | `r` | 15 | 15 | Grammar enabled |
| Haxe | `haxe` | 0 | 14 | Grammar enabled |
| Oracle PL/SQL | `plsql` | 0 | 8 | Base-language highlighting only |
| Transact-SQL | `sql` | 15 | 15 | Model returned sql; explicit tsql also supported |
| UnrealScript | `unrealscript` | 0 | 0 | Plain text: no grammar |
| GLSL | `glsl` | 0 | 12 | Grammar enabled |
| HLSL | `hlsl` | 0 | 9 | Grammar enabled |
| OpenCL C | `opencl` | 0 | 13 | Base-language highlighting only |
| CUDA C++ | `cuda` | 0 | 12 | Base-language highlighting only |
| TADS 3 | `tads` | 0 | 0 | Plain text: no grammar |
| URQ | `urql` | 0 | 0 | Wrong language from model (GraphQL); clarification: UNKNOWN |
| WGSL | `wgsl` | 0 | 18 | Grammar enabled |
| Vue single-file component | `vue` | 20 | 20 | HTML/XML plus ordinary embedded JS/CSS |
| PostCSS | `postcss` | 6 | 6 | Grammar enabled |
| React TypeScript (TSX) | `tsx` | 10 | 10 | Grammar enabled |

## Regression fixtures and maintenance

- `external/ui/src/ui/markdown/neuraldeepLanguageFixtures.json`: the 63 original responses, including language labels and expected fallback behavior.
- `external/ui/src/ui/markdown/syntaxLanguages.test.tsx`: offline replay plus common alternative-label checks. No account or network is needed.
- `external/ui/src/ui/markdown/syntaxLanguages.ts`: one explicit grammar registry and alias map. Automatic detection remains disabled.
- `external/ui/src/ui/markdown/grammars/README.md`: upstream revisions and licenses for the six vendored grammars.

The production JS bundle increased from approximately 308.64 kB to 419.16 kB gzip (+110.52 kB, including the distributed license notices) with this broad grammar set. Grammars are bundled locally; rendering does not fetch grammar code from a CDN. The user requested no screenshot per language, so the audit is recorded as tests and this table instead.

## Validation

- UI suite: 1,169 tests passed across 158 files.
- Actual browser rendering: all 63 responses preserve source text; 60 contain colored token spans and three remain plain text. Fourteen theme/viewport combinations (seven themes, two widths) have no horizontal page overflow.
- Production Vite build and embedded asset synchronization pass; all six third-party license notices are retained in the emitted JavaScript.
- The repository-wide TypeScript check reports existing unrelated errors, including the unchanged Markdown component callback/component typings; the new registry, fixtures, and grammar declarations report no type errors.

- Targeted HTTP/UI embedding and BDD tests pass, and the `http,ui` Go binary builds.
- `make test` was rerun and stops on unchanged Windows failures in configuration log paths, transcript-export paths, swarm file permissions, and config staging/rollback. Later tag combinations are not reached.
