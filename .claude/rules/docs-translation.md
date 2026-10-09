---
description: The Russian documentation tree (docs/ru), what moves with an English page, the stamp, and the writing rules for Russian and English documentation
paths:
  - "docs/**"
  - "internal/config/config.schema.json"
  - "internal/docs/**"
  - "internal/docsgen/**"
---

# Documentation in two languages

The documentation exists in English (`docs/<group>/<page>.md`) and in Russian
(`docs/ru/<group>/<page>.md`, the same name and the same path under `docs/ru/`). The English page is
the source; the Russian page is its translation and moves with it in the same pull request. Every
surface shows the language it already knows the person uses (the web UI's locale, the terminal's
locale), so a page that is current in one language and stale in the other is a bug the reader sees.

## What moves together

| You change | You also change |
| --- | --- |
| an English page | its Russian page, then restamp it (below) |
| a page added to `docs/nav.yaml` | the Russian page and its entry in `docs/ru/nav.yaml` |
| a title or summary in `docs/nav.yaml` | the entry in `docs/ru/nav.yaml`: `title` and `summary` translated, `en:` set to the new English text |
| a `description` in `internal/config/config.schema.json` | its entry in `docs/ru/config-descriptions.yaml`: `en` set to the new English text, `ru` translated |
| the introduction of `docs/README.md` | the introduction of `docs/ru/README.md`, then restamp it |
| a page removed from `docs/nav.yaml` or moved | its Russian page removed or moved to the same new path, and its entry in `docs/ru/nav.yaml` with it |

Not translated: `docs/plans/` (design records), `docs/assets/` (shared: a Russian page embeds the same
files), and the pages the map lists from the repository root (`CONTRIBUTING.md`, `DESIGN.md`,
`AGENTS.md`). The changelog is generated in both languages.

## A Russian page

- **Headings**: the same headings as the English page, in the same order and at the same levels.
  Translate them; never add, drop, merge or split one. One address names a section in both languages
  because the Russian heading at a position answers to the English heading's anchor. A page whose
  English H1 is its title in `docs/nav.yaml` starts with its Russian title from `docs/ru/nav.yaml`.
- **Literal text stays literal**: code blocks, inline code, commands, flags, config keys, paths, file
  names, identifiers, HTTP routes, model and product names, and program output are copied unchanged.
  Comments inside code blocks stay as they are.
- **The web UI by its labels**: a control or a screen of the web UI is named the way the Russian
  interface names it; look the English label up in `external/ui/src/ui/i18n/messages/en.ts` and take
  the same key from `ru.ts`.
- **Links**: copy every link target as the English page writes it. `make docs` makes it right for the
  translation's folder: a link to another page keeps its relative path and gets the anchor GitHub
  gives the Russian heading (`../features/mcp.md#общие-серверы`); an image, a design record or a
  repository file gets the one more `../` the deeper folder needs. A link already written that way is
  left alone.
- **Generated blocks** keep their `<!-- docsgen:NAME:start -->` / `<!-- docsgen:NAME:end -->`
  markers; `make docs` fills them.
- **Captions**: the italic caption under a screenshot is translated; the screenshot is the English one.
- **The stamp**: the last line, `<!-- docsgen:source sha256=<16 hex> -->`, is the digest of the
  English page the translation follows. docsgen writes it; never edit it by hand.

## Commands

```bash
make docs                                   # both hubs, generated blocks, the Russian changelog, the links of docs/ru
make docs-check                             # what CI runs: both trees, the map, parity, stamps, links, typography
make docs-stamp PAGES="docs/ru/features/mcp.md"   # after the Russian page says what the English one says
make docs-stamp PAGES="..." UNCHANGED=1     # the English change needs no translation (a fixed typo)
```

`make docs-check` reports a stale Russian page with the command that shows what changed in the
English page since the translation was stamped. Restamp a page only after its translation says what
the English page says; a stamp over an untranslated change is a stale page CI cannot see, which is why
the stamp refuses a page whose translation did not change since `HEAD` unless `UNCHANGED=1` says so.
The pre-commit hook reports a stale stamp as a warning; CI fails on it.

## Writing Russian

