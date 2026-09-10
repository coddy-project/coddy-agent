# Vendored syntax grammars

These language definitions are listed by highlight.js in its supported-language catalog but are not bundled with the core npm package. Only grammar source is vendored; no build scripts or transitive tools are included. Revisions are pinned and each original license is stored beside its grammar.

| Grammar | Source revision | License | Local changes |
| --- | --- | --- | --- |
| gdscript | [4b584e9c](https://github.com/highlightjs/highlightjs-gdscript/blob/4b584e9c8853348d58eb23ad3e867b015a6a89d7/src/languages/gdscript.js) | [BSD-3-Clause](gdscript.LICENSE) | CommonJS export converted to ES module export; provenance header. |
| hlsl | [dabd22b9](https://github.com/highlightjs/highlightjs-hlsl/blob/dabd22b9a538cc2f76c9d4b21689366d1ccf4009/src/languages/hlsl.js) | [MIT](hlsl.LICENSE) | Provenance header only. |
| wgsl | [41818558](https://github.com/highlightjs/highlightjs-wgsl/blob/418185581f59e67c257a1d2fa43ec3f9103382df/src/languages/wgsl.js) | [Apache-2.0](wgsl.LICENSE) | Provenance header only. |
| tsql | [27b7b121](https://github.com/highlightjs/highlightjs-tsql/blob/27b7b12146ec75859ca5836dbb6b3904441f7057/src/languages/tsql.js) | [BSD-3-Clause](tsql.LICENSE) | Provenance header only. |
| vba | [ec940fd5](https://github.com/dullin/highlightjs-vba/blob/ec940fd58be5cd9b5612a66e6641c84b7cd0c6e9/src/vba.js) | [MIT](vba.LICENSE) | Provenance header only. |
| cobol | [f439e861](https://github.com/otterkit/highlightjs-cobol/blob/f439e861eb8c0deee01b2b61035c272e9320e774/src/cobol.js) | [Apache-2.0](cobol.LICENSE) | Provenance header only. |

The GDScript package metadata says MIT, but the source repository LICENSE at the pinned revision is BSD-3-Clause; the actual repository license is retained here. TypeScript declarations describe each module as a highlight.js LanguageFn.

After updating a revision, retain notices, review the grammar source, and replay the offline language fixtures. These grammars provide lexical highlighting, not compiler validation; their keyword coverage may lag newer language versions.

The Vite output footer includes the complete license notices in the distributed app.js; source comments alone are removed by the production minifier.

Trailing whitespace in the vendored JavaScript is normalized; grammar behavior is otherwise unchanged.
