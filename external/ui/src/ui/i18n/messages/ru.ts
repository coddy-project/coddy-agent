/** Russian UI strings for the Coddy SPA. Key set mirrors messages/en.ts. */
export const messagesRu: Record<string, string> = {
  "appearance.themeLabel": "Тема",
  "appearance.themeGroupAria": "Тема",
  "appearance.languageLabel": "Язык",
  "appearance.locale.auto": "Авто",
  "appearance.locale.en": "English",
  "appearance.locale.ru": "Русский",
  "appearance.toggleLabel": "Оформление",
  "appearance.theme.dark": "Тёмная",
  "appearance.theme.light": "Светлая",
  "appearance.theme.midnight": "Полночь",
  "appearance.theme.solarizedDark": "Solarized Dark",
  "appearance.theme.monokai": "Monokai",
  "appearance.theme.nord": "Nord",
  "appearance.theme.rosePine": "Rosé Pine",

  "common.cancel": "Отмена",
  "common.confirm": "Подтвердить",
  "common.confirmAction": "Подтвердить действие",
  "common.delete": "Удалить",

  "confirm.session.deleteDraft.title": "Удалить черновик?",
  "confirm.session.deleteDraft.message": "Этот черновик беседы будет удалён.",
  "confirm.session.deleteChat.title": "Удалить чат?",
  "confirm.session.deleteChat.message":
    "Эта беседа будет удалена без возможности восстановления.",
  "confirm.scheduler.deleteJob.title": "Удалить задачу планировщика «{id}»?",
  "confirm.scheduler.deleteJob.message":
    "Эта запланированная задача будет удалена без возможности восстановления.",

  "settings.title": "Настройки",
  "settings.aria.panel": "Настройки",
  "settings.aria.close": "Закрыть настройки",
  "settings.backToSections": "Назад к разделам",
  "settings.lead":
    "Редактируйте конфигурацию по живой JSON-схеме. Секреты (API-ключи) показываются полностью — используйте только в доверенных сетях.",
  "settings.loading": "Загрузка…",
  "settings.toast.saved": "Все разделы сохранены. Конфигурация перезагружена.",
  "settings.reload.title": "Перезагрузить с сервера",
  "settings.reload.aria": "Перезагрузить конфигурацию с сервера",
  "settings.save.title": "Сохранить все разделы",
  "settings.save.aria": "Сохранить все разделы конфигурации",
  "settings.error.schemaLoadFailed": "схема",
  "settings.error.configLoadFailed": "конфиг",
  "settings.error.validationFailed": "ошибка валидации",
  "settings.error.saveFailed": "не удалось сохранить ({status})",
  "settings.error.requestFailed": "ошибка запроса",
  "settings.error.failedToLoad": "Не удалось загрузить: {error}",
  "settings.error.unsupportedSchemaRoot":
    "Неподдерживаемый корень схемы (ожидается объект).",
  "settings.error.skillsSchemaUnavailable": "Схема навыков недоступна.",
  "settings.error.sectionSchemaUnavailable": "Схема раздела недоступна.",
  "settings.error.noItemSchema": "У этого раздела нет схемы элементов.",

  "settings.section.appearance.label": "Оформление",
  "settings.section.providers.label": "Провайдеры LLM",
  "settings.section.models.label": "Логические модели",
  "settings.section.agent.label": "ReAct-агент",
  "settings.section.tools.label": "Инструменты и разрешения",
  "settings.section.mcp_servers.label": "MCP-серверы",
  "settings.section.skills.label": "Навыки",
  "settings.section.memory.label": "Долговременная память",
  "settings.section.system.label": "Система",
  "settings.section.compaction.label": "Сжатие контекста",
  "settings.section.appearance.desc": "Тема и цветовой режим",
  "settings.section.providers.desc": "Подключения LLM API",
  "settings.section.models.desc": "Именованные конфигурации моделей",
  "settings.section.agent.desc": "Параметры агента ReAct",
  "settings.section.tools.desc": "Разрешения и лимиты инструментов",
  "settings.section.mcp_servers.desc": "Внешние MCP-инструменты",
  "settings.section.skills.desc": "Установленные навыки (slash)",
  "settings.section.memory.desc": "Параметры долговременной памяти",
  "settings.section.system.desc": "Планировщик, логи, промпты",
  "settings.section.compaction.desc": "Сжатие истории диалога",

  "settings.nav.aria.scrollLeft": "Прокрутить разделы влево",
  "settings.nav.aria.scrollRight": "Прокрутить разделы вправо",
  "settings.nav.aria.sections": "Разделы настроек",
  "settings.tileGrid.aria": "Разделы настроек",

  "settings.array.add": "Добавить",
  "settings.array.removeTitle": "Удалить",
  "settings.array.removeAria": "Удалить",
  "settings.array.removeRowAria": "Удалить {name}",
  "settings.array.unnamed": "(без имени #{n})",
  "settings.array.back": "Назад к списку",
  "settings.array.backTitle": "Назад к списку",
  "settings.array.empty":
    "Здесь пока пусто. Используйте «Добавить», чтобы создать.",

  "settings.field.apiBaseFallback": "Базовый URL API",
  "settings.field.modelIdFallback": "Идентификатор модели",
  "settings.field.defaultModelFallback": "Модель по умолчанию",
  "settings.field.providerAria": "Провайдер",
  "settings.field.providerPlaceholder": "провайдер",
  "settings.field.modelPlaceholder": "провайдер/идентификатор-модели",
  "settings.field.fetching": "Получение…",
  "settings.field.fetchModels": "Получить модели",
  "settings.field.fetchError":
    "Не удалось получить модели: {error}. Введите идентификатор модели вручную ниже.",
  "settings.field.noModels":
    "Модели не возвращены. Введите идентификатор модели вручную ниже.",
  "settings.field.apiKeyPlaceholder":
    "Если пусто, читается из {env} во время выполнения, либо укажите ключ явно (в YAML можно использовать {varToken} при загрузке)",
  "settings.field.apiKeyPlaceholderInvalid":
    "Идентификатор провайдера должен начинаться с буквы. Когда идентификатор корректен, оставьте пустым, чтобы читать переменную NAME_API_KEY (NAME — верхний регистр, дефисы становятся подчёркиваниями).",

  // Schema-driven settings fields: labels and descriptions rendered from the
  // server JSON Schema (internal/config/ui_schema.go), keyed
  // settings.schema.<section>.<dotted.field.path>.label / .desc and resolved by
  // settings/schemaI18n.ts. System-group children live under
  // settings.schema.system.<child>.<path>.
  "settings.schema.providers.desc":
    "Учётные данные API и выбор транспорта для внешних LLM-провайдеров.",
  "settings.schema.providers.name.label": "Имя провайдера",
  "settings.schema.providers.name.desc":
    "Логический идентификатор, используемый в id моделей (provider/model-id). Только латинские буквы, цифры, дефис и подчёркивание; должен начинаться с буквы. Если api_key пуст, исполняющая среда читает ключ из переменной окружения NAME_API_KEY (NAME — это значение поля в верхнем регистре, дефисы заменяются на подчёркивания).",
  "settings.schema.providers.type.label": "Тип провайдера",
  "settings.schema.providers.type.desc":
    "Сетевой протокол для этой записи провайдера.",
  "settings.schema.providers.api_base.label": "Базовый URL API",
  "settings.schema.providers.api_base.desc":
    "Необязательное переопределение базового URL API для этого провайдера. Игнорируется для neuraldeep и codex — они используют фиксированные официальные адреса.",
  "settings.schema.providers.api_key.label": "API-ключ",
  "settings.schema.providers.api_key.desc":
    "Можно указать ключ напрямую, сослаться на ${ENV} в YAML (разворачивается при загрузке файла) или оставить пустым — тогда процесс прочитает стандартную переменную NAME_API_KEY, производную от имени провайдера (см. описание поля «Имя провайдера»).",
  "settings.schema.providers.api_key_command.label":
    "Команда получения API-ключа",
  "settings.schema.providers.api_key_command.desc":
    "Необязательная команда для получения ключа. Когда api_key пуст, она запускается через обнаруженный шелл хоста (pwsh, powershell или cmd на Windows; bash или sh в остальных случаях), и её вывод без краевых пробелов используется как ключ (как git/docker credential helpers или AWS credential_process). При ошибке используется стандартная переменная NAME_API_KEY.",
  "settings.schema.providers.proxy.label": "HTTP- или SOCKS-прокси",
  "settings.schema.providers.proxy.desc":
    "Необязательный исходящий прокси для провайдера. http:// или https:// — HTTP-прокси, socks5:// / socks5h:// — SOCKS5 (socks5h резолвит имена хостов через прокси). Пусто — прямое соединение.",
  "settings.schema.providers.timeout_ms.label": "Таймаут запроса, мс",
  "settings.schema.providers.timeout_ms.desc":
    "Необязательный предел на каждый HTTP-запрос к LLM этого провайдера, включая чтение потокового тела ответа. 0 (по умолчанию) — без клиентского таймаута.",

  "settings.schema.models.desc":
    "Именованные записи моделей, которые агент и UI могут выбирать; id ссылаются на префиксы провайдеров.",
  "settings.schema.models.model.label": "Идентификатор модели",
  "settings.schema.models.model.desc":
    "Логический идентификатор вида provider/api-model-id; префикс должен совпадать с именем провайдера.",
  "settings.schema.models.max_tokens.label": "Максимум токенов",
  "settings.schema.models.max_tokens.desc":
    "Верхний предел токенов завершения, которые модель может выдать за одно сообщение ассистента. Игнорируется Codex: его бэкенд не принимает max_output_tokens.",
  "settings.schema.models.temperature.label": "Температура",
  "settings.schema.models.temperature.desc":
    "Температура сэмплирования для этой логической модели (0 = детерминированно, выше = более случайно).",
  "settings.schema.models.max_context_tokens.label":
    "Максимум токенов контекста (подсказка UI)",
  "settings.schema.models.max_context_tokens.desc":
    "Необязательная подсказка для индикатора контекста в композере; 0 — определить по метаданным провайдера, когда они доступны.",
  "settings.schema.models.multimodal.label": "Мультимодальная",
  "settings.schema.models.multimodal.desc":
    "Если включено, модель принимает изображения или файлы в дополнение к тексту. UI предложит прикрепление файлов для сообщений, отправляемых с этой моделью.",
  "settings.schema.models.reasoning_levels.label": "Уровни рассуждения",
  "settings.schema.models.reasoning_levels.desc":
    "Необязательное переопределение уровней рассуждения для этой модели (например, low, medium, high). Пусто — автоопределение по идентификатору модели; явный пустой список скрывает селектор уровней.",
  "settings.schema.models.reasoning_default.label":
    "Уровень рассуждения по умолчанию",
  "settings.schema.models.reasoning_default.desc":
    "Уровень рассуждения, предвыбранный для новых чатов с этой моделью. Должен быть одним из разрешённых уровней рассуждения, иначе игнорируется.",
  "settings.schema.models.stream.label": "Потоковые ответы",
  "settings.schema.models.stream.desc":
    "Оставьте включённым, чтобы получать ответ токен за токеном по SSE. Выключите, чтобы отправлять один блокирующий запрос и ждать ответ целиком — для серверов и прокси, которые плохо работают с потоками событий; транскрипт тогда заполнится разом, а не по мере набора. Недоступно для моделей codex: их бэкенд работает только в потоковом режиме.",

  "settings.schema.agent.model.label": "Модель по умолчанию",
  "settings.schema.agent.model.desc":
    "Логический идентификатор модели из списка моделей, используемый, когда клиент не указал модель.",
  "settings.schema.agent.max_turns.label": "Максимум итераций",
  "settings.schema.agent.max_turns.desc":
    "Жёсткий предел итераций ReAct (вызовы LLM плюс раунды инструментов) на один запрос пользователя.",
  "settings.schema.agent.max_tokens_per_turn.label": "Максимум токенов на шаг",
  "settings.schema.agent.max_tokens_per_turn.desc":
    "Верхний предел всех токенов (промпт + ответ) для одного шага агента.",
  "settings.schema.agent.llm_retry_max.label": "Максимум повторов LLM",
  "settings.schema.agent.llm_retry_max.desc":
    "Повторы после исправимых ошибок LLM (например, HTTP 429) перед провалом шага (явный 0 отключает повторы).",
  "settings.schema.agent.llm_retry_base_ms.label": "База повтора LLM, мс",
  "settings.schema.agent.llm_retry_base_ms.desc":
    "Начальная пауза между повторами LLM в миллисекундах; серверная пауза (Retry-After) имеет приоритет.",
  "settings.schema.agent.llm_min_interval_ms.label": "Мин. интервал LLM, мс",
  "settings.schema.agent.llm_min_interval_ms.desc":
    "Минимальный интервал между последовательными вызовами LLM в миллисекундах, повторы включены (0 отключает троттлинг).",
  "settings.schema.agent.llm_first_token_timeout_ms.label":
    "Таймаут первого токена LLM, мс",
  "settings.schema.agent.llm_first_token_timeout_ms.desc":
    "Как долго потоковый вызов LLM может оставаться без ответа, прежде чем шаг будет отменён (явный 0 отключает защиту).",
  "settings.schema.agent.loop_guard.label": "Защита от зацикливания",
  "settings.schema.agent.loop_guard.desc":
    "Останавливает ответ, вырождающийся в повторение самого себя, и блокирует инструмент, вызываемый раз за разом с одинаковыми аргументами.",
  "settings.schema.agent.loop_tool_repeat_limit.label":
    "Предел повторов инструмента",
  "settings.schema.agent.loop_tool_repeat_limit.desc":
    "Сколько одинаковых вызовов инструмента подряд допустимо, прежде чем защита от зацикливания вмешается (0 отключает проверку).",
  "settings.schema.agent.loop_stream_repeat_cycles.label":
    "Циклы повтора в потоке",
  "settings.schema.agent.loop_stream_repeat_cycles.desc":
    "Сколько одинаковых подряд идущих циклов вывода внутри одного потокового ответа допустимо, прежде чем он будет обрезан (0 отключает проверку).",
  "settings.schema.agent.loop_nudge_max.label": "Максимум поправок",
  "settings.schema.agent.loop_nudge_max.desc":
    "Сколько раз за один шаг модель можно вернуть на путь, прежде чем защита от зацикливания остановит его.",

  "settings.schema.tools.permission_mode.label": "Режим разрешений",
  "settings.schema.tools.permission_mode.desc":
    "Определяет, когда агент запрашивает одобрение перед запуском инструментов. «ask» — подтверждать команды и запись файлов. «accept_edits» — автоматически принимать правки, подтверждать команды. «bypass» — не спрашивать вовсе.",
  "settings.schema.tools.command_allowlist.label": "Белый список команд",
  "settings.schema.tools.command_allowlist.desc":
    "Если не пуст, без дополнительной политики могут запускаться только команды с этими префиксами.",
  "settings.schema.tools.output_limits.label": "Лимиты вывода инструментов",
  "settings.schema.tools.output_limits.desc":
    "Максимум строк, которые результат или ошибка инструмента может вернуть в контекст LLM. Включённые лимиты также накладывают страховочный потолок 64 КиБ на вызов. 0 отключает оба лимита; не задано — встроенное значение по умолчанию.",
  "settings.schema.tools.output_limits.read.desc":
    "Максимум строк страницы чтения файла или листинга каталога (по умолчанию 1000).",
  "settings.schema.tools.output_limits.grep.desc":
    "Максимум записей path:line:content от grep (по умолчанию 200).",
  "settings.schema.tools.output_limits.glob.desc":
    "Максимум путей от glob (по умолчанию 300).",
  "settings.schema.tools.output_limits.print_tree.desc":
    "Максимум строк дерева каталогов (по умолчанию 400).",
  "settings.schema.tools.output_limits.run_command.desc":
    "Максимум строк stdout+stderr команды шелла (по умолчанию 500).",
  "settings.schema.tools.output_limits.ssh_run_command.desc":
    "Максимум строк stdout+stderr удалённой SSH-команды (по умолчанию 500).",
  "settings.schema.tools.output_limits.webfetch.desc":
    "Максимум строк markdown загруженной страницы (по умолчанию 800).",
  "settings.schema.tools.output_limits.websearch.desc":
    "Максимум строк результатов поиска (по умолчанию 200).",
  "settings.schema.tools.output_limits.default.desc":
    "Применяется к любому не перечисленному инструменту, включая MCP (по умолчанию 1000; 0 = без лимита).",
  "settings.schema.tools.background.label": "Фоновые задачи",
  "settings.schema.tools.background.desc":
    "Команды, которые агент запускает отсоединённо в пуле задач сессии, не блокируя шаг.",
  "settings.schema.tools.background.enabled.label": "Включено",
  "settings.schema.tools.background.enabled.desc":
    "Показывать фоновую опцию у run_command и инструменты фоновых задач (по умолчанию включено).",
  "settings.schema.tools.background.max_concurrent.label":
    "Максимум одновременно",
  "settings.schema.tools.background.max_concurrent.desc":
    "Сколько фоновых задач одна сессия может запускать одновременно (по умолчанию 5).",
  "settings.schema.tools.background.default_timeout_seconds.label":
    "Таймаут по умолчанию (с)",
  "settings.schema.tools.background.default_timeout_seconds.desc":
    "Жёсткий предел для задачи, запущенной без таймаута и оценки длительности (по умолчанию 900).",
  "settings.schema.tools.background.max_timeout_seconds.label":
    "Максимум таймаут (с)",
  "settings.schema.tools.background.max_timeout_seconds.desc":
    "Потолок для любого запрошенного или оценённого таймаута (по умолчанию 3600).",
  "settings.schema.tools.background.output_buffer_bytes.label":
    "Буфер вывода (байт)",
  "settings.schema.tools.background.output_buffer_bytes.desc":
    "Какая часть вывода каждой задачи хранится в памяти для тикера; полный лог всё равно попадает в бандл сессии (по умолчанию 262144).",

  "settings.schema.skills.dirs.label": "Каталоги навыков",
  "settings.schema.skills.dirs.desc":
    "Пути поиска навыков. По умолчанию: ~/.agents/skills (глобальные, совместно с npx skills / npx skillsbd), ${CODDY_HOME}/skills (специфичные для coddy), ${CWD}/.coddy/skills (проектные). ${CODDY_HOME} и ${CWD} разворачиваются во время выполнения.",
  "settings.schema.skills.auto_discovery.desc":
    "Разрешить агенту самостоятельно загружать полные инструкции подходящего навыка (инструмент load_skill, управляемый моделью), а не только по команде /имя. По умолчанию включено.",

  "settings.schema.memory.enabled.label": "Включено",
  "settings.schema.memory.enabled.desc":
    "Включает копилот памяти для подходящих сборок.",
  "settings.schema.memory.model.label": "Модель памяти",
  "settings.schema.memory.model.desc":
    "Логическая модель для вызовов LLM памяти; пусто — модель агента.",
  "settings.schema.memory.dir.label": "Корень памяти",
  "settings.schema.memory.dir.desc":
    "Файловый корень markdown-файлов памяти; пусто — ${CODDY_HOME}/memory.",
  "settings.schema.memory.recall_max_turns.label":
    "Максимум раундов выборки",
  "settings.schema.memory.recall_max_turns.desc":
    "Предел раундов LLM на стороне выборки в цикле памяти.",
  "settings.schema.memory.persist_max_turns.label":
    "Максимум раундов сохранения",
  "settings.schema.memory.persist_max_turns.desc":
    "Предел раундов LLM на стороне сохранения в цикле памяти.",
  "settings.schema.memory.copilot_max_tokens.label":
    "Максимум токенов копилота",
  "settings.schema.memory.copilot_max_tokens.desc":
    "Предел токенов завершения для вызовов копилота памяти.",
  "settings.schema.memory.max_search_hits.label":
    "Максимум результатов поиска",
  "settings.schema.memory.max_search_hits.desc":
    "Максимум фрагментов, возвращаемых инструментами поиска по памяти.",

  "settings.schema.compaction.enabled.label": "Включено",
  "settings.schema.compaction.enabled.desc":
    "Главный выключатель сжатия (ручная команда и автоматический триггер). По умолчанию включено.",
  "settings.schema.compaction.threshold_percent.label":
    "Порог авто-сжатия (%)",
  "settings.schema.compaction.threshold_percent.desc":
    "Авто-сжатие, когда оценка контекста достигает этой доли от max_context_tokens модели (1..100, по умолчанию 80). Модели без max_context_tokens пропускают авто-сжатие.",
  "settings.schema.compaction.keep_recent_turns.label":
    "Сохранять последние ходы",
  "settings.schema.compaction.keep_recent_turns.desc":
    "Сколько последних ходов пользователя остаются дословными после сжатия (по умолчанию 2; 0 — суммируется всё).",
  "settings.schema.compaction.model.label": "Модель суммаризации",
  "settings.schema.compaction.model.desc":
    "Необязательный models[].model для вызова суммаризации; пусто — модель сессии.",
  "settings.schema.compaction.result_eviction.label":
    "Вытеснение результатов read/grep",
  "settings.schema.compaction.result_eviction.desc":
    "Свёртывает устаревшие результаты read/grep в плейсхолдеры при сборке запроса LLM; сохранённый транскрипт не меняется. Выживают только помеченные (keep_result / keep:true) или самые свежие результаты.",
  "settings.schema.compaction.result_eviction.enabled.label": "Включено",
  "settings.schema.compaction.result_eviction.enabled.desc":
    "Главный выключатель вытеснения результатов read/grep. По умолчанию включено.",
  "settings.schema.compaction.result_eviction.keep_recent.label":
    "Сохранять последние результаты",
  "settings.schema.compaction.result_eviction.keep_recent.desc":
    "Сколько самых свежих вытесняемых результатов остаются целыми как рабочее окно (по умолчанию 2 — достаточно для одновременных read и grep; 0 — не сохранять ничего).",
  "settings.schema.compaction.result_eviction.min_result_bytes.label":
    "Мин. размер результата, байт",
  "settings.schema.compaction.result_eviction.min_result_bytes.desc":
    "Результаты этого размера и меньше никогда не вытесняются (по умолчанию 2000; 0 — кандидат любой результат).",

  "settings.schema.system.scheduler.label": "Планировщик",
  "settings.schema.system.scheduler.enabled.label": "Включено",
  "settings.schema.system.scheduler.enabled.desc":
    "Когда включено, этот процесс может запускать демон планировщика и REST API.",
  "settings.schema.system.scheduler.dir.label": "Каталог заданий",
  "settings.schema.system.scheduler.dir.desc":
    "Каталог markdown-определений заданий.",
  "settings.schema.system.scheduler.max_queue.label": "Максимум очереди",
  "settings.schema.system.scheduler.max_queue.desc":
    "Максимум одновременных запусков агента по расписанию.",
  "settings.schema.system.scheduler.timeout.label": "Таймаут задания",
  "settings.schema.system.scheduler.timeout.desc":
    "Предельное время выполнения задания, например 30m или 1h30m.",
  "settings.schema.system.scheduler.retain_sessions.label":
    "Хранить сессий",
  "settings.schema.system.scheduler.retain_sessions.desc":
    "Сколько папок завершённых сессий планировщика хранить на каждый идентификатор задания.",
  "settings.schema.system.prompts.label": "Промпты",
  "settings.schema.system.prompts.dir.label": "Каталог промптов",
  "settings.schema.system.prompts.dir.desc":
    "Необязательный каталог переопределения для markdown-файлов промптов.",
  "settings.schema.system.prompts.agent_prompt.label": "Файл промпта агента",
  "settings.schema.system.prompts.agent_prompt.desc":
    "Имя файла системного промпта основного агента.",
  "settings.schema.system.prompts.plan_prompt.label":
    "Файл промпта планирования",
  "settings.schema.system.prompts.plan_prompt.desc":
    "Имя файла системного промпта режима планирования.",
  "settings.schema.system.instructions.label": "Инструкции",
  "settings.schema.system.instructions.files.label": "Файлы инструкций",
  "settings.schema.system.instructions.files.desc":
    "Имена файлов относительно рабочего каталога сессии, читаемые как инструкции. По умолчанию [\"AGENTS.md\"].",
  "settings.schema.system.logger.label": "Логирование",
  "settings.schema.system.logger.level.label": "Уровень",
  "settings.schema.system.logger.level.desc":
    "Минимальная важность, записываемая в настроенные приёмники.",
  "settings.schema.system.logger.outputs.label": "Приёмники",
  "settings.schema.system.logger.outputs.desc":
    "Куда записываются строки лога.",
  "settings.schema.system.logger.file.label": "Путь к файлу лога",
  "settings.schema.system.logger.file.desc":
    "Файл назначения, когда приёмники включают file.",
  "settings.schema.system.logger.format.label": "Формат",
  "settings.schema.system.logger.format.desc":
    "text — человекочитаемые логи; json — структурированные.",
  "settings.schema.system.logger.rotation.label": "Ротация",
  "settings.schema.system.logger.rotation.desc":
    "Ротация по размеру при логировании в файл.",
  "settings.schema.system.logger.rotation.max_size_mb.label":
    "Макс. размер файла (МБ)",
  "settings.schema.system.logger.rotation.max_size_mb.desc":
    "Ротировать после достижения файлом этого размера; 0 — значения по умолчанию логгера.",
  "settings.schema.system.logger.rotation.max_files.label": "Максимум файлов",
  "settings.schema.system.logger.rotation.max_files.desc":
    "Сколько сегментов ротации хранить; 0 — значения по умолчанию логгера.",
  "settings.schema.system.sessions.label": "Сессии",
  "settings.schema.system.sessions.dir.label": "Каталог сессий",
  "settings.schema.system.sessions.dir.desc":
    "Переопределение корня сессий; пусто — внутри CODDY_HOME.",
  "settings.schema.system.gateways.label": "Шлюзы мессенджеров",
  "settings.schema.system.gateways.telegram.label": "Telegram",
  "settings.schema.system.gateways.telegram.desc":
    "Настройки адаптера Telegram-бота.",
  "settings.schema.system.gateways.telegram.enabled.label": "Включено",
  "settings.schema.system.gateways.telegram.enabled.desc":
    "Запускать Telegram-бота (требуется сборочный тег gateway или gateway.telegram).",
  "settings.schema.system.gateways.telegram.token.label": "Токен бота",
  "settings.schema.system.gateways.telegram.token.desc":
    "Токен от BotFather. Здесь необязателен — оставьте пустым, чтобы читать из переменной окружения TELEGRAM_BOT_TOKEN (например, через .env). Секрет: если задан, хранится в config.yaml и показывается целиком.",
  "settings.schema.system.gateways.telegram.rich_messages.label":
    "Rich messages",
  "settings.schema.system.gateways.telegram.rich_messages.desc":
    "Использовать Rich Messages Bot API 10.1: встроенный Markdown агента рендерится дословно, активность инструментов стримится как плейсхолдер Thinking, а выполненные инструменты показываются сворачиваемым блоком. При отсутствии поддержки откатывается к прежнему форматированию.",
  "settings.schema.system.gateways.telegram.proxy.label": "Прокси",
  "settings.schema.system.gateways.telegram.proxy.desc":
    "Необязательный исходящий прокси для запросов к Telegram API. http, https, socks5 или socks5h.",
  "settings.schema.system.gateways.telegram.admins.label":
    "Администраторы",
  "settings.schema.system.gateways.telegram.admins.desc":
    "Идентификаторы пользователей Telegram с расширенными правами; администраторы всегда проходят проверку доступа.",
  "settings.schema.system.gateways.telegram.default_access.label":
    "Доступ по умолчанию",
  "settings.schema.system.gateways.telegram.default_access.desc":
    "Резервный уровень доступа для чатов без переопределения: all, admins или group:<имя>.",
  "settings.schema.system.gateways.telegram.default_isolation.label":
    "Изоляция по умолчанию",
  "settings.schema.system.gateways.telegram.default_isolation.desc":
    "Резервная изоляция сессий для групповых чатов.",
  "settings.schema.system.gateways.telegram.user_groups.label":
    "Группы пользователей",
  "settings.schema.system.gateways.telegram.user_groups.desc":
    "Именованные наборы идентификаторов пользователей, на которые ссылается access как group:<имя>.",
  "settings.schema.system.gateways.telegram.user_groups.name.label":
    "Имя группы",
  "settings.schema.system.gateways.telegram.user_groups.name.desc":
    "Имя, на которое ссылается access как group:<имя>.",
  "settings.schema.system.gateways.telegram.user_groups.user_ids.label":
    "Идентификаторы пользователей",
  "settings.schema.system.gateways.telegram.user_groups.user_ids.desc":
    "Числовые идентификаторы Telegram, входящие в эту группу.",
  "settings.schema.system.gateways.telegram.chats.label":
    "Переопределения по чатам",
  "settings.schema.system.gateways.telegram.chats.desc":
    "Переопределение изоляции и доступа для отдельных чатов.",
  "settings.schema.system.gateways.telegram.chats.chat_id.label":
    "Идентификатор чата",
  "settings.schema.system.gateways.telegram.chats.chat_id.desc":
    "Идентификатор чата Telegram; отрицательный для групп и супергрупп.",
  "settings.schema.system.gateways.telegram.chats.isolation.label":
    "Изоляция",
  "settings.schema.system.gateways.telegram.chats.isolation.desc":
    "Переопределение изоляции сессий для конкретного чата.",
  "settings.schema.system.gateways.telegram.chats.access.label": "Доступ",
  "settings.schema.system.gateways.telegram.chats.access.desc":
    "Переопределение доступа для конкретного чата: all, admins или group:<имя>.",

  "settings.combobox.toggleAria": "Показать параметры",

  "codexAuth.error.signInFailed": "Не удалось войти через ChatGPT.",
  "codexAuth.error.incompleteResponse":
    "Сервер OAuth вернул неполный ответ входа.",
  "codexAuth.connected.viaCli":
    "Подключено через вход Codex CLI на этом сервере.",
  "codexAuth.connected.withChatGpt": "Подключено через ChatGPT.",
  "codexAuth.fieldLabel": "Аккаунт ChatGPT",
  "codexAuth.description":
    "Codex использует вашу подписку ChatGPT через OAuth. Учётные данные хранятся на сервере Coddy и никогда не добавляются в config.yaml.",
  "codexAuth.enterCode": "Введите этот одноразовый код на странице ChatGPT:",
  "codexAuth.openSignInPage": "Открыть страницу входа",
  "codexAuth.waiting": "Ожидание подтверждения…",
  "codexAuth.signingOut": "Выход…",
  "codexAuth.signOut": "Выйти",
  "codexAuth.waitingForChatGpt": "Ожидание ChatGPT…",
  "codexAuth.signInWithChatGpt": "Войти через ChatGPT",
  "codexAuth.enterProviderName": "Введите имя провайдера перед входом.",

  "neuralDeepAuth.error.signInFailed": "Не удалось войти в NeuralDeep.",
  "neuralDeepAuth.error.incompleteResponse":
    "Хаб вернул неполный ответ входа.",
  "neuralDeepAuth.fieldLabel": "Аккаунт NeuralDeep",
  "neuralDeepAuth.description":
    "Войдите под учёткой хаба NeuralDeep вместо ручной вставки ключа: хаб выдаст персональный ключ для Coddy. Ключ хранится на сервере Coddy и не попадает в config.yaml. Модели тарифа добавьте в разделе Логические модели - пикер моделей подтянет каталог с этим входом.",
  "neuralDeepAuth.connected": "Выполнен вход в NeuralDeep ({masked}).",
  "neuralDeepAuth.shadowedByKey":
    "Задан явный API-ключ, поэтому запросы используют его, а не этот вход. Очистите поле api_key, чтобы использовать вход.",
  "neuralDeepAuth.enterCode": "Введите этот одноразовый код на странице NeuralDeep:",
  "neuralDeepAuth.openSignInPage": "Открыть страницу входа",
  "neuralDeepAuth.waiting": "Ожидание подтверждения…",
  "neuralDeepAuth.signingOut": "Выход…",
  "neuralDeepAuth.signOut": "Выйти",
  "neuralDeepAuth.waitingForHub": "Ожидание NeuralDeep…",
  "neuralDeepAuth.signIn": "Войти через NeuralDeep",
  "neuralDeepAuth.enterProviderName": "Введите имя провайдера перед входом.",

  "mcp.status.connectedOne": "Подключён — {count} инструмент",
  "mcp.status.connectedMany": "Подключён — {count} инструментов",
  "mcp.status.probeFailed": "Не удалось опросить",
  "mcp.status.disabled": "Отключён",
  "mcp.status.needsApproval":
    "Ожидает вашего одобрения — не запущен, не опрашивался",
  "mcp.status.denied":
    "Проектные MCP-серверы выключены параметром mcp.project_trust: deny",
  "mcp.status.unsupported": "Транспорт не поддерживается",
  "mcp.error.toggleServer": "Не удалось {action} {name}",
  "mcp.error.toggleTool": "Не удалось {action} {tool}",
  "mcp.error.changeTrustPolicy": "Не удалось изменить политику доверия проекта",
  "mcp.error.toggleTrust": "Не удалось {action} {name}",
  "mcp.error.delete": "Не удалось удалить {name}",
  "mcp.error.invalidEntry": "Некорректная запись.",
  "mcp.error.saveServer": "Не удалось сохранить сервер",
  "mcp.discovery.legend": "Обнаружение MCP",
  "mcp.discovery.projectServersLabel": "Проектные серверы",
  "mcp.servers.legend": "Серверы MCP",
  "mcp.addServer": "Добавить сервер",
  "mcp.refresh.title": "Опросить все серверы заново",
  "mcp.refresh.aria": "Обновить серверы MCP",
  "mcp.loading": "Загрузка…",
  "mcp.expand.collapse": "Свернуть инструменты",
  "mcp.expand.expand": "Развернуть инструменты",
  "mcp.expand.aria": "{action} инструменты {name}",
  "mcp.badge.definedIn": "Определено в {origin}",
  "mcp.trust.approvedTitle":
    "Одобрено для этого рабочего пространства ({fingerprint}) — нажмите, чтобы отозвать",
  "mcp.trust.approveTitle":
    "Одобрить запуск «{target}» в этом рабочем пространстве",
  "mcp.trust.withdrawAria": "Отозвать одобрение MCP-сервера {name}",
  "mcp.trust.approveAria": "Одобрить MCP-сервер {name}",
  "mcp.switch.enabledTitle": "Включён — нажмите, чтобы отключить",
  "mcp.switch.disabledTitle": "Отключён — нажмите, чтобы включить",
  "mcp.switch.disableAria": "Отключить MCP-сервер {name}",
  "mcp.switch.enableAria": "Включить MCP-сервер {name}",
  "mcp.edit.title": "Изменить запись ({origin})",
  "mcp.edit.readonlyTitle":
    "Определено в config.yaml — изменяйте в разделах конфигурации",
  "mcp.edit.aria": "Изменить {name}",
  "mcp.delete.title": "Удалить из {origin}",
  "mcp.delete.readonlyTitle": "Определено в config.yaml — здесь удалить нельзя",
  "mcp.delete.aria": "Удалить {name}",
  "mcp.note.denied":
    "Проектные MCP-серверы выключены параметром mcp.project_trust: deny. Эта запись никогда не запускается.",
  "mcp.fact.in": "в",
  "mcp.tools.emptyConnected": "Этот сервер не объявляет инструментов.",
  "mcp.tools.notReachable": "Нет списка инструментов — сервер недоступен.",
  "mcp.toolSwitch.serverDisabled": "Сервер отключён",
  "mcp.toolSwitch.disableAria": "Отключить инструмент {tool} сервера {server}",
  "mcp.toolSwitch.enableAria": "Включить инструмент {tool} сервера {server}",
  "mcp.editor.namePlaceholder": "имя-сервера",
  "mcp.editor.nameAria": "Имя сервера",
  "mcp.editor.scopeAria": "Область сервера",
  "mcp.editor.scopeLocal": "Локально — ./.coddy/mcp.json",
  "mcp.editor.scopeGlobal": "Глобально — ~/.coddy/mcp.json",
  "mcp.editor.jsonAria": "JSON записи сервера",
  "mcp.editor.save": "Сохранить",
  "mcp.editor.cancel": "Отмена",
  "mcp.discovery.description":
    "Проектный ./.coddy/mcp.json приходит вместе с чекаутом, поэтому команду, которую запустит сессия, выбирает репозиторий, а не вы. В режиме «Спрашивать» его серверы не запускаются и не опрашиваются, пока вы не одобрите именно это объявление для данного рабочего пространства (кнопка-щит в списке ниже); изменение одобренной записи снова потребует одобрения. Серверы, добавленные здесь, одобряются самим фактом записи. Записи из config.yaml и ~/.coddy/mcp.json — ваши и никогда не блокируются.",
  "mcp.servers.description":
    "Серверы Model Context Protocol из трёх уровней: config.yaml (mcp_servers) и глобальный ~/.coddy/mcp.json, объединённые с локальным ./.coddy/mcp.json проекта (формат Cursor; более поздние уровни переопределяют по имени). Можно отключить весь сервер или отдельные инструменты — переключатели сохраняются в файл, определяющий сервер, и применяются в работающих сессиях на следующем ходе.",
  "mcp.empty":
    "Серверы MCP не настроены. Добавьте сервер здесь (сохранится в локальный ./.coddy/mcp.json или глобальный ~/.coddy/mcp.json) либо объявите его в mcp_servers в config.yaml.",
  "mcp.note.declaredBy":
    "Объявлено в {path}; этот файл передаётся вместе с чекаутом, поэтому сервер пока не запускается и не опрашивается. Одобрение распространяется ровно на это объявление:",
  "mcp.note.namesOnly":
    "Перечисляются только имена переменных окружения и заголовков, но не их значения. Изменение записи потребует повторного одобрения.",
  "mcp.note.workspaceFallback": "рабочее пространство сессии",
  "mcp.editor.formatDescription":
    "Одна запись mcpServers в формате Cursor: command/args/env (объект), необязательные disabled и disabledTools. Сохраняется в {path}.",

  "mcp.trustOption.ask":
    "Спрашивать — одобрять каждый проектный сервер однократно",
  "mcp.trustOption.allow":
    "Разрешать — запускать проектные серверы автоматически",
  "mcp.trustOption.deny": "Запрещать — никогда не загружать проектные серверы",
  "mcp.fact.transport": "транспорт",
  "mcp.fact.runs": "запускает",
  "mcp.fact.contacts": "обращается",
  "mcp.fact.env": "env",
  "mcp.fact.headers": "заголовки",
  "mcp.origin.config": "config.yaml",
  "mcp.origin.home": "~/.coddy/mcp.json",
  "mcp.origin.project": "./.coddy/mcp.json",
  "mcp.validation.nameRequired": "Требуется имя сервера.",
  "mcp.validation.noDoubleUnderscore": "Имя сервера не должно содержать «__».",
  "mcp.validation.noSpacesOrSeparators":
    "Имя сервера не должно содержать пробелов или разделителей пути.",
  "mcp.parse.invalidJson": "Некорректный JSON.",
  "mcp.parse.mustBeObject": "Запись должна быть JSON-объектом.",
  "mcp.parse.typeString": "«type» должен быть строкой.",
  "mcp.parse.commandString": "«command» должен быть строкой.",
  "mcp.parse.urlString": "«url» должен быть строкой.",
  "mcp.parse.commandOrUrlRequired": "Требуется либо «command», либо «url».",
  "mcp.parse.argsStringArray": "«args» должен быть массивом строк.",
  "mcp.parse.envStringMap":
    "«env» должен быть объектом со строковыми значениями.",
  "mcp.parse.headersStringMap":
    "«headers» должен быть объектом со строковыми значениями.",
  "mcp.parse.disabledBoolean": "«disabled» должен быть логическим значением.",
  "mcp.parse.disabledToolsStringArray":
    "«disabledTools» должен быть массивом строк.",

  "skills.state.enabled": "Включён",
  "skills.state.disabled": "Отключён",
  "skills.badge.remote": "удалённый",
  "skills.loading": "Загрузка:",
  "skills.installed.legend": "Установленные навыки",
  "skills.autoDiscovery.legend": "Автообнаружение навыков",
  "skills.autoDiscovery.aria": "Автообнаружение навыков",
  "skills.autoDiscovery.fallbackDesc":
    "Позволить агенту самостоятельно загружать полные инструкции подходящего навыка (инструмент load_skill под управлением модели), а не только по команде /name.",
  "skills.install.searchPlaceholder": "Поиск навыков в маркете для установки:",
  "skills.install.loadingMarketplaces": "Загрузка маркетов:",
  "skills.install.noMatches": "Подходящие навыки в маркете не найдены.",
  "skills.install.moreHint": "+{count} ещё — уточните запрос",
  "skills.install.installTitle": "Установить {name}",
  "skills.install.installAria": "Установить {name}",
  "skills.error.toggle": "Не удалось {action}",
  "skills.error.delete": "Не удалось удалить",
  "skills.error.update": "Не удалось обновить",
  "skills.error.sync": "Не удалось синхронизировать",
  "skills.error.install": "Не удалось установить {name}",
  "skills.status.updated": "Обновлено: {name}.",
  "skills.status.installed": "Установлено: {name}.",
  "skills.sources.legend": "Удалённые источники навыков",
  "skills.sources.add": "Добавить",
  "skills.sources.syncAll": "Синхронизировать все",
  "skills.sources.syncAllTitle": "Получить все настроенные маркеты",
  "skills.sources.completed": "Готово",
  "skills.sources.syncedTitle": "Синхронизировано",
  "skills.sources.syncTitle": "Синхронизировать {source}",
  "skills.sources.syncAria": "Синхронизировать этот маркет",
  "skills.sources.removeTitle": "Удалить",
  "skills.sources.removeAria": "Удалить маркет",
  "skills.sources.description":
    "Репозитории GitHub (owner/repo[@ref]), git-ссылки или URL agents-standard marketplace.json. Сохраняется в skills.sources; запрашивается только при синхронизации.",
  "skills.sources.placeholder": "owner/repo  ·  https://…/marketplace.json",
  "skills.install.cliHint":
    "Навыки можно также установить через npx skills или npx skillsbd — они попадают в ~/.agents/skills/ и подхватываются автоматически.",
  "skills.empty":
    "Навыки не найдены. Используйте npx skills или npx skillsbd для установки.",
  "skills.switch.enabledTitle": "Включён - нажмите, чтобы отключить",
  "skills.switch.disabledTitle": "Отключён - нажмите, чтобы включить",
  "skills.switch.disableAria": "Отключить {name}",
  "skills.switch.enableAria": "Включить {name}",
  "skills.delete.title": "Удалить",
  "skills.delete.bundledTitle": "Встроенный навык - нельзя удалить",
  "skills.delete.aria": "Удалить {name}",
  "skills.update.title": "Скачать обновление: {name} v{from} → v{to}",
  "skills.update.aria": "Скачать обновление для {name} до версии {version}",
  "skills.badge.syncedFrom": "Синхронизировано из {source}",
  "app.chatBusy": "Этот чат занят в другом клиенте. Попробуйте снова через момент.",
  "app.emptyResponseBody": "Пустое тело ответа",
  "app.branchCreationNoSessionId": "Создание ветки не вернуло ID сессии",

  "nav.ariaLabel": "Навигация",
  "nav.brandTitle": "Coddy",
  "nav.brandSub": "агент",
  "nav.homeAriaLabel": "Главная Coddy agent",
  "nav.newChatTooltip": "Новый чат",
  "nav.useWideSidebar": "Широкая боковая панель",
  "nav.useNarrowSidebar": "Узкая боковая панель",
  "nav.wideSidebarTooltip": "Широкая панель",
  "nav.history": "История",
  "nav.scheduler": "Планировщик",
  "nav.schedulerAriaLabel": "Задачи планировщика",
  "nav.settings": "Настройки",

  "sessions.history": "История",
  "sessions.closeHistory": "Закрыть историю",
  "sessions.searchPlaceholder": "Поиск по заголовку или первому сообщению",
  "sessions.searchAriaLabel": "Поиск в истории по заголовку или первому сообщению",
  "sessions.clearSearch": "Очистить поиск",
  "sessions.empty": "История пуста",
  "sessions.permissionRequired": "Требуется разрешение",
  "sessions.questionPending": "Ожидается ответ",
  "sessions.unreadCompletion": "Непрочитанное завершение",
  "sessions.newChatFallback": "Новый чат",
  "sessions.deleteConversation": "Удалить диалог",
  "sessions.delete": "Удалить",
  "sessions.loadingMore": "Загрузка…",

  "chat.newChat": "Новый чат",
  "chat.chatTitleAriaLabel": "Заголовок чата",
  "chat.branchPrev": "Предыдущая ветка",
  "chat.branchNext": "Следующая ветка",
  "chat.branchLabel": "Ветка {current} из {total}",
  "chat.heroTitle": "Что вы хотите {verb}?",
  "chat.heroVerb.know": "узнать",
  "chat.heroVerb.build": "создать",
  "chat.heroVerb.find": "найти",
  "chat.heroVerb.research": "изучить",
  "chat.heroVerb.explore": "исследовать",
  "chat.heroVerb.debug": "отладить",
  "chat.heroVerb.ship": "выпустить",
  "chat.heroVerb.design": "спроектировать",
  "chat.heroVerb.learn": "выучить",
  "chat.heroVerb.automate": "автоматизировать",
  "chat.heroVerb.refactor": "рефакторить",
  "chat.heroVerb.plan": "спланировать",
  "chat.runPlanMessage": "Реализуй план.",
  "chat.contextTitle": "Контекст",
  "chat.contextClose": "Закрыть",
  "chat.contextCloseBreakdown": "Закрыть разбор контекста",
  "chat.contextEmpty": "Контекст пока не используется",
  "chat.contextPercentUsed": "Использовано {percent}%",
  "chat.contextTokensSummary": "~{used} / {max} токенов",
  "chat.contextMeterAriaLabel": "Использовано {percent}% контекста",
  "chat.contextSegmentTooltip": "{label}: {count}",
  "chat.contextSegment.systemPrompt": "Системный промпт",
  "chat.contextSegment.toolDefinitions": "Определения инструментов",
  "chat.contextSegment.rules": "Правила",
  "chat.contextSegment.skills": "Навыки",
  "chat.contextSegment.mcp": "MCP",
  "chat.contextSegment.subagents": "Субагенты",
  "chat.contextSegment.conversation": "Диалог",

  "composer.messageLabel": "Сообщение",
  "composer.placeholderEmpty": "План, сборка, / для навыков, @ для файлов",
  "composer.placeholderFollowUp": "Добавить уточнение",
  "composer.send": "Отправить",
  "composer.stopGeneration": "Остановить генерацию",
  "composer.enhance": "Улучшить промпт",
  "composer.enhanceNoModel": "Не удалось улучшить промпт: модель не настроена.",
  "composer.enhanceFailed": "Не удалось улучшить промпт. Черновик не изменён.",
  "composer.attachFile": "Прикрепить файл",
  "composer.attachUnsupportedModel": "Выбранная модель не принимает вложения",
  "composer.attachmentTooltip": "{fileName}\\n{label} · {size}",
  "composer.removeAttachment": "Удалить {fileName}",
  "composer.attachedFilesAriaLabel": "Прикреплённые файлы",
  "composer.bytesB": "{n} Б",
  "composer.bytesKB": "{n} КБ",
  "composer.bytesMB": "{n} МБ",
  "composer.mode": "Режим",
  "composer.modeAgent": "Агент",
  "composer.modePlan": "План",
  "composer.model": "Модель",
  "composer.modelTitle": "YAML-бэкенд (metadata.model)",
  "composer.reasoning": "Рассуждение",
  "composer.reasoningLevel": "Уровень рассуждения",
  "composer.reasoningLevelTitle": "Уровень рассуждения (metadata.reasoning)",
  "composer.contextUsage": "Использование контекста",
  "composer.contextTipIdle": "Контекст пока не используется",
  "composer.contextTipUsed": "Использовано {percent}% контекста",
  "composer.contextTipInput": "Ввод {count}",
  "composer.contextTipOutput": "Вывод {count}",
  "composer.contextTipTotal": "Всего {count}",
  "composer.contextTipMaxContext": "Макс. контекст {count}",
  "composer.composerOptions": "Параметры композера",
  "composer.skillsTitle": "Навыки",
  "composer.loading": "Загрузка…",
  "composer.more": "Ещё",
  "composer.noMatchingSkills": "Подходящих навыков нет",
  "composer.commandsTitle": "Команды",
  "composer.typeAfterAt": "Введите текст после @ для поиска",
  "composer.noFiles": "Нет файлов",
  "composer.filterModels": "Фильтр моделей",
  "composer.filterModelsPlaceholder": "Фильтр моделей…",
  "composer.noModelsMatch": "Нет моделей по запросу «{query}»",
  "composer.vendorOther": "Другие",
  "composer.closePicker": "Закрыть выбор",
  "composer.slashCommandsAriaLabel": "Команды со слэшем",
  "composer.workspaceFilesTitle": "Файлы проекта",
  "composer.workspaceFilesAriaLabel": "Файлы проекта",
  "composer.requestFailed": "ошибка запроса",
  "composer.env.ariaLabel": "Окружение",
  "composer.env.title": "Окружение (локальный или удалённый coddy http)",
  "composer.env.local": "Локальное",
  "composer.env.localThisOrigin": "Локальное (этот origin)",
  "composer.env.groupEnvironment": "Окружение",
  "composer.env.groupRemote": "Удалённые",
  "composer.env.addFormTitle": "Добавить удалённое",
  "composer.env.addRemote": "+ Добавить удалённое…",
  "composer.env.namePlaceholder": "название",
  "composer.env.tokenPlaceholder": "bearer-токен (пусто, если не нужен)",
  "composer.env.connect": "Подключить",
  "composer.env.cancel": "Отмена",
  "composer.folderModal.title": "Открыть папку",
  "composer.folderModal.close": "Закрыть обзор папок",
  "composer.folderModal.pathLabel": "Путь к папке",
  "composer.folderModal.pathPlaceholder": "Путь",
  "composer.folderModal.drivesPlaceholder": "Этот компьютер",
  "composer.folderModal.noSubfolders": "Вложенных папок нет",
  "composer.folderModal.noDrives": "Диски не найдены",
  "composer.folderModal.cannotList": "Не удалось прочитать {path}",
  "composer.folderModal.cancel": "Отмена",
  "composer.folderModal.open": "Открыть",
  "composer.folderModal.go": "Перейти",

  "env.banner.unreachable": "Удалённое окружение {name} недоступно или не авторизовано — проверьте, что оно запущено, что {cors} разрешает этот origin и что токен верный.",
  "env.banner.switchLocal": "Переключиться на локальное",

  "prompts.questions": "Вопросы",
  "prompts.questionsCount": "Вопросов: {count}",
  "prompts.noAnswer": "(нет ответа)",
  "prompts.answered": "Отвечено",
  "prompts.skipped": "Пропущено",
  "prompts.skip": "Пропустить",
  "prompts.continue": "Продолжить",
  "prompts.optionsAriaLabel": "Варианты {index}",
  "prompts.otherPlaceholder": "Другое…",
  "prompts.otherAriaLabel": "Другое, введите ответ",
  "prompts.allow": "Разрешить",
  "prompts.allowAlways": "Всегда разрешать",
  "prompts.allowAlwaysProgram": "Всегда разрешать {program}",
  "prompts.reject": "Отклонить",
  "prompts.planRun": "Запустить план",
  "prompts.planDiscard": "Отменить",
  "prompts.planTogglePreview": "Переключить предпросмотр",
  "prompts.planBodyAriaLabel": "Тело плана (markdown)",
  "prompts.planBodyPlaceholder": "Шаги и заметки плана…",
  "prompts.planSaving": "Сохранение…",
  "prompts.planPreviewEmpty": "Пока нечего показывать.",
  "prompts.planSaveFailed": "ошибка сохранения ({status})",
  "prompts.planSaveFailedNoStatus": "ошибка сохранения",

  "scheduler.title": "Планировщик",
  "scheduler.close": "Закрыть планировщик",
  "scheduler.searchPlaceholder": "Поиск по описанию или id задачи",
  "scheduler.searchAriaLabel": "Поиск задач планировщика по описанию или id",
  "scheduler.clearSearch": "Очистить поиск",
  "scheduler.empty": "Задач пока нет",
  "scheduler.loading": "Загрузка…",
  "scheduler.noDescription": "—",
  "scheduler.paused": "на паузе",
  "scheduler.addJob": "Добавить задачу",
  "scheduler.runJobNow": "Запустить сейчас",
  "scheduler.stopJob": "Остановить задачу",
  "scheduler.newJob": "Новая задача",
  "scheduler.jobTitle": "Задача {jobId}",
  "scheduler.editorNewAriaLabel": "Новая задача планировщика",
  "scheduler.editorEditAriaLabel": "Редактирование задачи",
  "scheduler.closeEditor": "Закрыть редактор",
  "scheduler.field.jobId": "job_id",
  "scheduler.field.jobIdHelp": "Имя файла — буквы, цифры, дефисы (пример: daily-report).",
  "scheduler.field.description": "description",
  "scheduler.field.schedule": "schedule (UTC, 5 полей)",
  "scheduler.field.schedulePlaceholder": "0 * * * *",
  "scheduler.field.cwd": "cwd (необязательно)",
  "scheduler.field.cwdHelp": "По умолчанию — рабочая директория агента для этого экземпляра.",
  "scheduler.field.mode": "mode",
  "scheduler.mode.agent": "agent",
  "scheduler.mode.plan": "plan",
  "scheduler.field.model": "model",
  "scheduler.field.body": "body (markdown)",
  "scheduler.bodyAriaLabel": "Тело задачи (markdown)",
  "scheduler.bodyPlaceholder": "Инструкция для запланированного запуска…",
  "scheduler.pause": "Пауза",
  "scheduler.resume": "Возобновить",
  "scheduler.delete": "Удалить",
  "scheduler.apiNotAvailable": "API планировщика недоступен в этой сборке (пересоберите с http,scheduler).",
  "scheduler.disabled": "Планировщик отключён (включите scheduler.enabled или передайте -scheduler-enabled).",
  "scheduler.validation.required": "Обязательное поле",
  "scheduler.validation.tooLong": "Слишком длинное",
  "scheduler.validation.noSpaces": "Без пробелов — используйте дефисы (пример: daily-report)",
  "scheduler.validation.invalidJobId": "Только буквы, цифры и дефисы (пример: daily-report)",

  "tasks.panelTitle": "Фоновые задачи",
  "tasks.closePanel": "Закрыть фоновые задачи",
  "tasks.loading": "Загрузка…",
  "tasks.empty": "В этом чате ещё нет фоновых задач. Агент запускает их, когда команда достаточно долгая, чтобы выполнять её отдельно.",
  "tasks.sectionRunning": "Выполняются",
  "tasks.sectionFinished": "Завершённых: {count}",
  "tasks.clearFinished": "Очистить",
  "tasks.backToList": "← К списку задач",
  "tasks.stopTitle": "Остановить задачу",
  "tasks.stopAriaLabel": "Остановить {label}",
  "tasks.outputHeading": "Вывод",
  "tasks.truncated": "усечён",
  "tasks.truncatedTitle": "Ранний вывод вытеснен из буфера в памяти; полный лог остаётся в бандле сессии",
  "tasks.noOutput": "(вывода пока нет)",
  "tasks.olderOnDisk": "Ещё задач на диске: {count} — они не показаны в списке",
  "tasks.progressAriaLabel": "Прогресс относительно оценки для {label}",
  "tasks.estimate": "оценка {value}",
  "tasks.exitCode": "код {code}",
  "tasks.overdue": "просрочена",
  "tasks.status.queued": "В очереди",
  "tasks.status.running": "Выполняется",
  "tasks.status.succeeded": "Успешно",
  "tasks.status.failed": "Ошибка",
  "tasks.status.timedOut": "Таймаут",
  "tasks.status.stopped": "Остановлена",
  "tasks.status.orphaned": "Осиротела",

  "messages.preparingResponse": "Готовлю ответ",
  "messages.copyCode": "Копировать код",
  "messages.copy": "Копировать",
  "messages.copied": "Скопировано",
  "messages.copyMessage": "Копировать сообщение",
  "messages.copyErrorMessage": "Копировать сообщение об ошибке",
  "messages.editMessage": "Редактировать сообщение",
  "messages.attachedFiles": "Прикреплённые файлы",
  "messages.systemLabel": "Система",
  "messages.refresh": "Обновить",
  "messages.retryLastMessage": "Повторить последнее сообщение",
  "messages.thinkingInProgress": "думаю…",
  "messages.thinkingCompleted": "размышление",
  "messages.thinkingSummaryAriaLabel": "Сводка размышления",
  "messages.thinkingContentAriaLabel": "Содержимое размышления",
  "messages.compactionLabel": "контекст сжат",
  "messages.compactionSummaryAriaLabel": "Сводка сжатого контекста",
  "messages.compactionBodyAriaLabel": "Содержимое сжатого контекста",
  "messages.memoryInProgress": "память…",
  "messages.memoryCompleted": "память",
  "messages.memoryInProgressAriaLabel": "Работа с памятью",
  "messages.memorySummaryAriaLabel": "Сводка копилот памяти",
  "messages.memoryContentAriaLabel": "Содержимое copilot памяти",
  "messages.memoryMarkedSaved": "Отмечено как сохранённое ({title}).",
  "messages.memoryMarkedSavedDefaultTitle": "заметка",
  "messages.memoryEmpty": "Подходящих заметок для этого хода не найдено.",
  "messages.toolDefaultName": "инструмент",
  "messages.toolQuestionLabel": "вопрос",
  "messages.toolPendingSuffix": "…",
  "messages.toolSummaryAriaLabel": "Сводка инструмента",
  "messages.toolDetailsAriaLabel": "Детали вызова инструмента",
  "messages.toolResultAriaLabel": "Результат инструмента",
  "messages.toolResultSection": "Результат",
  "messages.toolLoading": "Загрузка…",
  "messages.toolMore": "Ещё…",
  "messages.toolLess": "Свернуть",
  "messages.toolQuestionTimelineAriaLabel": "Хронология инструмента вопроса",
  "messages.toolAwaitingAnswer": "Ожидается ответ",
  "messages.toolQuestionMirrorHint": "Ответьте через карточку «Вопросы» в этом чате. Эта строка только отражает состояние инструмента.",
  "messages.toolBgTaskOpen": "Открыть в задачах",
  "messages.toolBgTaskStop": "Остановить",
  "messages.fileType.image": "Изображение",
  "messages.fileType.video": "Видео",
  "messages.fileType.audio": "Аудио",
  "messages.fileType.pdf": "PDF",
  "messages.fileType.text": "Текст",
  "messages.fileType.archive": "Архив",
  "messages.fileType.file": "Файл",
  "messages.requestFailed": "Запрос не выполнен",
  "messages.streamEnded": "Поток завершён",

  "app.backendUnavailable": "Бэкенд недоступен ({status})",

  "sessions.draftPrefix": "Черновик: {title}",
  "sessions.draftEmpty": "Черновик: Новый чат",

  "workspace.detached": "отсоединённая",
  "workspace.worktree": "рабочее дерево",
  "workspace.worktreeActiveTitle": "Эта сессия работает в отдельном рабочем дереве",
  "workspace.worktreeInactiveTitle": "Переход на другую ветку откроется в отдельном рабочем дереве",
  "workspace.recent": "Недавние",
  "workspace.openFolder": "Открыть папку…",
  "workspace.noBranches": "Веток нет",

  "permission.preview.patch": "Предпросмотр патча",
  "permission.preview.edit": "Предпросмотр изменения",
  "permission.preview.more": "Ещё…",
  "permission.preview.less": "Свернуть",
  "permission.question.runCommand": "Запустить эту команду?",
  "permission.question.writeFile": "Записать этот файл?",
  "permission.question.editFile": "Изменить этот файл?",
  "permission.question.applyPatch": "Применить этот патч?",
  "permission.question.createDirectory": "Создать эту папку?",
  "permission.question.createOrUpdateFile": "Создать или обновить этот файл?",
  "permission.question.movePath": "Переместить этот путь?",
  "permission.question.removeDirectoryTree": "Удалить это дерево папок?",
  "permission.question.removePath": "Удалить этот путь?",
  "permission.question.removeEmptyDirectory": "Удалить эту пустую папку?",
  "permission.question.allowAction": "Разрешить это действие?",
  "permission.header.shell": "Оболочка",
  "permission.header.sshShell": "Оболочка SSH",
  "permission.header.move": "Перемещение",
  "permission.header.workspace": "Рабочая область",
  "permission.meta.timeout": "тайм-аут {seconds} с",
  "permission.meta.replaceAll": "заменить все",
  "permission.meta.chars.one": "{count} символ",
  "permission.meta.chars.few": "{count} символа",
  "permission.meta.chars.many": "{count} символов",
  "permission.meta.chars.other": "{count} символа",
  "permission.meta.createParents": "создать родительские папки",
  "permission.meta.directParentOnly": "только непосредственный родительский каталог",
  "permission.meta.existingParentsOnly": "только существующие родительские папки",
  "permission.meta.recursive": "рекурсивно",
  "permission.meta.emptyDirectoryOnly": "только пустая папка",
  "permission.meta.fromLine": "со строки {line}",
  "permission.meta.lines.one": "{count} строка",
  "permission.meta.lines.few": "{count} строки",
  "permission.meta.lines.many": "{count} строк",
  "permission.meta.lines.other": "{count} строки",
  "permission.meta.hiddenFiles": "скрытые файлы",
  "permission.meta.caseSensitive": "с учётом регистра",
  "permission.meta.maxResults": "макс. {count}",
  "permission.meta.depth": "глубина {depth}",

  "scheduler.cron.required": "Введите cron-выражение (5 полей, UTC).",
  "scheduler.cron.invalid": "Некорректное cron-выражение",

  "tasks.notReachable": "Coddy недоступен",
  "tasks.chip.running.one": "Выполняется {count} задача",
  "tasks.chip.running.few": "Выполняются {count} задачи",
  "tasks.chip.running.many": "Выполняется {count} задач",
  "tasks.chip.running.other": "Выполняется {count} задачи",
  "tasks.chip.total.one": "{count} фоновая задача",
  "tasks.chip.total.few": "{count} фоновые задачи",
  "tasks.chip.total.many": "{count} фоновых задач",
  "tasks.chip.total.other": "{count} фоновой задачи",
  "tasks.chip.openAria": "Открыть фоновые задачи: {label}",

  "env.error.remoteUnreachable": "Не удаётся подключиться к удалённому {host} — возможно, он выключен, URL неверный или ответ заблокирован CORS (включите httpserver.cors на удалённом сервере).",
  "env.error.localNetwork": "Ошибка сети при отправке сообщения — проверьте, что сервер запущен.",
  "env.error.remoteUnauthorized": "Нет авторизации на удалённом {host} — проверьте bearer-токен для этого окружения.",
  "env.error.localUnauthorized": "Нет авторизации ({status}).",
  "env.error.remoteRequestFailed": "Запрос к удалённому {host} не выполнен ({status}).",
  "env.error.requestFailed": "Запрос не выполнен ({status}).",

  "status.read": "Читаю",
  "status.list": "Смотрю каталог",
  "status.search": "Ищу",
  "status.edit": "Правлю",
  "status.write": "Пишу",
  "status.run": "Выполняю",
  "status.runRemote": "Выполняю по SSH",
  "status.createDir": "Создаю каталог",
  "status.touch": "Создаю файл",
  "status.move": "Перемещаю",
  "status.delete": "Удаляю",
  "status.webSearch": "Ищу в интернете",
  "status.webFetch": "Загружаю страницу",
  "status.plan": "Обновляю план",
  "status.planRead": "Читаю план",
  "status.skill": "Загружаю навык",
  "status.schedule": "Обновляю расписание",
  "status.config": "Обновляю конфигурацию",
  "status.backgroundWait": "Жду фоновую задачу",
  "status.backgroundList": "Проверяю фоновые задачи",
  "status.backgroundOutput": "Читаю вывод фоновой задачи",
  "status.backgroundStop": "Останавливаю фоновую задачу",
  "status.backgroundReap": "Убираю фоновые задачи",
  "status.tool": "Работаю с инструментом",
  "status.thinking": "Думаю…",
  "status.memory": "Работаю с памятью",
  "status.awaitingPermission": "Жду разрешения",
  "status.awaitingAnswer": "Жду ответа",
  "status.waitingModel": "Жду ответ модели",
  "status.waitingSlow": "Модель отвечает дольше обычного",
  "status.waitingStuck": "Ответа от сервера всё ещё нет",
};
