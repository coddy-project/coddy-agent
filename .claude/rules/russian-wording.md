---
description: Russian wording for agent-related text (UI dictionaries, documentation, answers)
---

# Russian wording

The Russian adjective for "agent" / "agentic" is **агентный** (агентный режим, агентная сессия,
агентные инструменты). Never write **агентский**: it is the wrong word for this meaning.

An agent that a session spawns is a **субагент** (субагент памяти, запускаю субагента, передать
работу субагенту). Never write **сабагент**: the prefix is the Russian "суб-", as in "субподрядчик".

A git worktree keeps its English name, **worktree** (отдельный worktree, работает в worktree). Never
translate it as **рабочее дерево** in any of its forms: nobody searches for the Russian calque, and the
English word is what git, the chips of the composer and the documentation all say.

These rules apply to everything written in Russian for this project: the `ru` dictionary under
`external/ui/src/ui/i18n/messages/`, Russian documentation and screenshots of the Russian interface,
commit messages, issue comments and answers to the operator. The dictionary is held to them by
`external/ui/src/ui/i18n/ruWording.test.ts`. Before finishing a change that touches Russian text, run
the three checks below and expect no hits other than the rule files, which name the wrong words in
order to forbid them (the test spells them with a character class, so it stays out of the result):

```bash
git grep -n -i 'агентск'
git grep -n -i 'сабагент'
git grep -n -i -e 'рабочее дерево' -e 'рабочего дерева' -e 'рабочем дереве' -e 'рабочие деревья'
```
