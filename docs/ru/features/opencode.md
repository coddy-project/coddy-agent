# OpenCode

Провайдер `type: opencode` даёт Coddy модели шлюза [OpenCode](https://opencode.ai) - подписки **Go** или **Console** (Zen) с оплатой по мере использования - по одному API-ключу. OpenCode здесь только бэкенд: агент остаётся собственным агентом Coddy, со своим системным промптом, своими инструментами, своим шлюзом разрешений и своим циклом ReAct на каждом интерфейсе - в консоли, в веб-интерфейсе, в ACP-редакторах и в Telegram-шлюзе.

OpenCode собирает каталог моделей для кода (Grok, GPT, Claude, Qwen, GLM, Kimi, DeepSeek, MiniMax, MiMo, Gemini и остальные) и обслуживает каждую через подходящий ей протокол. Coddy выбирает протокол по модели и шлюзу. Модели решений Jev в Console используют отдельный System One API и не подходят для диалогов в Coddy.

## Получение ключа

1. Войдите в [консоль OpenCode](https://opencode.ai/console).
2. Оформите подписку **Go** (или **Go Plus**) либо добавьте кредиты для **Console** и укажите платёжные данные.
3. Скопируйте API-ключ.

## Использование ключа

Вставьте ключ в строку провайдера или позвольте команде login сохранить его за вас.

```yaml
# yaml-language-server: $schema=https://coddy.dev/config.schema.json
providers:
  - name: opencode
    type: opencode
    api_key: "${OPENCODE_API_KEY}"   # или литерал ключа, или пусто, чтобы читать OPENCODE_API_KEY
    # api_base: https://opencode.ai/inference/v1   # Console; без значения - Go по умолчанию
models:
  - model: opencode/glm-5.3
  - model: opencode/gpt-6-luna
  - model: opencode/claude-haiku-5-5
agent:
  model: opencode/glm-5.3
```

Пустой `api_key` читает переменную окружения `OPENCODE_API_KEY` (обычное правило `NAME_API_KEY`, так что строка с именем `opencode` читает `OPENCODE_API_KEY`), затем ключ, который сохранил login. Форма `${OPENCODE_API_KEY}` раскрывается при загрузке файла; просто пустое значение читается в момент вызова.

### Сохранить ключ через login

```bash
coddy providers login opencode
```

У OpenCode нет браузерного или device-потока - ключ копируется из веб-консоли, - поэтому login сводится к вставке. Он читает ключ из терминала (без эха) или из `--api-key`, сохраняет его в `$CODDY_HOME/providers/opencode/opencode-auth.json` с правами `0600` и, если не указан `--no-config`, записывает строку провайдера, модели шлюза и `agent.model` в `config.yaml`.

```text
$ coddy providers login opencode
Copy your API key from https://opencode.ai/console, then paste it here.
API key:
Stored the OpenCode key for https://opencode.ai/inference/go/v1 at /home/jane/.coddy/providers/opencode/opencode-auth.json
Updated /home/jane/.coddy/config.yaml: provider opencode, agent.model opencode/glm-5.3-flash, 39 models (opencode/glm-5.3-flash, opencode/kimi-k3, opencode/deepseek-v4-flash, ...)
```

Явный `api_key`, `api_key_command` или `OPENCODE_API_KEY` всё равно имеет приоритет над сохранённым входом. `coddy providers logout opencode` удаляет сохранённый файл; ключ остаётся действительным, пока вы не отзовёте его в консоли. `coddy providers list` показывает, какой источник использует строка, с маскированием ключа.

## Два шлюза

`api_base` выбирает шлюз; оба принимают ключ одного формата, но ключ выпускается для одного из них.

| Шлюз | `api_base` | Что это |
|---------|-----------|------------|
| **Go** (по умолчанию) | `https://opencode.ai/inference/go/v1` | месячная подписка Go / Go Plus |
| **Console** | `https://opencode.ai/inference/v1` | кредиты с оплатой по мере использования (Zen) |

Пустой `api_base` означает Go. Значение, которое не называет ни один шлюз, сводится к Go, так что ключ OpenCode никогда не уходит на произвольный хост. В веб-интерфейсе в разделе **Settings → LLM providers** строка показывает выбор шлюза вместо текстового поля базы. `CODDY_OPENCODE_BASE_URL` переводит весь процесс на другой адрес - для стенда или теста.

## Протокол обмена для каждой модели

Шлюз OpenCode отдаёт разные модели по разным протоколам, а его список `/models` не несёт подсказки, по какому именно, - только id. Coddy выбирает протокол по id модели.

| Протокол | Эндпоинт | Семейства моделей |
|----------|----------|----------------|
| OpenAI Responses | `/openai/v1/responses` | `grok-*`, `gpt-*`, `muse-*` |
| Anthropic | `/anthropic/v1/messages` | `claude-*`, `qwen*`, `minimax-m3` в Go |
| Google | `/google/v1beta/models/<id>:generateContent` | `gemini-*` |
| OpenAI-совместимый | `/openai/v1/chat/completions` | `glm-*`, `kimi-*`, `deepseek-*`, `longcat-*`, `mimo-*`, `hy*`, `space-bunny`, бесплатные модели для чата и неизвестные id |

Эти пути следуют за `https://opencode.ai/inference/go` для Go или `https://opencode.ai/inference` для Console. Каталог остаётся на `/v1/models` под этим префиксом. Console обслуживает `qwen3.8-max` и все модели `minimax-*` через Chat Completions; Go обслуживает `qwen3.8-max` и `minimax-m3` через Anthropic. Более старые модели MiniMax используют Chat Completions.

Id, которого нет в этой таблице, использует Chat Completions. Соответствие следует [документации OpenCode Go](https://opencode.ai/v2/docs/console/go/) и [эндпоинтам моделей Console](https://opencode.ai/v2/docs/console/models/); когда OpenCode меняет протокол модели, обновите `opencodeProtocolForGateway` в `internal/llm/opencode.go`.

Уровни рассуждения передаются как `reasoning.effort` в OpenAI Responses, бюджет размышлений в Anthropic, `reasoning_effort` в OpenAI-совместимом протоколе или `thinkingConfig.thinkingLevel` в Google - по протоколу модели. Gemini передаёт поток через `:streamGenerateContent?alt=sse`; Coddy сохраняет подписанные части ответа между вызовами инструментов и частичный текст, если поток оборвался до причины завершения. Встроенные изображения поддерживаются; удалённые URL изображений обозначаются как вложения без скачивания.

## Обязательные заголовки клиента

Шлюз отслеживает злоупотребления и просит каждый клиент представляться и держать диалог на одном маршруте. Coddy отправляет в каждом запросе к OpenCode - на всех четырёх протоколах, для завершений и для списка моделей:

- `User-Agent: coddy/<version>` - собственное имя, а не типовое имя SDK или HTTP-библиотеки;
- `x-opencode-session: <session id>` - стабильный id диалога, чтобы OpenCode мог оптимизировать маршрутизацию и повторно использовать свой кэш промптов. Вызов, который не является диалогом (список моделей, проба `--dry-run`), отправляет сгенерированный id.

Оба заголовка добавляются поверх собственного транспорта строки, поэтому они никогда не открывают маршрут в обход `providers[].proxy`.

Отправляется только ключ OpenCode этой строки. Ключи SDK по умолчанию, такие как `OPENAI_API_KEY` и `ANTHROPIC_API_KEY`, отбрасываются, а перенаправления на другой origin отклоняются до пересылки ключа.

## Прокси

`providers[].proxy` маршрутизирует каждый запрос к OpenCode - завершения на всех четырёх протоколах, список моделей и запрос каталога при входе - точно так же, как у любого другого провайдера: `inherit` следует прокси окружения, `none` соединяется напрямую, а URL идёт через этот прокси. См. [Прокси провайдера](../getting-started/configuration.md#прокси-провайдера).

## Лимиты использования

OpenCode учитывает расход каждой модели по месячному долларовому бюджету (с подлимитами на 5 часов и на неделю), и вы можете следить за ним в [консоли](https://opencode.ai/console). В Coddy нет чтения расхода аккаунта OpenCode, поэтому панель расхода для строки `opencode` не показывается. Окно контекста модели берётся из `max_context_tokens`, затем из метаданных каталога, если они есть, затем используется 128000.

## Тестирование

- Модульные тесты: `internal/llm/opencode_test.go` проверяет точные пути запросов для обоих шлюзов, заголовки, изоляцию ключей, отказ от перенаправлений и сохранённые учётные данные. `opencode_google_test.go` покрывает диалоги Gemini с инструментами, подписанные части, расход и обрывы потока;
- `features/provider_proxy.feature` покрывает все четыре семейства API, получение списка моделей и обновление конфигурации при входе для каждого режима прокси;
- Транспорт Responses общий с бэкендом Codex (`internal/llm/responses_stream.go`); его покрывает `internal/llm/codex_test.go`.

## См. также

- [Конфигурация](../getting-started/configuration.md) - провайдеры, модели и порядок разрешения учётных данных.
- [Переменные окружения](../reference/environment-variables.md) - `OPENCODE_API_KEY`, `CODDY_OPENCODE_BASE_URL`.
- [Справочник конфигурации](../reference/config.md) - поля `providers[]` и `models[]`.

<!-- docsgen:source sha256=3eb38a9aa67c4596 -->
