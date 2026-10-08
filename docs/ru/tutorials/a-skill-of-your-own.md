# Собственный скил

Скил - файл `SKILL.md` с frontmatter и телом с инструкциями; он становится слэш-командой во всех интерфейсах, а его тело попадает к модели только при вызове. Справочник - [Скилы](../features/skills.md), особенно разделы [Как написать свой скил](../features/skills.md#свой-скил) и [Поддерживаемые форматы файлов](../features/skills.md#поддерживаемые-форматы-файлов).

1. **Создайте пакет.** Один скил на каталог в `~/.coddy/skills/`; имя каталога становится слэш-командой, поэтому держите `name` равным ему. `description` - строка, которую показывают каталог скилов и страница "Настройки", а необязательную `version` выводит `coddy skills list`, и по ней определяются обновления скилов, установленных из источника.

   ```markdown
   ---
   name: release-notes
   description: Turns the commits since the last tag into release notes grouped by area.
   version: 1.0.0
   ---

   # Release notes

   Run `git describe --tags --abbrev=0` to find the last tag, then `git log <tag>..HEAD --oneline`.
   Group the commits by scope, drop merge commits, write one line per change in the imperative
   mood, and end with a "Breaking changes" section, or "None" when there are none.
   ```

   Сохранённый как `~/.coddy/skills/release-notes/SKILL.md`, скил лежит в третьей из четырёх папок, которые Coddy читает всегда. По порядку, от низшего приоритета к высшему, это `~/.agents/skills/` (ваша, общая с `npx skills` и другими агентами), `.agents/skills/` проекта, `~/.coddy/skills/` и `.coddy/skills/` проекта; при совпадении имён двух скилов побеждает более поздняя папка, а любую другую папку можно добавить в `skills.dirs` в `config.yaml` - она идёт после четырёх и сильнее их.

2. **Найдите его в каталоге.** `coddy skills list` печатает корни поиска и таблицу со скилом, его версией, состоянием `enabled` или `disabled` и описанием.

   ```bash
   coddy skills list
   ```

3. **Найдите его среди слэш-команд.** Наберите `/` в первой строке редактора консоли, и среди подсказок будет `/release-notes`; поле ввода веб-интерфейса берёт его из `GET /coddy/slash-commands`, а ACP-редактор получает его в `available_commands_update` после `session/new`. Когда сообщение содержит `/release-notes`, тело скила один раз записывается в это сообщение как вложение после вашего текста, и модель читает его там и в этом ходе, и в следующих; транскрипт показывает сообщение так, как вы его набрали. Если `skills.auto_discovery` оставлен по умолчанию, модель может и сама подтянуть скил через инструмент `load_skill`, когда запрос подходит под описание.

4. **Протестируйте его.** Загрузчик заново сканирует `skills.dirs` при каждом промпте, так что правьте, сохраняйте и отправляйте снова; перезапуск не нужен. Режим печати даёт быстрый цикл проверки в репозитории с тегами.

   ```bash
   cd ~/src/my-project
   coddy -p "/release-notes for everything since the last tag"
   ```

5. **Выключите его, не удаляя.** `coddy skills disable release-notes` сохраняет файлы и убирает команду; `coddy skills enable release-notes` возвращает её. Состояние хранится в `~/.coddy/skills/.disabled`, по одному имени на строку.

   ```bash
   coddy skills disable release-notes
   coddy skills enable release-notes
   ```

6. **Поделитесь им.** Опубликуйте каталог в репозитории GitHub. Другие устанавливают его командой `coddy skills add owner/repo` с последующей `coddy skills sync` или через `coddy plugin install owner/repo`, а с карточкой на [skills.sh](https://skills.sh) его находит `npx skills find`.

<!-- docsgen:source sha256=fa2296da2ace6387 -->