The reader is a person who installs and runs Coddy, often a developer, sometimes not. Write the way
a careful Russian-speaking engineer explains the program to a colleague: plain, exact, active. A
translation renders the meaning of the English sentence, not its word order.

Typography and form (`make docs-check` enforces the dashes, the quotes and the horizontal rules outside code):
- a hyphen with spaces ` - ` instead of an em dash or an en dash, ranges as `10-15`;
- straight quotes `"..."`, never guillemets or curly quotes;
- no horizontal rules, and no emoji either (not checked: keep it by hand);
- a colon only to introduce a list; before a code block or a table end the sentence with a period;
- list items end with `;`, the last with `.`; keep a list a list and prose prose, as in the source;
- numbers in prose take a decimal comma (`1,5 МБ`), units in Russian (`КБ`, `МБ`, `мс`, `с`); values
  inside code stay as written.

Wording:
- no calques: "является", "данный", "осуществлять", "в рамках", "производить" (as "to perform"),
  "позволяет" where a direct verb fits, "при помощи", "на сегодняшний день";
- no stock phrases: "стоит отметить", "важно понимать", "таким образом", "не просто X, а Y",
  "X - это Y, а не Z";
- no hedging where the English states a fact: write "Coddy делает", not "Coddy может делать";
- verbs over nouns: "сжимает контекст", not "осуществляет сжатие контекста";
- one term for one concept within a page and across pages (the glossary below); do not vary terms
  for style;
- the reader is addressed as "вы" in lower case, instructions in the imperative ("запустите",
  "откройте");
- the paired Russian wording rule holds: "агентный", never "агентский"; "субагент", never "сабагент";
  "worktree", never a translation.

Glossary (English - Russian):
- agent - агент; agentic - агентный; subagent - субагент;
- session - сессия; conversation - диалог; chat (the web UI entity) - чат; turn - ход;
  message - сообщение; transcript - транскрипт;
- prompt - промпт; system prompt - системный промпт; composer - поле ввода;
- tool - инструмент; tool call - вызов инструмента; tool result - результат инструмента;
- permission - разрешение; approval - одобрение; permission mode - режим разрешений;
- the modes Agent, Plan and Ask - режимы "Агент", "План" и "Чат" (the identifiers `agent`, `plan`,
  `ask` stay in code);
- skill - скил; rule - правило; hook - хук; mention - упоминание;
- workspace - рабочая папка; background task - фоновая задача;
- scheduler - планировщик; job - задание; run (of a job or a task) - запуск;
- swarm - рой; node - узел; relay - релей; ring - кольцо;
- memory - память; compaction - сжатие контекста; context window - окно контекста;
- reasoning - рассуждение; reasoning level - уровень рассуждения;
- provider - провайдер; model - модель; environment - окружение; remote mode - удалённый режим;
- surface - интерфейс; console - консоль; web UI - веб-интерфейс; harness - харнес;
- gateway - шлюз (Telegram gateway - Telegram-шлюз); bot - бот;
- trust - доверие; trust receipt - расписка; sandbox - песочница; loop guard - защита от зацикливания;
- endpoint - эндпоинт; route - маршрут; request - запрос; response - ответ; streaming - потоковая передача;
- configuration, config - конфигурация; config file - файл конфигурации; key (of the config) - ключ;
- release - релиз; build - сборка; binary - бинарник; build tag - тег сборки;
- pull request - пул-реквест; issue - issue; worktree - worktree; daemon - демон; service - сервис;
- page - страница; section - раздел; screenshot - скриншот; caption - подпись.

Names stay in Latin script: Coddy, MCP, ACP, ReAct, SSE, OpenAI, Telegram, GitHub, Docker, systemd,
the names of models, commands and files.

## Writing English

The English pages follow `docs/contributing/documentation.md`. The same discipline applies: a hyphen
with spaces instead of an em dash, active voice with a named actor, specific statements instead of
vague ones, no filler ("It's worth noting", "Here's what"), no intensifiers (actually, really, very),
no "not X but Y" where a direct statement does. Write the English page so that it can be translated
sentence by sentence: one claim per sentence, the same term for the same thing.
