# Проект, подготовленный для агентов

Репозиторий может нести всё, что нужно агенту для хорошей работы в нём - соглашения, рабочие процессы, серверы инструментов и защитные ограничения. Всё это лежит в файлах внутри чекаута, поэтому версионируется вместе с кодом и приходит с каждым клоном. Ниже дерево, которое получится после шагов этой страницы, и назначение каждого файла.

```text
my-project/
  AGENTS.md                       # the second document layer, in every prompt
  .coddy/
    rules/
      go-style.md                 # a rule, attached once a Go file comes into play
    skills/
      release-notes/
        SKILL.md                  # /release-notes, project-local
    mcp.json                      # project MCP servers, held until approved
    hooks.json                    # lifecycle hooks, held until approved
    hooks/
      gofmt.sh                    # the script hooks.json runs
    agents/
      reviewer.md                 # a subagent definition, held until approved
```

1. **`AGENTS.md` в корне.** Он попадает в каждый промпт безусловно, как второй слой документов после ваших собственных ([AGENTS.md и DESIGN.md](../features/rules.md#agentsmd-и-designmd)), и так же попадает лежащий рядом `DESIGN.md`. Держите его короткой картой - где что лежит, как собирать и тестировать, что должно входить в изменение. Вложенные файлы `AGENTS.md` глубже в дереве читаются по требованию, в момент, когда инструмент заходит в их папку, и обходом дерева их не ищут.

2. **Правила в `.coddy/rules/`.** Файл `.md` - правило Claude Code (его ограничивают `paths`, а без них оно действует всегда); файл `.mdc` - правило Cursor (`description`, `globs`, `alwaysApply`). Оба диалекта принимаются в любой папке правил ([Форматы файлов правил](../features/rules.md#форматы-файлов-правил)). Coddy читает одну папку проекта - `.coddy/rules`, если она есть в проекте, иначе первую из `.agents/rules`, `.cursor/rules`, `.claude/rules` и `.codex/rules`, в которой есть файл правила, поэтому правила, продублированные для других агентов, не загружаются дважды ([Поиск правил](../features/rules.md#обнаружение)). Правило с условием попадает к модели один раз, вместе с результатом инструмента или сообщением, которое первым затронуло подходящий файл.

   Всё в этом руководстве говорит от имени проекта. То, чего *вы* хотите от каждой сессии в любом репозитории (язык ответов, команды, которые нельзя запускать никогда), относится к вам, а не к этому чекауту. Запишите это в `~/.coddy/AGENTS.md` и `~/.coddy/rules/`, где оно действует везде и ни с кем его не нужно согласовывать ([Ваши собственные инструкции и правила](../features/rules.md#ваши-собственные-инструкции-и-правила)).

   ```markdown
   ---
   description: Go coding standards
   paths:
     - "**/*.go"
   ---

   Write comments in English. Wrap errors with fmt.Errorf("context: %w", err).
   Run `make lint` before you report a change as done.
   ```

   `coddy rules list` печатает каталог правил с исходной папкой, диалектом, в котором прочитан каждый файл, признаком того, входит ли правило в каждый промпт, и тем, что активирует остальные.

3. **Скил в `.coddy/skills/<name>/SKILL.md`.** `.coddy/skills` - самая сильная из четырёх папок, которые Coddy читает всегда, поэтому скил проекта перекрывает одноимённый из `~/.coddy/skills`, из `.agents/skills` проекта или из `~/.agents/skills`; после неё идёт только каталог, который вы добавили в `skills.dirs` ([Скилы](../features/skills.md)). Имя каталога становится слэш-командой, здесь это `/release-notes`; как написать сам файл, показывает последний рецепт на этой странице. Запустите `coddy skills list` внутри проекта, чтобы увидеть скил в списке для этого каталога.

4. **MCP-серверы в `.coddy/mcp.json`** ([MCP-серверы](../features/mcp.md)). Файл в формате Cursor - один объект `mcpServers` с ключами по именам серверов; запись только с URL описывает сервер streamable HTTP, а `${CWD}` в `args` означает рабочую папку сессии.

   ```json
   {
     "mcpServers": {
       "filesystem": {
         "command": "npx",
         "args": ["-y", "@modelcontextprotocol/server-filesystem", "${CWD}"]
       },
       "docs": { "url": "https://mcp.example.com/mcp" }
     }
   }
   ```

   Файл приходит с чекаутом, а значит, команду, которую запускает сессия, выбирал бы репозиторий, а не оператор. При политике по умолчанию `mcp.project_trust: ask` эти серверы не запускаются, пока их не одобрят для этой рабочей папки ([Доверие рабочей папке для проектных серверов](../features/mcp.md#доверие-к-рабочей-папке-для-серверов-проекта)).

   ```bash
   coddy mcp list                # scope, trust state and the command of every merged server
   coddy mcp trust filesystem    # prints the declaration once more, then records the receipt
   ```

   Расписка в `~/.coddy/mcp-trust.json` привязана к рабочей папке и к дайджесту объявления, поэтому после правки записи одобрение спросят снова; `config.yaml` и `~/.coddy/mcp.json` принадлежат вам, и проверка доверия к ним не применяется.

5. **Хуки в `.coddy/hooks.json`** ([Хуки](../features/hooks.md)). Файл в формате Claude Code. Этот хук запускает `gofmt` после каждой правки и сообщает модели, что изменил.

   ```json
   {
     "hooks": {
       "PostToolUse": [
         {
           "matcher": "edit|write|apply_patch",
           "hooks": [{ "type": "command", "command": ".coddy/hooks/gofmt.sh" }]
         }
       ]
     }
   }
   ```

   ```bash
   #!/bin/sh
   # .coddy/hooks/gofmt.sh - runs in the session cwd with the call's JSON on stdin
   path=$(jq -r '.tool_input.path // ""')
   case "$path" in
     *.go)
       if out=$(gofmt -l "$path" 2>&1) && [ -n "$out" ]; then
         gofmt -w "$path"
         printf '{"hookSpecificOutput":{"hookEventName":"PostToolUse","additionalContext":"gofmt reformatted %s"}}' "$path"
       fi
       ;;
   esac
   exit 0
   ```

   Хуки работают с вашими правами, до любого запроса разрешения, поэтому файл, пришедший с клоном, разбирается и показывается в списке, но ничего не запускает, пока его не одобрят ([Файлы проекта и доверие](../features/hooks.md#файлы-проекта-и-доверие)); первый ход, который его обнаружит, записывает в сессию уведомление. Проверьте команды и выполните следующее.

   ```bash
   coddy hooks list
   coddy hooks trust .coddy/hooks.json
   ```

6. **Субагент в `.coddy/agents/reviewer.md`** ([Файлы определений](../features/subagents.md#файлы-определений)). Обязателен только `description`; `name` по умолчанию берётся из имени файла без расширения. `tools`, `disallowed_tools` и `permission_mode` могут только сузить то, что мог бы делать родитель, а тело файла задаёт роль дочернего агента.

   ```markdown
   ---
   name: reviewer
   description: Reviews a diff or a module for defects and reports findings with file paths and lines.
   tools: read, glob, grep, print_tree, run_command
   permission_mode: ask
   max_turns: 20
   ---
   You review code. Read before you judge, cite `path:line` for every finding,
   separate defects from style remarks, and end with a short verdict.
   ```

   Проектные определения подчиняются `subagents.project_trust` и получают собственные расписки ([Области и доверие к проекту](../features/subagents.md#области-и-доверие-к-проекту)).

   ```bash
   coddy agents list
   coddy agents trust reviewer
   ```

7. **Одобряйте один раз на клон или задайте политику.** Три одобрения выше действуют для одной рабочей папки и одного дайджеста файла, поэтому свежий клон или изменённый файл снова требуют одобрения. Для чекаута, который принадлежит вам, `mcp.project_trust`, `hooks.project_trust` и `subagents.project_trust` со значением `allow` в `config.yaml` обходятся без расписок, а `deny` вообще не читает файлы проекта. С `--remote` одобрения принадлежат серверу, где будет выполняться код (следующий рецепт).

<!-- docsgen:source sha256=914f5291a36063d8 -->
