# Интеграционные тесты с tgfake

Telegram-бот Coddy тестируется против
[tgfake](https://github.com/EvilFreelancer/tgfake) - поддельного Telegram Bot
API со страницей чата, API симуляции и OpenAI-совместимой моделью, которая
отвечает по сценарию. Раньше он жил здесь как `internal/tgfake` и `cmd/tgfake`,
а теперь это отдельный проект, который выпускается в виде бинарников и
импортируется как Go-модуль, так что в той же песочнице можно тестировать
любого бота. На этой странице собрано то, что нужно контрибьютору, чтобы
запускать эти тесты, разбираться в их падениях и переводить Coddy на новую
версию tgfake.

## Одна версия, три способа подключения

`go.mod` фиксирует версию tgfake в одном месте, и всё в этом репозитории
использует именно её.

```text
require github.com/EvilFreelancer/tgfake vX.Y.Z
tool github.com/EvilFreelancer/tgfake/cmd/tgfake
```

| Где | Как используется tgfake |
|-------|--------------------|
| тесты `external/gateway/telegram` (`-tags gateway`) | `pkg/server` внутри процесса на `httptest`, импортированный как `tgfake` и открываемый через `fakeapi_test.go`; `pkg/botapi` для типов клавиатуры. Через него проходит каждый тест шлюза, которому нужен Telegram. |
| наборы тестов `internal/agent`, `external/httpserver`, `external/cli` и шлюза | `pkg/llmstub`, модель по сценарию, с `StripTags: []string{"turn_context"}`, чтобы правило сопоставлялось с тем, что написал человек, а не с блоком, который Coddy добавляет к каждому запросу. |
| `examples/gateway/tg_e2e_offline.sh`, `examples/shared/wake_e2e_common.py`, `examples/cli/capture_wake.py`, `npm run check:telegram` | Бинарник `tgfake` - тот, на который указывает `CODDY_TGFAKE_BIN`, иначе собранный из зафиксированного модуля (`go build github.com/EvilFreelancer/tgfake/cmd/tgfake`). Скрипты запускают его с `--llm-model coddy-demo --llm-strip-tag turn_context`. |
| CI, задание **Telegram stand (tgfake)** | Релизный бинарник зафиксированной версии, установленный собственным GitHub Action tgfake, затем `tg_e2e_offline.sh` против настоящего `coddy serve`. |

Go-тестам не нужно ничего, кроме `go test` - модуль скачивается, как любая
другая зависимость. Процесс запускают только скрипты.

## Запуск тестов

Наборы тестов внутри процесса входят в обычные прогоны.

```bash
go test -tags gateway ./external/gateway/telegram/
```

```bash
make test
```

Сквозной прогон запускает бинарник и настоящий `coddy serve` с ботом,
направленным на него, и проверяет, что на сообщение приходит ответ, `/clear` и
`/resume` возвращают в ту же сессию, а фоновая задача будит агента в чате.

```bash
make build TAGS=gateway
```

```bash
examples/gateway/tg_e2e_offline.sh
```

`TG_E2E_KEEP=1` оставляет стенд работать и печатает адрес страницы чата, где
можно продолжить разговор с ботом. Чтобы запустить готовый бинарник вместо
сборки, установите релиз зафиксированной версии и укажите путь к нему.

```bash
go list -m -f '{{.Version}}' github.com/EvilFreelancer/tgfake
```

```bash
curl -sSfL https://raw.githubusercontent.com/EvilFreelancer/tgfake/vX.Y.Z/scripts/install.sh | sh -s -- -b ./build vX.Y.Z
```

```bash
CODDY_TGFAKE_BIN=./build/tgfake examples/gateway/tg_e2e_offline.sh
```

Та же переменная используется в стендах фонового пробуждения
(`examples/test_httpserver.sh`, `examples/test_acp.sh`, консольный
`cli_e2e_background_wake.py`) и в проверке Mini App (`npm run
check:telegram`, см. [страницу веб-интерфейса](../surfaces/web-ui.md)).

Чтобы получить стенд, с которым можно поработать вручную, запустите
зафиксированную версию из корня репозитория и следуйте разделу
[Отладка с поддельным Bot API](../surfaces/gateway.md#отладка-на-поддельном-bot-api).

```bash
go tool tgfake --llm --llm-strip-tag turn_context
```

## В CI

Задание **Telegram stand (tgfake)** из `.github/workflows/tests-on-pr.yaml`
служит интеграционным слоем:

1. оно читает версию, зафиксированную в `go.mod`, через `go list -m` и падает,
   если workflow использует action tgfake с другой ссылкой, так что они не
   могут разойтись;
2. `EvilFreelancer/tgfake@vX.Y.Z` устанавливает релизный бинарник этой версии
   для раннера и сверяет его с контрольными суммами релиза;
3. оно собирает `coddy` с тегом `gateway` и запускает
   `examples/gateway/tg_e2e_offline.sh` с заданным `CODDY_TGFAKE_BIN`.

Ни токен, ни модель, ни сеть, кроме скачивания, не нужны. Наборы тестов
внутри процесса идут в матрице тестов, как и все остальные тесты.

## Значения по умолчанию, которые полезно знать

- стенд изображает `@tgfake_bot` с именем `tgfake`. Спецификация опроса
  называет своего бота `coddy_fake_bot` / `Coddy Fake` через `Options`,
  остальные тесты берут значение по умолчанию;
- модель по сценарию сообщает имя `tgfake-demo`. Тесты и скрипты Coddy
  запрашивают `coddy-demo` (`Model` в Go, `--llm-model` в командной строке),
  потому что в их конфигурациях указан `stub/coddy-demo`;
- без `--llm-strip-tag turn_context` (или `StripTags` в Go) модель принимает
  блок `<turn_context>` за промпт, и ни одно правило не срабатывает;
- человек по умолчанию - пользователь 4242 `alice`, у его личного чата тот же
  id.

## Изменение tgfake

Тест, которому нужно, чтобы Telegram вёл себя по-новому (метод, который шлюз
начинает вызывать, отказ, который он должен пережить), требует изменения в
tgfake, а не обработчика, написанного здесь. В репозитории tgfake описано, как
сервер воспроизводит Telegram (`AGENTS.md`, `.claude/rules/telegram-fidelity.md`).

1. Внесите изменение в tgfake вместе с тестами, влейте его и запушьте тег
   `vX.Y.Z`; workflow релиза tgfake опубликует бинарники.
2. Здесь перейдите на новую версию и обновите вместе с ней инструмент.

   ```bash
   go get github.com/EvilFreelancer/tgfake@vX.Y.Z
   ```

   ```bash
   go mod tidy
   ```

3. Замените `uses: EvilFreelancer/tgfake@...` в `tests-on-pr.yaml` на тот же
   тег, затем запустите `make test` и `examples/gateway/tg_e2e_offline.sh`.

Чтобы работать над обоими проектами сразу, создайте вне обоих чекаутов файл
рабочего пространства Go, который указывает на оба дерева, и используйте его
для команд этого репозитория. Рабочему пространству нужна ещё и строка
`replace`, потому что `go.mod` называет версию, вместо которой подставляется
локальное дерево.

```text
go 1.26.0

use (
	./coddy-agent
	./tgfake
)

replace github.com/EvilFreelancer/tgfake vX.Y.Z => ./tgfake
```

```bash
GOWORK=$PWD/../coddy-tgfake.work go test -tags gateway ./external/gateway/telegram/
```

Скрипт, собранный через `go build`, подхватывает локальное дерево так же. Файл
рабочего пространства никогда не коммитится - CI и все остальные чекауты
используют зафиксированный релиз.

<!-- docsgen:source sha256=1a47bd3e81823e45 -->
