# OpenCode

Провайдер `type: opencode` даёт Coddy модели шлюза [OpenCode](https://opencode.ai) - подписки **Go** или **Console** (Zen) с оплатой по мере использования - по одному API-ключу. OpenCode здесь работает только как бэкенд. Во всех интерфейсах - в консоли, веб-интерфейсе, ACP-редакторах и Telegram-шлюзе - работает агент Coddy со своим системным промптом, инструментами, проверкой разрешений и циклом ReAct.

OpenCode собирает каталог моделей для кода (Grok, GPT, Claude, Qwen, GLM, Kimi, DeepSeek, MiniMax, MiMo, Gemini и остальные) и обслуживает каждую через подходящий ей протокол. Coddy выбирает протокол по модели и шлюзу. Модели Jev для принятия решений в Console используют отдельный System One API и не подходят для диалогов в Coddy.

## Получение ключа

1. Войдите в [консоль OpenCode](https://opencode.ai/console);
2. Оформите подписку **Go** (или **Go Plus**) либо добавьте кредиты для **Console** и укажите платёжные данные;
3. Скопируйте API-ключ.

## Использование ключа

Вставьте ключ в строку провайдера или сохраните его командой login.

```yaml
# yaml-language-server: $schema=https://coddy.dev/config.schema.json
providers:
  - name: opencode
    type: opencode
    api_key: "${OPENCODE_API_KEY}"   # or a literal key, or empty to read OPENCODE_API_KEY
    # api_base: https://opencode.ai/inference/v1   # Console; omit for the Go default
models:
  - model: opencode/glm-5.3
  - model: opencode/gpt-6-luna
  - model: opencode/claude-haiku-5-5
agent:
  model: opencode/glm-5.3
```

Если `api_key` пуст, Coddy сначала читает переменную окружения `OPENCODE_API_KEY` (обычное правило `NAME_API_KEY`, так что для строки с именем `opencode` используется `OPENCODE_API_KEY`), а если переменная не задана, использует ключ, сохранённый командой login. Форма `${OPENCODE_API_KEY}` раскрывается при загрузке файла; просто пустое значение читается в момент вызова.

### Сохранить ключ через login

```bash
coddy providers login opencode
```

Вход через `login` не использует браузерную авторизацию или авторизацию по коду устройства - ключ копируется из веб-консоли, поэтому его нужно вставить. Команда читает ключ из терминала (ввод не отображается) или из `--api-key`, сохраняет его в `$CODDY_HOME/providers/opencode/opencode-auth.json` с правами `0600` и, если не указан `--no-config`, записывает строку провайдера, модели шлюза и `agent.model` в `config.yaml`.

```text
$ coddy providers login opencode
Copy your API key from https://opencode.ai/console, then paste it here.
API key:
Stored the OpenCode key for https://opencode.ai/inference/go/v1 at /home/jane/.coddy/providers/opencode/opencode-auth.json
Updated /home/jane/.coddy/config.yaml: provider opencode, agent.model opencode/glm-5.3-flash, 39 models (opencode/glm-5.3-flash, opencode/kimi-k3, opencode/deepseek-v4-flash, ...)
```

Явный `api_key`, `api_key_command` или `OPENCODE_API_KEY` всё равно имеет приоритет над ключом, сохранённым командой login. `coddy providers logout opencode` удаляет сохранённый файл; ключ остаётся действительным, пока вы не отзовёте его в консоли. `coddy providers list` показывает, какой источник использует строка, с маскированием ключа.

## Два шлюза

`api_base` выбирает шлюз; оба принимают ключ одного формата, но ключ выпускается для одного из них.

| Шлюз | `api_base` | Что это |
|---------|-----------|------------|
| **Go** (по умолчанию) | `https://opencode.ai/inference/go/v1` | месячная подписка Go / Go Plus |
| **Console** | `https://opencode.ai/inference/v1` | кредиты с оплатой по мере использования (Zen) |

Пустой `api_base` означает Go. Если значение не соответствует ни одному из этих шлюзов, Coddy использует Go, так что ключ OpenCode никогда не уходит на произвольный хост. В веб-интерфейсе в разделе **Настройки → Провайдеры LLM** в строке провайдера вместо поля для ввода базового адреса отображается список выбора шлюза. `CODDY_OPENCODE_BASE_URL` переводит весь процесс на другой адрес - для стенда или теста.

## Протокол обмена для каждой модели

Шлюз OpenCode отдаёт разные модели по разным протоколам. Список `/models` содержит только id моделей и не указывает их протоколы. Coddy выбирает протокол по id модели.

| Протокол | Эндпоинт | Семейства моделей |
|----------|----------|----------------|
| OpenAI Responses | `/openai/v1/responses` | `grok-*`, `gpt-*`, `muse-*` |
| Anthropic | `/anthropic/v1/messages` | `claude-*`, `qwen*`, `minimax-m3` в Go |
| Google | `/google/v1beta/models/<id>:generateContent` | `gemini-*` |
| OpenAI-совместимый | `/openai/v1/chat/completions` | `glm-*`, `kimi-*`, `deepseek-*`, `longcat-*`, `mimo-*`, `hy*`, `space-bunny`, бесплатные модели для чата и неизвестные id |

Эти пути добавляются к `https://opencode.ai/inference/go` для Go или к `https://opencode.ai/inference` для Console. Адрес каталога - этот префикс с путём `/v1/models`. Console обслуживает `qwen3.8-max` и все модели `minimax-*` через Chat Completions; Go обслуживает `qwen3.8-max` и `minimax-m3` через Anthropic. Более старые модели MiniMax используют Chat Completions.

Если id модели отсутствует в этой таблице, Coddy использует Chat Completions. Соответствие следует [документации OpenCode Go](https://opencode.ai/v2/docs/console/go/) и [эндпоинтам моделей Console](https://opencode.ai/v2/docs/console/models/); когда OpenCode меняет протокол модели, обновите `opencodeProtocolForGateway` в `internal/llm/opencode.go`.

Уровни рассуждения передаются как `reasoning.effort` в OpenAI Responses, бюджет размышлений в Anthropic, `reasoning_effort` в OpenAI-совместимом протоколе или `thinkingConfig.thinkingLevel` в Google - по протоколу модели. Gemini передаёт поток через `:streamGenerateContent?alt=sse`; Coddy сохраняет подписанные части ответа между вызовами инструментов и частичный текст, если поток оборвался до получения причины завершения. Встроенные изображения поддерживаются; удалённые URL изображений обозначаются как вложения без скачивания.

## Обязательные заголовки клиента

Шлюз отслеживает злоупотребления и просит каждый клиент представляться и держать диалог на одном маршруте. Coddy отправляет в каждом запросе к OpenCode - на всех четырёх протоколах, для генерации ответов и для списка моделей:

- `User-Agent: coddy/<version>` - собственное имя, а не типовое имя SDK или HTTP-библиотеки;
- `x-opencode-session: <session id>` - стабильный id диалога, чтобы OpenCode мог оптимизировать маршрутизацию и повторно использовать свой кэш промптов. Вызов вне диалога (список моделей, проба `--dry-run`) отправляет сгенерированный id.

Оба заголовка добавляются к запросам, которые отправляет транспорт этой строки, поэтому запросы не обходят `providers[].proxy`.

Отправляется только ключ OpenCode этой строки. Ключи SDK по умолчанию, такие как `OPENAI_API_KEY` и `ANTHROPIC_API_KEY`, отбрасываются, а перенаправления на другой origin отклоняются до пересылки ключа.

## Прокси

`providers[].proxy` маршрутизирует каждый запрос к OpenCode - генерацию ответов по всем четырём протоколам, список моделей и запрос каталога при входе - точно так же, как у любого другого провайдера. При `inherit` используется прокси из окружения, при `none` запросы идут напрямую, а если указан URL, запросы идут через этот прокси. См. [Прокси провайдера](../getting-started/configuration.md#прокси-провайдера).

## Лимиты использования

OpenCode учитывает расход каждой модели по месячному долларовому бюджету (с подлимитами на 5 часов и на неделю), и вы можете следить за ним в [консоли](https://opencode.ai/console). Coddy не получает данные о расходе аккаунта OpenCode, поэтому панель расхода для строки `opencode` не показывается. Окно контекста модели берётся из `max_context_tokens`, затем из метаданных каталога, если они есть, затем используется 128000.

## Тестирование

- Модульные тесты. `internal/llm/opencode_test.go` проверяет точные пути запросов для обоих шлюзов, заголовки, изоляцию ключей, отказ от перенаправлений и сохранённые учётные данные. `opencode_google_test.go` покрывает диалоги Gemini с инструментами, подписанные части, расход и обрывы потока;
- `features/provider_proxy.feature` покрывает все четыре семейства API, получение списка моделей и обновление конфигурации при входе для каждого режима прокси;
- Транспорт Responses общий с бэкендом Codex (`internal/llm/responses_stream.go`); его покрывает `internal/llm/codex_test.go`.

## См. также

- [Конфигурация](../getting-started/configuration.md) - провайдеры, модели и порядок разрешения учётных данных;
- [Переменные окружения](../reference/environment-variables.md) - `OPENCODE_API_KEY`, `CODDY_OPENCODE_BASE_URL`;
- [Справочник конфигурации](../reference/config.md) - поля `providers[]` и `models[]`.

<!-- docsgen:source sha256=3eb38a9aa67c4596 -->
