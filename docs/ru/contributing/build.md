# Сборка Coddy из исходников

На этой странице подробно описана локальная сборка. Краткая версия - в разделе [Установка](../../../README.md#install) корневого **README**.

## Требования

- **Go** - **1.26** или новее, строка `go` из [`go.mod`](../../../go.mod). Строка `toolchain` рядом с ней (сейчас **`go1.26.8`**) задаёт самый старый релиз, с которым линкуется любая сборка. CI и релизные архивы собираются ровно с ним, а при `GOTOOLCHAIN=auto` по умолчанию более старый локальный `go` скачивает его при первом использовании. Более новый локальный Go или более новый патч в образе `golang:1.26`, с которого начинается Dockerfile, используется как есть, а это только добавляет исправления. Поднимайте эту строку командой `go mod edit -toolchain=go1.26.N`, когда патч-релиз Go исправляет уязвимость, до которой доходит код (см. [Сканирование AppSec](security-scanning.md#инструментарий-go));
- **Git** - Makefile встраивает строку версии из тегов или `git describe`, если они доступны;
- **Node.js и npm** - нужны при сборке с **`http`** и **`ui`** вместе, потому что Makefile запускает **`ui-build`** (см. [`Makefile`](../../../Makefile)), чтобы получить ресурсы, которые подхватывает **`go:embed`**, а также для **`make test`** и **`make lint`**, которые запускают тесты vitest для SPA и его проверку TypeScript.

Необязательно:

- **`golangci-lint` v2.x** (собранный с Go **1.26** или новее) - для **`make lint`**. CI использует **`golangci/golangci-lint-action@v7`** или новее (v6 поддерживает только golangci-lint v1).

## Рекомендуемый полный бинарник (HTTP, веб-интерфейс, планировщик, память, консоль)

Соберите с **`memory`**, чтобы скомпоновать долговременную память (`external/memory`). Во время работы она включается ключом **`memory.enable`** в конфигурации (см. [`external/memory/README.md`](../../../external/memory/README.md)).

**HTTP-шлюз**, **встроенный SPA**, **планировщик**, **память**, **шлюз мессенджеров** (**`gateway`**, см. [`docs/surfaces/gateway.md`](../surfaces/gateway.md)) и **интерактивная консоль** (**`cli`**, **`coddy`** без аргументов в терминале, см. [`docs/surfaces/console.md`](../surfaces/console.md)) управляются тегами сборки Go. Один бинарник, который соответствует **Docker**-образу по умолчанию и включает все необязательные возможности, собирается так.

```bash
make build TAGS="http ui scheduler memory cli gateway swarm"
```

Результат - **`build/coddy`**.

Эквивалентный **`go build`** (после `ui-build`, если вы используете **`ui`**, или воспользуйтесь **`make build`**, который сам запускает **`ui-build`**, когда в **`TAGS`** есть и **`http`**, и **`ui`**).

```bash
make ui-build   # only when using -tags=...,ui,... with http; Makefile runs this for you on `make build`
VERSION="$(make -s print-version)"
go build -tags=http,ui,scheduler,memory,cli \
  -ldflags "-X github.com/EvilFreelancer/coddy-agent/internal/version.Version=${VERSION}" \
  -o build/coddy \
  ./cmd/coddy/
```

[**Dockerfile**](../../../Dockerfile) следует той же схеме, передаёт теги через запятую в **`BUILD_TAGS`** (по умолчанию **`http,scheduler,ui,memory,gateway,cli`**) и, кроме флага версии **`X`**, убирает отладочные символы через **`-ldflags "-s -w ..."`**.

## Установка в PATH

**`make install`** копирует **`build/coddy`** в каталог из вашего **`PATH`**:

- если **`build/coddy`** уже есть (например, после **`make build TAGS="http ui scheduler memory cli gateway swarm"`**), он устанавливается как есть, без пересборки;
- если бинарника нет, **`make install`** сначала запускает **`make build TAGS="http ui scheduler memory cli gateway swarm"`**.

- **root** - **`/usr/local/bin/coddy`**, man-страница в **`/usr/local/share/man/man1`**;
- **не root** - **`~/.local/bin/coddy`** (убедитесь, что этот каталог есть в **`PATH`**), man-страница в **`~/.local/share/man/man1`**.

```bash
make build TAGS="http ui scheduler memory cli gateway swarm"
make install
```

## Пакеты дистрибутивов

**`make deb`** и **`make rpm`** собирают пакеты для Linux, которые публикует релиз; **`make brew`** генерирует для релиза Homebrew cask.

```bash
make deb
make rpm
```

Результат - **`dist/coddy_<version>_linux_<arch>.deb`** и **`.rpm`**. Сборку настраивают переменные из таблицы.

| Переменная | По умолчанию | Назначение |
|----------|---------|------|
| **`PKG_ARCHS`** | **`GOARCH`** хоста | архитектуры для упаковки, например **`"amd64 arm64"`** |
| **`PKG_TAGS`** | **`http ui scheduler memory cli gateway swarm`** | теги сборки упакованного бинарника |
| **`DIST_DIR`** | **`dist`** | куда кладутся пакеты |

```bash
make deb PKG_ARCHS="amd64 arm64"
make rpm PKG_TAGS="http cli"      # lean binary, no npm step
```

Рецепт лежит в **`packaging/nfpm.yaml`**, а запускает его **`scripts/build-packages.sh`**, который собирает в один каталог man-страницу (**`packaging/man/coddy.1`**), автодополнения оболочек (**`packaging/completions/`**), пользовательский unit systemd (**`packaging/systemd/coddy.service`**), maintainer-скрипты (**`packaging/scripts/postinstall.sh`**, **`preremove.sh`**), **`config.example.yaml`** и **`LICENSE`** и запускает над ним [nfpm](https://nfpm.goreleaser.com/). nfpm не входит в зависимости модуля. Скрипт берёт **`nfpm`** из **`PATH`**, если он там есть, а иначе получает закреплённую версию через **`go run`**, так что заранее ничего устанавливать не нужно.

Пользовательский unit устанавливается, но не включается, и сообщение после установки об этом говорит; каждый пользователь, которому нужен сервис, запускает **`coddy serve install`**. Системный сервис, системная учётная запись и файлы в **`/etc`** не создаются, потому что каждый пользователь хранит состояние в **`~/.coddy`**. Unit не пишется вручную. Его генерирует **`serve.PackagedUnitFile()`** (**`internal/serve/systemd.go`**), та же функция, которая записывает unit при установке скриптом, и **`TestPackagedUnitIsTheRenderedOne`** падает, пока **`packaging/systemd/coddy.service`** не совпадёт с ней байт в байт. Затем задание CI **Distribution packages** проверяет, что оба формата пакетов содержат этот файл и maintainer-скрипты.

Пакеты предлагают **`tmux`** (suggests) и ничего не требуют. Coddy работает без него, а его консоль хорошо работает внутри tmux, где сессия переживает закрытый терминал или оборванное SSH-соединение. Предложенный пакет только называется и никогда не устанавливается - **`apt`** и **`zypper`** показывают его при установке, **`dnf`** хранит его в метаданных (**`rpm -q --suggests`**), - так что выбор остаётся за пользователем. Задание CI **Distribution packages** проверяет, что deb и rpm предлагают его и ни один из них его не требует и не рекомендует.

Строки версий приводит к виду, подходящему обоим форматам, **`scripts/package-version.sh`** - rpm запрещает **`-`** в версии, а dpkg считает последний дефис началом ревизии Debian, поэтому **`1.0.8-5-gb6b7d31-dirty`** упаковывается как **`1.0.8+5.gb6b7d31.dirty`**, что принимают оба формата и оба сортируют после **`1.0.8`**.

### Cask для Homebrew

```bash
make brew VERSION=1.0.11
```

**`scripts/build-homebrew-cask.sh`** подставляет в **`packaging/homebrew/coddy.rb.tmpl`** версию и SHA-256 обоих архивов для macOS и записывает **`dist/coddy.rb`**. Архивы он берёт из **`DIST_DIR`**, если они там есть (так бывает в задании релиза, сразу после кросс-компиляции), а иначе скачивает из релиза на GitHub - cask закрепляет контрольные суммы, поэтому сгенерировать его можно только для версии, архивы которой уже существуют.

Чтобы попробовать сгенерированный файл, установите его.

```bash
brew install --cask dist/coddy.rb
```

Cask создаёт ссылки на **`coddy`**, **`coddy.1`** и оба скрипта автодополнения, поэтому релизные архивы **`darwin`** и **`linux`** содержат эти файлы рядом с бинарником. **`tmux`** он рекомендует в своих **`caveats`**, а не объявляет зависимостью, потому что у **`depends_on`** в cask нет необязательной формы, а Coddy работает без tmux, так что cask его называет и ничего не устанавливает. Каждый релиз публикует **`coddy.rb`** как файл релиза, и **`brew install --cask <url>`** устанавливает из него.

### Формула для Homebrew

```bash
make brew-formula VERSION=1.0.13
make brew-check VERSION=1.0.13
```

**`scripts/build-homebrew-formula.sh`** подставляет в **`packaging/homebrew/coddy-formula.rb.tmpl`** версию и SHA-256 архива **исходников** этого тега и записывает **`dist/formula/coddy.rb`**. Чтобы посчитать хеш, он скачивает архив, поэтому версия должна быть опубликованным тегом.

Этот файл - артефакт, который несёт пул-реквест в [homebrew/core](https://github.com/Homebrew/homebrew-core). Homebrew направляет туда программы командной строки с открытым кодом в виде формул, собираемых из исходников, а homebrew/cask оставляет для нативных приложений и программ, распространяемых только в бинарном виде, поэтому cask выше - наш собственный канал, а формулу мы подаём в homebrew/core. Формула собирает релизный набор тегов, поэтому к **`go`** в зависимостях сборки добавляется **`node`**, ведь встроенный SPA генерируется, а не хранится в репозитории.

**`scripts/check-homebrew-submission.sh`** (**`make brew-check`**) - предварительная проверка: наличие токена, пороги известности, релиз и сгенерированная формула. Если что-то мешает подаче, скрипт завершается с ненулевым кодом. Весь путь, включая расчёт известности, из-за которого сейчас нельзя подать формулу самим, описан в [homebrew.md](../getting-started/homebrew.md).

Что устанавливают пакеты и как они взаимодействуют с **`coddy update`**, описано в [install.md](../getting-started/install.md#пакеты-для-linux-deb-rpm) и [update.md](../getting-started/update.md#установки-которыми-владеет-пакетный-менеджер).

## Обновление из GitHub Releases

Про **`coddy update`**, имена файлов релиза и чем это отличается от **`make install`**, см. **[docs/getting-started/update.md](../getting-started/update.md)**.

## Облегчённая сборка (для ACP, бинарник меньше)

Обычный **`make build`** (пустой **`TAGS`**) не включает **`external/httpserver`**, встроенный интерфейс, **`external/scheduler`** и **`external/memory`**. **`coddy acp`**, основные инструменты и MCP при этом остаются.

```bash
make build
```

Используйте эту сборку, когда вам нужен только ACP через stdio, а зависимостей хочется меньше и без шага **`npm`**.

## Android (Termux)

```bash
make android TAGS="http ui scheduler memory cli gateway swarm"   # build/coddy-android-arm64, -amd64
make check-android                                              # go vet with GOOS=android, test files included
```

Сборка для Android - те же исходники с **`GOOS=android`** и cgo, слинкованные Android NDK с Bionic для arm64 и x86_64. Получается позиционно-независимый исполняемый файл, который указывает **`/system/bin/linker64`** как свой интерпретатор и требует только **`libc`**, **`libdl`** и **`liblog`**. Только такой файл Termux может запустить там, где он запускает каждую программу через линкер Android, а статический бинарник **`GOOS=linux`** этот линкер отвергает с **`has unexpected e_type: 2`**. Bionic даёт бинарнику то, что libc даёт любой программе, а именно собственные аргументы при запуске через линкер и резолвер Android. **`GOOS=android`** к тому же удерживает Go от системных вызовов, которые запрещает фильтр seccomp в Android. Go в любом случае линкует android/amd64 только через инструментарий C, а сборка без cgo обращалась бы к **`127.0.0.1:53`** за каждым хостом, поэтому **`internal/platform/android_nocgo.go`** останавливает её на этапе компиляции. Что видят пользователи, описано на странице [Android (Termux)](../getting-started/android.md).

**`scripts/android-cc.sh arm64|amd64`** выводит clang из NDK, которым пользуются обе цели, для API 24 (Android 7.0, самая старая версия, на которой работает Termux). NDK он берёт из **`ANDROID_NDK_HOME`**, **`ANDROID_NDK_ROOT`** или **`ANDROID_NDK`**, которые задают раннеры GitHub, а иначе самый новый NDK в SDK (**`sdkmanager "ndk;27.3.13750724"`** устанавливает версию, которая есть на раннерах).

В бинарнике, собранном вне Termux, нет ни одного из патчей, которые Termux накладывает на свой Go, поэтому оставшиеся поправки **`internal/platform`** делает во время работы. **`android_init.go`** запоминает путь к бинарнику, когда его запустил линкер, потому что **`/proc/self/exe`** в этом случае указывает на линкер, и направляет **`SSL_CERT_FILE`** и **`TMPDIR`** в префикс Termux; **`platform.AdaptCommand`** запускает дочерний процесс так, как это сделал бы **`termux-exec`**, то есть через линкер, если сам Coddy поднялся так же, и с **`/usr/bin/env`** и **`/bin/sh`** в shebang из префикса. Каждое место, которое строит **`exec.Cmd`**, вызывает **`AdaptCommand`** перед **`Start`**, а **`TestEverySpawnSiteAdaptsTheCommand`** падает на месте, которое этого не делает.

Решения принимают обычные функции в **`internal/platform/android.go`**, и на любом Unix-хосте их проверяют **`features/android_termux.feature`** и **`internal/platform/android_test.go`**. Файлы за **`//go:build android`** компилируют только **`make check-android`** и задание **Android cross-build** в проверках пул-реквеста. Это задание ещё и собирает оба релизных бинарника, проверяет, что каждый из них позиционно-независимый, указывает линкер Android и не требует библиотек, которых нет в Android, и выкладывает их как артефакты **`coddy-android-arm64`** и **`coddy-android-amd64`**, чтобы их можно было попробовать на устройстве до релиза.

## Строка версии (`LDFLAGS`, `print-version`)

Makefile задаёт следующее.

```text
LDFLAGS := -X github.com/EvilFreelancer/coddy-agent/internal/version.Version=$(VERSION)
```

**`VERSION`** определяется по git (тег на **HEAD**, иначе **`git describe`**, иначе **`dev`**). Вывести то же значение, которое встроит следующий **`make build`**, можно так.

```bash
make -s print-version
```

Ручная команда, согласованная с **`make build`**.

```bash
go build \
  -tags=http,ui,scheduler,memory,cli \
  -ldflags "-X github.com/EvilFreelancer/coddy-agent/internal/version.Version=$(make -s print-version)" \
  -o build/coddy \
  ./cmd/coddy/
```

## **`TAGS` и `go build -tags`**

В **`Makefile`** список **`TAGS`** разделяется **пробелами**.

```bash
make build TAGS="http ui scheduler memory cli gateway swarm"
```

**`go build`** ожидает список **через запятую** (без пробелов).

```bash
go build -tags=http,ui,scheduler,memory,cli ...
```

Порядок этих тегов не важен.

## Справочник тегов сборки

| Тег | Что включает | Документация |
|-----|---------|----------------|
| **`memory`** | Субагент долговременной памяти; с **`http`** - REST **`/coddy/sessions/{id}/memory/*`**; поведение во время работы переключается ключом **`memory.enable`** | [`external/memory/README.md`](../../../external/memory/README.md) |
| **`http`** | REST-шлюз в стиле OpenAI, который `coddy serve` запускает при **`httpserver.enable`**, **`/docs`**, **`/openapi.yaml`** | [`docs/reference/http-api.md`](../reference/http-api.md) · [`external/httpserver/`](../../../external/httpserver) |
| **`ui`** | Встроенный SPA на **`/`** (требует **`http`**; только с **`http`** **`/`** возвращает **404**) | [`docs/surfaces/web-ui.md`](../surfaces/web-ui.md) · [`DESIGN.md`](../../../DESIGN.md) |
| **`scheduler`** | Точки подключения демона планировщика, инструменты **`coddy_scheduler_*`**; с **`http`** - REST **`/coddy/scheduler`** | [`docs/operate/scheduler.md`](../operate/scheduler.md) · [`external/scheduler/README.md`](../../../external/scheduler/README.md) |
| **`cli`** | Интерактивная консоль (TUI) - **`coddy`** без аргументов в терминале | [`docs/surfaces/console.md`](../surfaces/console.md) · [`external/cli/`](../../../external/cli) |
| **`gateway.telegram`** | Адаптер Telegram-бота, который запускает **`coddy serve`** при **`gateways.telegram.enable`**; сессии на пользователя или группу, контроль доступа | [`docs/surfaces/gateway.md`](../surfaces/gateway.md) · [`external/gateway/`](../../../external/gateway) |
| **`gateway`** | Все адаптеры мессенджеров (надмножество **`gateway.telegram`**; включает будущие адаптеры Discord и Slack) | [`docs/surfaces/gateway.md`](../surfaces/gateway.md) |
| **`swarm`** | Релей без состояния, который объединяет узлы и запускается **`coddy serve`** при **`swarm.enable`** | [`docs/operate/swarm.md`](../operate/swarm.md) · [`external/swarm/`](../../../external/swarm) |

**`make test`** - экспресс-прогон, то есть всё дерево один раз со всеми необязательными модулями (**`http,ui,scheduler,memory,cli,gateway,swarm`**). **`make test-matrix`** перебирает все комбинации (список **`TEST_TAG_SETS`** в [`Makefile`](../../../Makefile)); CI прогоняет эту матрицу на каждом пул-реквесте, по заданию на комбинацию. Задание `cli` к тому же запускает собранный бинарник и управляет консолью через настоящий pty (`examples/cli/cli_e2e_startup.py`), а задание **Telegram stand (tgfake)** запускает Telegram-бота против релизного бинарника [tgfake](tgfake.md) (`examples/gateway/tg_e2e_offline.sh`). Задание **Docker image** собирает образ среды выполнения для **`linux/amd64`** и **`linux/arm64`**, платформ, которые публикует релиз, и проверяет, что в каждом варианте лежит бинарник его собственной платформы (**`make check-image`**, см. [Проверка платформ образа](../getting-started/docker.md#проверка-платформ-образа)). **`make test-race`** прогоняет всё дерево под детектором гонок Go со всеми тегами, кроме `ui`; CI запускает его на каждом пул-реквесте в задании **Race detector**, а `GOFLAGS=-count=3 make test-race` локально повторяет каждый тест, чтобы поймать редко проявляющуюся гонку. Две группы можно запускать отдельно. **`make test-cache`** - группа кэша промптов (спецификации `features/prompt_cache_*.feature` и тесты `TestPromptCache*`, входят в `make test`; они проверяют, что каждый запрос отправляет провайдеру, байт в байт на протяжении сессии), а **`make test-perf`** - группа производительности (бенчмарки файлов `perf_test.go`, никогда не входят в `make test`; они измеряют время работы, которую повторяет каждый ход, и стоимость контекста сессии над собственными правилами этого репозитория в виде `system-tokens`, `request-tokens` и `duplicate-rules`).

В Windows запускайте Make через Git Bash. Цели сборки и установки используют суффикс исполняемых файлов Go (`coddy.exe`), в том числе для бинарника, который запускает `make docs`. Фикстуры экранируют родные пути Windows в JSON и YAML, а проверки упаковки принимают чекауты с CRLF. Проверки прав доступа POSIX в Windows не применяются; тесты, которые вызывают сбои через права на каталоги или файлы Unix, там пропускаются (и при запуске от root). Сценарий ACP с символической ссылкой пропускается, только если Windows не даёт права на создание символических ссылок; чтобы его выполнить, включите режим разработчика или запустите терминал с повышенными правами. Проверки ресурсов Docker охватывают и чекауты Git с `core.symlinks=false`, читая режимы ссылок из индекса.

Проверки документации принимают ограждения блоков кода с CRLF и измеряют текст SVG с окончаниями строк LF, поэтому преобразование строк при чекауте в Windows не меняет генерируемую опись ресурсов.

## Релизные бинарники (CI)

Для каждого git-тега SemVer **`X.Y.Z`** на **`main`** workflow [**Release binaries**](../../../.github/workflows/release-binaries.yaml) (отдельный от Docker CI) загружает архивы в соответствующий **GitHub Release**.

| Архив | Платформа |
|---------|----------|
| **`coddy_X.Y.Z_linux_amd64.tar.gz`** | Linux x86_64 |
| **`coddy_X.Y.Z_linux_arm64.tar.gz`** | Linux arm64 |
| **`coddy_X.Y.Z_android_arm64.tar.gz`**, **`coddy_X.Y.Z_android_amd64.tar.gz`** | Android arm64 и x86_64, под Termux (см. [выше](#android-termux)) |
| **`coddy_X.Y.Z_windows_amd64.zip`** | Windows x86_64 (**`coddy.exe`**) |
| **`coddy_X.Y.Z_darwin_amd64.tar.gz`** | macOS Intel |
| **`coddy_X.Y.Z_darwin_arm64.tar.gz`** | macOS Apple Silicon |
| **`coddy_X.Y.Z_linux_amd64.deb`**, **`coddy_X.Y.Z_linux_arm64.deb`** | Debian, Ubuntu и производные |
| **`coddy_X.Y.Z_linux_amd64.rpm`**, **`coddy_X.Y.Z_linux_arm64.rpm`** | Fedora, RHEL, openSUSE и производные |
| **`coddy.rb`** | Homebrew cask для архивов macOS этого тега |
| **`SHA256SUMS`** | Контрольные суммы всех архивов и пакетов выше |

Архивы **`.tar.gz`** содержат рядом с бинарником man-страницу и автодополнения оболочек; пакеты оборачивают бинарники Linux, которые только что собрало то же задание, а не компилируют свои, поэтому в **`.deb`**, **`.rpm`** и **`.tar.gz`** одного тега лежат побайтно одинаковые исполняемые файлы.

Теги соответствуют полному набору возможностей: **`http`**, **`ui`**, **`scheduler`**, **`memory`**, **`cli`**, **`gateway`**, **`swarm`**. Ручной запуск, когда тег уже существует, выглядит так.

```bash
gh workflow run "Release binaries" --ref X.Y.Z -f tag=X.Y.Z
```

## Сканирование AppSec

**`make security`** прогоняет над чекаутом гейт безопасности проекта - trivy (уязвимости зависимостей, секреты; ошибки конфигурации только в отчёте), semgrep (SAST) и govulncheck (уязвимости Go, до которых доходит код, включая стандартную библиотеку закреплённого инструментария), - с теми же версиями и порогами, что и workflow [**Security scan**](../../../.github/workflows/security.yaml) на пул-реквестах. **`make sec-report`** сканирует, не падая на находках. Отчёты кладутся в **`dist/security/`**. Как запускаются сканеры, параметры гейта и разбор находок описаны на странице [Сканирование AppSec](security-scanning.md).

## **`go install` из основного репозитория**

```bash
go install github.com/EvilFreelancer/coddy-agent/cmd/coddy@latest
```

Так компилируется то, что модуль собирает по умолчанию, **без** ваших локальных **`TAGS`**. Чтобы получить известный набор возможностей (HTTP, веб-интерфейс, планировщик, память, консоль, шлюз мессенджеров), клонируйте репозиторий и используйте **`make build TAGS="http ui scheduler memory cli gateway swarm"`** (или **`go build -tags=...`**, как выше).

<!-- docsgen:source sha256=5dc14aeab19ee267 -->
