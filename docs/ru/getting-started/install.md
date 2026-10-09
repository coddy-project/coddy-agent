# Установка Coddy

Скрипты установки и лендинг - **https://coddy.dev/**

## Установка одной командой

**Linux / macOS**

```bash
curl -fsSL https://coddy.dev/install.sh | bash
```

**Windows (PowerShell)**

```powershell
irm https://coddy.dev/install.ps1 | iex
```

**Android (Termux)** - тот же **`install.sh`** скачивает сборку для Android; см.
[Android (Termux)](#android-termux).

Если **`~/.coddy/config.yaml`** нет, скрипт создаёт его из **`config.example.yaml`** релиза.

В Linux и macOS скрипт ставит не только бинарник. В архиве релиза рядом с **`coddy`** лежат
man-страница и автодополнения для bash и zsh, поэтому они попадают в каталог **`share`** того же
префикса (**`~/.local/bin`** → **`~/.local/share`**), а установка на уровне пользователя затем
записывает один защищённый блок в rc-файл вашей login-оболочки.

| Что делает блок | Зачем |
|---|---|
| добавляет каталог установки в **`PATH`** | новая оболочка ничего не знает о **`~/.local/bin`** |
| добавляет каталог данных в **`MANPATH`** | чтобы **`man coddy`** находил страницу |
| добавляет каталог автодополнений в начало **`fpath`** zsh и регистрирует **`_coddy`** | zsh ищет только в системных каталогах |
| подключает файл автодополнения bash | **`bash-completion`** может быть не установлен |

**`coddy update`** обновляет эти файлы вместе с бинарником, поэтому **`man coddy`** и дополнение по
Tab никогда не отстают от работающего релиза (см. [update.md](update.md#man-страница-и-автодополнения-рядом-с-бинарником)).

При каждом запуске блок переписывается между своими маркерами, а не дописывается, поэтому
повторный запуск установщика никогда его не дублирует. Чтобы пропустить блок, передайте
**`--no-shell-setup`**. Системный префикс (**`--install-dir /usr/local/bin`**) блока не получает
вовсе, потому что эти каталоги уже есть в **`PATH`**, в пути поиска man и в **`fpath`** zsh по
умолчанию.

```bash
source ~/.zshrc   # or open a new terminal
coddy -v
```

Скрипт не устанавливает юнит systemd. Чтобы в Linux **`coddy serve`** работал как сервис вашей
учётной записи, выполните **`coddy serve install`**, когда в конфигурации появится ключ провайдера.
Команда записывает **`~/.config/systemd/user/coddy.service`** для бинарника, который поставил скрипт,
включает этот юнит и запускает его в **`~/Coddy`**. Об этом скрипт и сообщает в конце работы. См.
[руководство по сервису](../operate/serve.md#как-пользовательский-сервис-systemd-в-linux).

## Пакеты для Linux (deb, rpm)

Каждый релиз рядом с архивами публикует **`.deb`** и **`.rpm`** для **x86_64** и **arm64**, так
что Coddy можно поставить так же, как остальную систему, и так же удалить. На машине, которую вы
администрируете, выбирайте этот путь вместо скрипта установки. Файлы учитывает база пакетов,
man-страница и автодополнения оболочки подключаются за вас, а `coddy update` знает, что спорить с
вашим пакетным менеджером не нужно.

**Debian, Ubuntu и производные дистрибутивы**

```bash
curl -fsSLO https://github.com/coddy-project/coddy-agent/releases/latest/download/coddy_1.0.10_linux_amd64.deb
sudo apt-get install ./coddy_1.0.10_linux_amd64.deb
```

**Fedora, RHEL, openSUSE и производные дистрибутивы**

```bash
curl -fsSLO https://github.com/coddy-project/coddy-agent/releases/latest/download/coddy_1.0.10_linux_amd64.rpm
sudo dnf install ./coddy_1.0.10_linux_amd64.rpm
```

Замените **`1.0.10`** на нужный релиз, а на 64-битном ARM замените **`amd64`** на **`arm64`**. На
[странице релизов](https://github.com/coddy-project/coddy-agent/releases) перечислено, что
опубликовал каждый тег, а **`SHA256SUMS`** рядом с файлами покрывает и пакеты.

```bash
sha256sum -c --ignore-missing SHA256SUMS
```

Пакеты предлагают **`tmux`**, но не устанавливают его. Coddy работает и без него, а консоль Coddy
хорошо работает внутри tmux, потому что сессия там переживает закрытый терминал или оборванное
SSH-соединение. **`apt`** показывает его в списке *Suggested packages*; поставьте его командой
**`sudo apt-get install tmux`** или **`sudo dnf install tmux`**.

### Что устанавливает пакет

| Путь | Что |
|------|------|
| **`/usr/bin/coddy`** | Полный бинарник (**`http`**, **`ui`**, **`scheduler`**, **`memory`**, **`cli`**, **`gateway`**, **`swarm`**) |
| **`/usr/share/man/man1/coddy.1.gz`** | **`man coddy`** |
| **`/usr/share/bash-completion/completions/coddy`** | автодополнение bash |
| **`/usr/share/zsh/site-functions/_coddy`** | автодополнение zsh |
| **`/usr/lib/systemd/user/coddy.service`** | пользовательский юнит systemd для **`coddy serve`**, установлен и **не включён** |
| **`/usr/share/doc/coddy/config.example.yaml`** | отправная точка для **`~/.coddy/config.yaml`** |
| **`/usr/share/doc/coddy/LICENSE`**, **`copyright`** | лицензия |

Пакет ни для кого не включает юнит, и в нём нет ни системного сервиса, ни системной учётной записи,
ни файлов в **`/etc`**. Конфигурация, сессии, скилы и учётные данные остаются в **`~/.coddy`**
пользователя, который запускает Coddy, поэтому один установленный пакет обслуживает всех
пользователей машины, у каждого своё состояние, и что запускать - консоль, HTTP-шлюз, редактор через
ACP, сервис - каждый пользователь решает сам. Сообщение при установке говорит, что юнит на месте и не
включён; **`coddy serve install`**, запущенный от имени пользователя, для которого предназначен
сервис, включает и запускает его (см.
[руководство по сервису](../operate/serve.md#как-пользовательский-сервис-systemd-в-linux)).

### Первый запуск

```bash
mkdir -p ~/.coddy
cp /usr/share/doc/coddy/config.example.yaml ~/.coddy/config.yaml
# set a provider key in ~/.coddy/config.yaml
coddy
```

### Обновление и удаление

Обновляйтесь через пакетный менеджер или дайте Coddy скачать пакет релиза за вас от root - оба пути
приводят к одному результату, а **`coddy update`** от обычного пользователя сообщит об этом вместо
того, чтобы молча заменить файл из пакета (см. [update.md](update.md#установки-которыми-владеет-пакетный-менеджер)).

```bash
sudo apt-get install ./coddy_<newer>_linux_amd64.deb   # or dnf install ./...rpm
sudo coddy update -y                                   # downloads and installs the package
coddy serve install                                    # if you run the service: restart it on the new binary
```

Работающий сервис держит бинарник, с которым запустился, до перезапуска, а перезапуск и выполняет
последняя строка; обновление печатает такое же напоминание.

```bash
coddy serve uninstall        # first, as each user that enabled the service
sudo apt-get remove coddy    # or: sudo dnf remove coddy
rm -rf ~/.coddy ~/Coddy      # only if you also want the sessions, config and workspace gone
```

Удаление пакета не может добраться до **`~/.config`** каждой учётной записи, поэтому включённый
сервис остаётся включённым, а удаление печатает команды, которые его убирают
([Удаление сервиса](../operate/serve.md#удаление-сервиса)).

Репозитория apt или dnf, на который можно подписаться, нет. Пакеты - это файлы релиза, поэтому
новая версия появляется, когда вы ставите более новый файл или запускаете **`sudo coddy update`**, а
фоновый **`apt upgrade`** её не принесёт.

### Самостоятельная сборка пакетов

```bash
make deb
make rpm
```

См. [build.md](../contributing/build.md#пакеты-дистрибутивов).

## macOS (Homebrew)

```bash
brew install --cask https://github.com/coddy-project/coddy-agent/releases/latest/download/coddy.rb
```

Каждый релиз рядом с архивами публикует **`coddy.rb`**, собранный с контрольными суммами
macOS-архивов того же тега. Cask ставит тот же бинарник **`coddy`**, что лежит в архиве для macOS,
а также **`man coddy`** и автодополнения bash и zsh. **`tmux`** он не ставит. Его caveats, которые
выводятся после установки и командой **`brew info --cask coddy`**, рекомендуют **`brew install tmux`**
для консольной сессии, которая переживает терминал. Удаление идёт через Homebrew.

```bash
brew uninstall --cask coddy      # brew zap --cask coddy also removes ~/.coddy
```

Чтобы обновиться, снова выполните команду установки. Homebrew отслеживает новые версии cask,
полученного из tap, но не установленного по URL.

Для **`brew install --cask coddy`** по имени Coddy должен попасть в один из собственных
репозиториев Homebrew, и этот вопрос ещё открыт. Открытые консольные программы Homebrew направляет в
**homebrew/core** как формулу, которая собирается из исходников, поэтому cask выше служит нашим
собственным каналом распространения и в Homebrew не подаётся. Весь путь, включая то, что
сейчас его блокирует, описан в [homebrew.md](homebrew.md).

**`coddy update`** распознаёт установку через Homebrew и отсылает к **`brew upgrade`** вместо того,
чтобы заменять файл, который отслеживает Homebrew, - **`brew upgrade --cask coddy`** для этого cask
и **`brew upgrade coddy`** для формулы. Homebrew отказывается работать под **`sudo`**, так что
привилегированного обходного пути здесь нет.

Если macOS блокирует первый запуск, потому что бинарник не нотаризован, снимите флаг карантина
командой **`xattr -d com.apple.quarantine "$(which coddy)"`**.

## Android (Termux)

```bash
pkg install curl
curl -fsSL https://coddy.dev/install.sh | bash
```

В Termux скрипт скачивает сборку для Android - **`coddy_X.Y.Z_android_arm64.tar.gz`** на
64-битном ARM-устройстве и **`coddy_X.Y.Z_android_amd64.tar.gz`** на x86_64. Архив для Linux там
не запускается. Termux, нацеленный на Android 10 и новее, запускает каждую программу через
компоновщик Android, а тот отвергает статический исполняемый файл с ошибкой
**`has unexpected e_type: 2`**. В остальном установка такая же, как в Linux - **`~/.local/bin`**,
man-страница и автодополнения в **`~/.local/share`** и блок в **`~/.bashrc`**. То, что на
устройстве отличается (программы, которые запускает Coddy, сертификаты, работа **`coddy serve`** в
фоне), описано на отдельной странице [Android (Termux)](android.md).

## После установки

```bash
export PATH="$HOME/.local/bin:$PATH"
coddy -v
# edit ~/.coddy/config.yaml
coddy serve            # in this terminal
coddy serve --daemon   # in the background, restarted if it dies
coddy serve install    # Linux: as a systemd user service
```

В Linux с systemd **`coddy serve install`** вместо этого запускает Coddy как пользовательский сервис
вашей учётной записи. Команда включает юнит, который установил пакет, или записывает свой для
бинарника, который поставил скрипт установки, и запускает сервис с рабочей папкой **`~/Coddy`**.
**`coddy serve uninstall`** снова убирает его. Про лог, работу после выхода из системы и удаление
читайте в [руководстве по сервису](../operate/serve.md#как-пользовательский-сервис-systemd-в-linux). Там, где
systemd нет, используйте **`coddy serve --daemon`**.

## Windows

### Куда ставится Coddy

| Что | Путь |
|------|------|
| Бинарник | `%LOCALAPPDATA%\Programs\coddy\coddy.exe` |
| Конфигурация | `%USERPROFILE%\.coddy\config.yaml` |
| Сессии и память | `%USERPROFILE%\.coddy\sessions\` |

Каталог пользователя берётся из **`$env:USERPROFILE`** (`%USERPROFILE%`), `$HOME` для этого **не** подходит, потому что в Windows PowerShell и Git Bash на `$HOME` нельзя полагаться (в Git Bash `$HOME` может отличаться от `%USERPROFILE%`).

### PATH в текущей сессии

`install.ps1` добавляет каталог бинарника в **пользовательский** `PATH`. Новые терминалы подхватывают его автоматически, а терминал, из которого вы устанавливали, **нет**. Откройте новый терминал или обновите переменную на месте.

```powershell
$env:Path = [Environment]::GetEnvironmentVariable("Path","User") + ";" + [Environment]::GetEnvironmentVariable("Path","Machine")
```

(Если у вас есть Chocolatey, подойдёт и `refreshenv`.)

### Интеграции с редакторами и агентами - указывайте абсолютный путь

Некоторые харнесы запускают **`coddy acp`** через `cmd /c` или `sh -c` и не наследуют пользовательский `PATH`. Чтобы не ловить ошибки подключения вида "command not found", указывайте в клиентах абсолютный путь.

```text
%LOCALAPPDATA%\Programs\coddy\coddy.exe
```

### Пакетные менеджеры

Манифесты Scoop и winget пока не опубликованы, работа над ними отслеживается в [issue #42](https://github.com/coddy-project/coddy-agent/issues/42). До тех пор используйте `install.ps1` выше и обновляйтесь командой `coddy update -y`.

## Docker

```bash
docker compose pull && docker compose up -d
```

См. [Docker](docker.md).

## Обновление

```bash
coddy update -y
```

См. [update.md](update.md).

## Сборка из исходников

См. [build.md](../contributing/build.md) и раздел README **Other installation methods**.

<!-- docsgen:source sha256=cccf085abf891453 -->
