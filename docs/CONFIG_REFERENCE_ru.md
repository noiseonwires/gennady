# Справочник по конфигурации (RU)

Этот файл сформирован автоматически. Не редактируйте его вручную.

## Общие

| Ключ YAML | ENV | Тип | Описание |
|---|---|---|---|
| `bot_token` | `BOT_TOKEN` | string 🔒 | Токен бота - Токен Telegram Bot API, полученный от @BotFather |
| `proxy_url` | `PROXY_URL` | string | URL прокси - URL HTTP/SOCKS5 прокси (опционально) |
| `language` | `LANGUAGE` | string | Язык - Язык интерфейса бота: ru или en |

## База данных

| Ключ YAML | ENV | Тип | Описание |
|---|---|---|---|
| `database.provider` | `DATABASE_PROVIDER` | string | Провайдер базы данных - Значение `local` или `remote` (по умолчанию: `local`). Оставьте пустым для автоопределения: будет выбран `remote`, если заданы `url` и `auth_token`, иначе `local` |
| `database.path` | `DATABASE_PATH` | string | Путь к базе данных - Путь к файлу локальной базы данных SQLite |
| `database.url` | `DATABASE_URL` | string | URL подключения - URL подключения к удалённой базе данных (для провайдера remote) |
| `database.auth_token` | `DATABASE_AUTH_TOKEN` | string 🔒 | Токен авторизации - Токен аутентификации для удалённых провайдеров базы данных |

## Реакции

| Ключ YAML | ENV | Тип | Описание |
|---|---|---|---|
| `reactions.suspicious_message` | `REACTIONS_SUSPICIOUS_MESSAGE` | string | Подозрительное сообщение - Эмодзи-реакция на подозрительные сообщения (по умолч.: 🤔) |
| `reactions.bad_message` | `REACTIONS_BAD_MESSAGE` | string | Плохое сообщение - Эмодзи-реакция на сообщения, подтверждённые как плохие (по умолч.: 🍌) |
| `reactions.content_filter` | `REACTIONS_CONTENT_FILTER` | string | Контент-фильтр - Эмодзи-реакция при срабатывании контент-фильтра (по умолч.: 🥴) |
| `reactions.creative_reply_limit` | `REACTIONS_CREATIVE_REPLY_LIMIT` | string | Лимит креативных ответов - Эмодзи-реакция при достижении лимита креативных ответов (по умолч.: 🥱) |
| `reactions.extracting_link` | `REACTIONS_EXTRACTING_LINK` | string | Извлечение ссылки - Эмодзи-реакция во время извлечения содержимого ссылки (по умолч.: ✍) |
| `reactions.extract_link_failed` | `REACTIONS_EXTRACT_LINK_FAILED` | string | Ошибка извлечения ссылки - Эмодзи-реакция при ошибке извлечения ссылки (по умолч.: 🌚) |
| `reactions.user_muted` | `REACTIONS_USER_MUTED` | string | Пользователь в муте - Эмодзи-реакция при выдаче мута пользователю (по умолчанию: 🤮) |
| `reactions.report_acknowledged` | `REACTIONS_REPORT_ACKNOWLEDGED` | string | Жалоба принята - Эмодзи-реакция при принятии жалобы (по умолч.: 👌) |
| `reactions.creative_reply_error` | `REACTIONS_CREATIVE_REPLY_ERROR` | string | Ошибка креативного ответа - Эмодзи-реакция при ошибке генерации креативного ответа (по умолч.: 😐) |

## Администрирование

| Ключ YAML | ENV | Тип | Описание |
|---|---|---|---|
| `admin.chat_id` | `ADMIN_CHAT_ID` | int64 | ID чата администратора - ID Telegram-чата для уведомлений администратора |
| `admin.reply_message_ids` | `ADMIN_REPLY_MESSAGE_IDS` | []int | ID ответных сообщений - ID сообщений в чате администратора для ответа (для чатов с темами) |
| `admin.super_admin_user_id` | `ADMIN_SUPER_ADMIN_USER_ID` | int64 | ID суперадминистратора - Telegram ID суперадминистратора для прямых команд |
| `admin.notify_super_admin` | `ADMIN_NOTIFY_SUPER_ADMIN` | bool | Уведомлять суперадмина - Дублировать уведомления модерации (с клавиатурой действий) в личные сообщения суперадмину |
| `admin.notify_startup` | `ADMIN_NOTIFY_STARTUP` | bool | Уведомлять о запуске - Присылать суперадмину ЛС при запуске/перезапуске бота, а также при потере и восстановлении соединения с удалённой БД |
| `admin.whitelist_user_ids` | `ADMIN_WHITELIST_USER_IDS` | []int64 | ID пользователей в белом списке - ID пользователей, которые обходят контент-фильтры и имеют права администратора |

## Модерация

| Ключ YAML | ENV | Тип | Описание |
|---|---|---|---|
| `moderation.chat_id` | `MODERATION_CHAT_ID` | []int64 | ID модерируемых чатов - ID Telegram-чатов для модерации (один или массив) |
| `moderation.excluded_topics` | `MODERATION_EXCLUDED_TOPICS` | []chat_topic | Исключённые темы - Пары (чат, тема), исключённые из AI-анализа контента. Используйте «Любая тема» для подстановки, «Только основная» для основной области чата или конкретный ID темы форума. |
| `moderation.mute_across_all_chats` | `MODERATION_MUTE_ACROSS_ALL_CHATS` | bool | Мут во всех чатах - При выдаче мута пользователю также применять его во всех остальных модерируемых чатах |

## Удаление сообщений

| Ключ YAML | ENV | Тип | Описание |
|---|---|---|---|
| `message_deletion.enabled` | `MESSAGE_DELETION_ENABLED` | bool | Включено - Включить автоматическое удаление сообщений |
| `message_deletion.included_topics` | `MESSAGE_DELETION_INCLUDED_TOPICS` | []chat_topic | Включённые темы - Пары (чат, тема), где сообщения подлежат авто-удалению. Пусто = каждый чат модерации / любая тема. Используйте «Только основная», чтобы ограничить основной областью. |
| `message_deletion.excluded_topics` | `MESSAGE_DELETION_EXCLUDED_TOPICS` | []chat_topic | Исключённые темы - Пары (чат, тема), переопределяющие «Включённые темы» - сообщения здесь никогда не удаляются автоматически. |
| `message_deletion.excluded_user_ids` | `MESSAGE_DELETION_EXCLUDED_USER_IDS` | []int64 | Исключённые ID пользователей - ID пользователей, сообщения которых никогда не удаляются |
| `message_deletion.chat_deletion_retention_hours` | `MESSAGE_DELETION_CHAT_DELETION_RETENTION_HOURS` | int | Часы хранения - Удалять сообщения старше этого количества часов (по умолч.: 3) |
| `message_deletion.cleanup_interval_hours` | `MESSAGE_DELETION_CLEANUP_INTERVAL_HOURS` | int | Интервал очистки (часы) - Как часто запускать очистку удаления (по умолч.: 3) |

## Очистка базы данных

| Ключ YAML | ENV | Тип | Описание |
|---|---|---|---|
| `database_cleanup.cleanup_interval_hours` | `DATABASE_CLEANUP_CLEANUP_INTERVAL_HOURS` | int | Интервал очистки (часы) - Как часто запускать очистку базы данных в часах (0 = выключено, по умолч.: 24) |
| `database_cleanup.message_retention_hours` | `DATABASE_CLEANUP_MESSAGE_RETENTION_HOURS` | int | Хранение сообщений (часы) - Хранить записи сообщений столько часов (по умолч.: 168 = 7 дней) |
| `database_cleanup.warning_retention_hours` | `DATABASE_CLEANUP_WARNING_RETENTION_HOURS` | int | Хранение предупреждений (часы) - Хранить записи предупреждений столько часов (по умолч.: 168) |
| `database_cleanup.action_retention_hours` | `DATABASE_CLEANUP_ACTION_RETENTION_HOURS` | int | Хранение действий (часы) - Хранить записи действий столько часов (по умолч.: 168) |
| `database_cleanup.preserve_warned_muted_messages` | `DATABASE_CLEANUP_PRESERVE_WARNED_MUTED_MESSAGES` | bool | Сохранять сообщения с предупреждением или мутом - Не удалять сообщения, из-за которых было выдано предупреждение или активный мут, пока мут не снят и не истёк (по умолчанию: выкл.) |

## Запланированные события

| Ключ YAML | ENV | Тип | Описание |
|---|---|---|---|
| `scheduled_events.missed_event_max_delay_minutes` | `SCHEDULED_EVENTS_MISSED_EVENT_MAX_DELAY_MINUTES` | int | Макс. задержка пропущенного события - Максимальная задержка в минутах для запуска пропущенного события после перезапуска бота (по умолч.: 120) |
| `scheduled_events.webhook_mode` | `SCHEDULED_EVENTS_WEBHOOK_MODE` | bool | Режим вебхука - Если включено, запланированные события запускаются только по вебхуку (не автоматически) |
| `scheduled_events.webhook_path` | `SCHEDULED_EVENTS_WEBHOOK_PATH` | string | Путь вебхука - URL-путь для триггера запланированных событий через вебхук (по умолч.: /trigger-events) |
| `scheduled_events.lock_timeout_minutes` | `SCHEDULED_EVENTS_LOCK_TIMEOUT_MINUTES` | int | Таймаут блокировки - Минуты до истечения устаревшей блокировки события, после чего она может быть перехвачена (по умолч.: 15) |

## Отладка

| Ключ YAML | ENV | Тип | Описание |
|---|---|---|---|
| `debug.debug_telegram` | `DEBUG_DEBUG_TELEGRAM` | bool | Отладка Telegram - Логировать подробную информацию о взаимодействии с Telegram (обновления, команды, коллбэки) |
| `debug.debug_external_apis` | `DEBUG_DEBUG_EXTERNAL_APIS` | bool | Отладка внешних API - Логировать все запросы и ответы внешних API |
| `debug.debug_api_errors` | `DEBUG_DEBUG_API_ERRORS` | bool | Отладка ошибок API - Логировать только неуспешные API-запросы с кодами ошибок и обрезанным телом ответа |
| `debug.trace_topics` | `DEBUG_TRACE_TOPICS` | bool | Трассировка топиков - TRACE-логирование полей форум-топиков (message_thread_id, reply_to) во входящих обновлениях и исходящих сообщениях; используйте после деплоя для проверки обработки топиков |
| `debug.dump_moderation_messages` | `DEBUG_DUMP_MODERATION_MESSAGES` | bool | Дамп модерируемых сообщений - Сохранять все модерируемые сообщения в файлы |
| `debug.dump_admin_messages` | `DEBUG_DUMP_ADMIN_MESSAGES` | bool | Дамп административных сообщений - Сохранять все административные сообщения в файлы |
| `debug.message_dump_path` | `DEBUG_MESSAGE_DUMP_PATH` | string | Путь дампа сообщений - Путь к директории для файлов дампа сообщений (по умолч.: ./logs) |
| `debug.send_to_super_admin` | `DEBUG_SEND_TO_SUPER_ADMIN` | bool | Отправлять отладку суперадмину - Отправлять включённые отладочные логи (ошибки API, дамп чатов и т.д.) суперадмину в Telegram |

## Сервер

| Ключ YAML | ENV | Тип | Описание |
|---|---|---|---|
| `server.listen_addr` | `SERVER_LISTEN_ADDR` | string | Адрес прослушивания - Адрес для прослушивания HTTP-сервера (по умолч.: 0.0.0.0) |
| `server.listen_port` | `SERVER_LISTEN_PORT` | int | Порт прослушивания - Порт для прослушивания HTTP-сервера (по умолч.: 8080) |
| `server.certificate_path` | `SERVER_CERTIFICATE_PATH` | string | Путь к сертификату - Путь к файлу TLS-сертификата для самоподписанных сертификатов |

## Вебхуки

| Ключ YAML | ENV | Тип | Описание |
|---|---|---|---|
| `webhook.enabled` | `WEBHOOK_ENABLED` | bool | Включено - Использовать режим вебхуков вместо длинного опроса |
| `webhook.debug` | `WEBHOOK_DEBUG` | bool | Отладка - Включить отладочное логирование вебхуков |
| `webhook.secret_token` | `WEBHOOK_SECRET_TOKEN` | string 🔒 | Секретный токен - Секретный токен для валидации вебхуков |
| `webhook.url` | `WEBHOOK_URL` | string | URL - Публичный HTTPS URL вебхука |

## Обработка обновлений

| Ключ YAML | ENV | Тип | Описание |
|---|---|---|---|
| `update_processing.workers` | `UPDATE_PROCESSING_WORKERS` | int | Воркеры - Число параллельных воркеров обработки обновлений (только режим длинного опроса). По умолчанию 1 = обработка по порядку; увеличьте, чтобы запускать несколько конвейеров модерации одновременно, когда обновления накапливаются |
| `update_processing.stats_interval_seconds` | `UPDATE_PROCESSING_STATS_INTERVAL_SECONDS` | int | Интервал статистики, сек - Как часто логировать загрузку пула воркеров, чтобы решить, менять ли число воркеров (по умолчанию 600; отрицательное значение отключает) |

## Веб-интерфейс

| Ключ YAML | ENV | Тип | Описание |
|---|---|---|---|
| `web_ui.enabled` | `WEB_UI_ENABLED` | bool | Включено - Включить веб-панель администрирования |
| `web_ui.path_prefix` | `WEB_UI_PATH_PREFIX` | string | Префикс пути - Префикс URL-пути для веб-интерфейса (по умолч.: /admin) |
| `web_ui.password` | `WEB_UI_PASSWORD` | string 🔒 | Пароль - Пароль для аутентификации в веб-интерфейсе. В YAML можно хранить открытый пароль; конфиг в БД сохраняет его как hashed:pbkdf2-sha256:... |
| `web_ui.otp_enabled` | `WEB_UI_OTP_ENABLED` | bool | OTP включён - Включить одноразовый пароль (TOTP) для входа в веб-интерфейс (по умолчанию: true) |
| `web_ui.moderator_path_prefix` | `WEB_UI_MODERATOR_PATH_PREFIX` | string | Префикс пути модератора - Префикс URL-пути для изолированного ограниченного веб-интерфейса модератора (по умолч.: /mod; должен отличаться от path_prefix) |
| `web_ui.public_url` | `WEB_UI_PUBLIC_URL` | string | Публичный URL - Внешне доступный базовый URL без префикса пути (напр. https://bot.example.com) для ссылок входа модератора; при пустом значении берётся хост из URL вебхука |

## ИИ (общее)

| Ключ YAML | ENV | Тип | Описание |
|---|---|---|---|
| `ai.enabled` | `AI_ENABLED` | bool | ИИ включён - Включить функции на основе ИИ (модерация контента, сводки и т.д.) |
| `ai.chat_rules` | `AI_CHAT_RULES` | string | Правила чата - Текст правил чата, используемый в промптах ИИ для модерации контента |
| `ai.warning_mute` | `AI_WARNING_MUTE` | string | Текст мута в промпте - Текст о муте, который подставляется в промпт предупреждения через плейсхолдер {{mute_info}} |
| `ai.track_reactions` | `AI_TRACK_REACTIONS` | bool | Отслеживать реакции - Сохранять реакции-эмодзи по сообщениям и подмешивать их в AI-контекст для сводок, креативных ответов и профилей. Агрегированные счётчики работают в любом чате; пособытийные требуют прав админа |
| `ai.translation_prompt.system` | `AI_TRANSLATION_PROMPT_SYSTEM` | string | Перевод (системный) - Системный промпт для общего перевода (ссылки, события Википедии) (плейсхолдеры: `{{text}}`) |
| `ai.translation_prompt.user` | `AI_TRANSLATION_PROMPT_USER` | string | Перевод (пользовательский) - Пользовательский промпт для общего перевода (ссылки, события Википедии) (плейсхолдеры: `{{text}}`) |

## ИИ - модерация контента

| Ключ YAML | ENV | Тип | Описание |
|---|---|---|---|
| `ai.content_moderation.enabled` | `AI_CONTENT_MODERATION_ENABLED` | bool | Включено - Включить модерацию контента ИИ |
| `ai.content_moderation.skip_admin_users` | `AI_CONTENT_MODERATION_SKIP_ADMIN_USERS` | bool | Пропускать администраторов - Пропускать модерацию контента для администраторов |
| `ai.content_moderation.complaint_manual_moderation` | `AI_CONTENT_MODERATION_COMPLAINT_MANUAL_MODERATION` | bool | Ручная модерация по жалобе - Когда пользователь жалуется на сообщение (ответ + упоминание @бота), бот сначала перепроверяет его модерацией ИИ по всем настроенным моделям и действует автоматически, если хоть одна нашла нарушение. Если все модели сочли сообщение чистым: ВКЛ (по умолчанию) отправляет карточку решения администраторам для ручной проверки; ВЫКЛ - жалоба завершается без уведомления. |
| `ai.content_moderation.default_mute_minutes` | `AI_CONTENT_MODERATION_DEFAULT_MUTE_MINUTES` | int | Длительность авто-мута (мин) - Длительность действия `mute` (по умолчанию: 60, `0` = навсегда) |
| `ai.content_moderation.vision_enabled` | `AI_CONTENT_MODERATION_VISION_ENABLED` | bool | Vision включено - Включить Azure Vision API для анализа изображений |
| `ai.content_moderation.vision_endpoint` | `AI_CONTENT_MODERATION_VISION_ENDPOINT` | string | Vision эндпоинт - URL эндпоинта Azure Vision API |
| `ai.content_moderation.vision_api_key` | `AI_CONTENT_MODERATION_VISION_API_KEY` | string 🔒 | Vision API-ключ - API-ключ для Azure Vision |
| `ai.content_moderation.content_safety_enabled` | `AI_CONTENT_MODERATION_CONTENT_SAFETY_ENABLED` | bool | Content Safety включено - Включить Azure Content Safety API |
| `ai.content_moderation.content_safety_endpoint` | `AI_CONTENT_MODERATION_CONTENT_SAFETY_ENDPOINT` | string | Content Safety эндпоинт - URL эндпоинта Azure Content Safety API |
| `ai.content_moderation.content_safety_api_key` | `AI_CONTENT_MODERATION_CONTENT_SAFETY_API_KEY` | string 🔒 | Content Safety API-ключ - API-ключ для Azure Content Safety |
| `ai.content_moderation.new_user_profile_check_enabled` | `AI_CONTENT_MODERATION_NEW_USER_PROFILE_CHECK_ENABLED` | bool | Проверка профиля нового участника - При первом сообщении нового пользователя анализировать весь его публичный профиль (имя, био, фото профиля и привязанный личный канал: название/описание/фото) через AI и анализ изображений (Content Safety → Vision → OCR.space), добавляя отметку в профиль при срабатывании. Работает без Content Safety. |
| `ai.content_moderation.new_user_profile_use_full_model` | `AI_CONTENT_MODERATION_NEW_USER_PROFILE_USE_FULL_MODEL` | bool | Проверка профиля: полная модель - Оценивать собранный текст профиля полной моделью вместо лёгкой. Лучше распознаёт неочевидные признаки спама/скама/рекламы, но каждый вызов дороже. Влияет только на текстовый вердикт AI; проверка фото не меняется. По умолчанию: выкл (лёгкая модель). |
| `ai.content_moderation.new_user_profile_prompt.system` | `AI_CONTENT_MODERATION_NEW_USER_PROFILE_PROMPT_SYSTEM` | string | Проверка профиля нового участника (системный) - Системный промпт для анализа имени, био, фото и личного канала нового участника |
| `ai.content_moderation.new_user_profile_prompt.user` | `AI_CONTENT_MODERATION_NEW_USER_PROFILE_PROMPT_USER` | string | Проверка профиля нового участника (пользовательский) - Пользовательский промпт для анализа профиля нового участника (плейсхолдеры: `{{profile_text}}`) |
| `ai.content_moderation.new_user_window_hours` | `AI_CONTENT_MODERATION_NEW_USER_WINDOW_HOURS` | int | Окно нового пользователя (часы) - Сколько часов после первого замеченного сообщения пользователь считается «новым». Для новых пользователей добавляется отметка в контекст модерации и подставляется плейсхолдер {{new_user_rules}} (по умолчанию: 24). |
| `ai.content_moderation.new_user_rules` | `AI_CONTENT_MODERATION_NEW_USER_RULES` | string | Правила для новых пользователей - Дополнительный текст правил, подставляемый в плейсхолдер {{new_user_rules}} промпта модерации только для сообщений новых пользователей (см. Окно нового пользователя). Пусто - плейсхолдер раскрывается в пустую строку. |
| `ai.content_moderation.full_model_first_messages` | `AI_CONTENT_MODERATION_FULL_MODEL_FIRST_MESSAGES` | int | Полная модель для первых сообщений - Перепроверять первые N сообщений пользователя полной моделью, даже если лёгкая модель ничего не нашла, чтобы поймать неочевидный спам новых участников. Считается отдельно по каждому пользователю и чату. 0 - отключено. |
| `ai.content_moderation.reply_context_max_chars` | `AI_CONTENT_MODERATION_REPLY_CONTEXT_MAX_CHARS` | int | Макс. длина контекста ответа - Максимальная длина (в символах) цитируемого текста «в ответ на», подставляемого в плейсхолдер {{reply_to}} промпта модерации. Более длинные цитаты обрезаются с многоточием (без разрыва символа). 0 - без ограничения (по умолчанию: 500). |
| `ai.content_moderation.ocrspace_enabled` | `AI_CONTENT_MODERATION_OCRSPACE_ENABLED` | bool | OCR.space включён - Включить облачный API OCR.space (https://ocr.space/ocrapi) для извлечения текста из изображений |
| `ai.content_moderation.ocrspace_api_key` | `AI_CONTENT_MODERATION_OCRSPACE_API_KEY` | string 🔒 | Ключ API OCR.space - Ключ API OCR.space (есть бесплатный тариф; тестовый ключ: helloworld) |
| `ai.content_moderation.ocrspace_url` | `AI_CONTENT_MODERATION_OCRSPACE_URL` | string | URL OCR.space - Адрес OCR.space, по умолчанию https://api.ocr.space/parse/image |
| `ai.content_moderation.ocrspace_language` | `AI_CONTENT_MODERATION_OCRSPACE_LANGUAGE` | string | Язык OCR.space - 3-буквенный код языка (напр. eng, rus, cze); движки 2/3 также принимают 'auto' |
| `ai.content_moderation.ocrspace_engine` | `AI_CONTENT_MODERATION_OCRSPACE_ENGINE` | int | Движок OCR.space - Движок OCR: 1 (по умолчанию), 2 (универсальный, мемы/шумный фон), 3 (макс. точность, рукописный текст, 200+ языков) |
| `ai.content_moderation.prompt.system` | `AI_CONTENT_MODERATION_PROMPT_SYSTEM` | string | Модерация контента (системный) - Системный промпт для модерации контента (плейсхолдеры: `{{message}}`, `{{chat_rules}}`, `{{user_profile}}`, `{{user_reputation}}`, `{{reply_to}}`, `{{new_user_rules}}`) |
| `ai.content_moderation.prompt.user` | `AI_CONTENT_MODERATION_PROMPT_USER` | string | Модерация контента (пользовательский) - Пользовательский промпт для модерации контента (плейсхолдеры: `{{message}}`, `{{chat_rules}}`, `{{user_profile}}`, `{{user_reputation}}`, `{{reply_to}}`, `{{new_user_rules}}`) |
| `ai.content_moderation.warning_prompt.system` | `AI_CONTENT_MODERATION_WARNING_PROMPT_SYSTEM` | string | Предупреждение (системный) - Системный промпт для генерации предупреждения (плейсхолдеры: `{{username}}`, `{{user_message}}`, `{{chat_rules}}`, `{{mute_info}}`, `{{reputation}}`) |
| `ai.content_moderation.warning_prompt.user` | `AI_CONTENT_MODERATION_WARNING_PROMPT_USER` | string | Предупреждение (пользовательский) - Пользовательский промпт для генерации предупреждения (плейсхолдеры: `{{username}}`, `{{user_message}}`, `{{chat_rules}}`, `{{mute_info}}`, `{{reputation}}`) |

## ИИ - креативные ответы

| Ключ YAML | ENV | Тип | Описание |
|---|---|---|---|
| `ai.creative_replies.enabled` | `AI_CREATIVE_REPLIES_ENABLED` | bool | Включено - Включить креативные ответы ИИ на сообщения пользователей |
| `ai.creative_replies.use_full_model` | `AI_CREATIVE_REPLIES_USE_FULL_MODEL` | bool | Использовать полную модель - Использовать полную модель вместо лёгкой для креативных ответов |
| `ai.creative_replies.max_messages` | `AI_CREATIVE_REPLIES_MAX_MESSAGES` | int | Макс. сообщений - Макс. креативных ответов за временное окно (по умолч.: 3) |
| `ai.creative_replies.time_window` | `AI_CREATIVE_REPLIES_TIME_WINDOW` | int | Временное окно (часы) - Временное окно в часах для ограничения частоты (по умолч.: 3) |
| `ai.creative_replies.included_topics` | `AI_CREATIVE_REPLIES_INCLUDED_TOPICS` | []chat_topic | Включённые темы - Пары (чат, тема), где разрешены креативные ответы. Пусто = каждый чат модерации / любая тема. Используйте «Любая тема», чтобы включить весь чат, или «Только основная» для основной области. |
| `ai.creative_replies.excluded_topics` | `AI_CREATIVE_REPLIES_EXCLUDED_TOPICS` | []chat_topic | Исключённые темы - Пары (чат, тема), где креативные ответы подавляются, даже если в остальном они включены. |
| `ai.creative_replies.follow_up_only_same_user` | `AI_CREATIVE_REPLIES_FOLLOW_UP_ONLY_SAME_USER` | bool | Продолжение только тому же пользователю - Отвечать креативно только тому же пользователю в продолжении |
| `ai.creative_replies.reply_chain_depth` | `AI_CREATIVE_REPLIES_REPLY_CHAIN_DEPTH` | int | Глубина цепочки ответов - Макс. сообщений из цепочки ответов для контекста истории диалога (по умолч.: 5) |
| `ai.creative_replies.reply_chain_max_age_hours` | `AI_CREATIVE_REPLIES_REPLY_CHAIN_MAX_AGE_HOURS` | int | Макс. возраст цепочки (часы) - Прекращать обход цепочки ответов при попадании на сообщение старше указанного числа часов (по умолч.: 6) |
| `ai.creative_replies.reply_chain_adjacent_window` | `AI_CREATIVE_REPLIES_REPLY_CHAIN_ADJACENT_WINDOW` | int | Окно соседних сообщений - Добавлять в контекст другие сообщения участников цепочки, чьи ID сообщений попадают в это количество слотов вокруг сообщений цепочки (0 - отключено). Возраст ограничен параметром reply_chain_max_age_hours. |
| `ai.creative_replies.prompt.system` | `AI_CREATIVE_REPLIES_PROMPT_SYSTEM` | string | Креативный ответ (системный) - Системный промпт для генерации креативного ответа (плейсхолдеры: `{{message}}`, `{{context}}`, `{{quote}}`) |
| `ai.creative_replies.prompt.user` | `AI_CREATIVE_REPLIES_PROMPT_USER` | string | Креативный ответ (пользовательский) - Пользовательский промпт для генерации креативного ответа (плейсхолдеры: `{{message}}`, `{{context}}`, `{{quote}}`) |

## ИИ - утреннее приветствие

| Ключ YAML | ENV | Тип | Описание |
|---|---|---|---|
| `ai.morning_greeting.enabled` | `AI_MORNING_GREETING_ENABLED` | bool | Включено - Включить ежедневное утреннее приветствие |
| `ai.morning_greeting.use_ai` | `AI_MORNING_GREETING_USE_AI` | bool | Использовать ИИ - Использовать ИИ для генерации приветствия (если выкл., показывает только праздники, события и погоду) |
| `ai.morning_greeting.use_full_model` | `AI_MORNING_GREETING_USE_FULL_MODEL` | bool | Использовать полную модель - Использовать полную модель вместо лёгкой для утреннего приветствия |
| `ai.morning_greeting.time` | `AI_MORNING_GREETING_TIME` | string | Время - Время утреннего приветствия (формат ЧЧ:ММ, по умолч.: 08:00) |
| `ai.morning_greeting.post_to` | `AI_MORNING_GREETING_POST_TO` | []chat_topic | Куда публиковать - Пары (чат, тема), куда отправлять приветствие. Пусто = в основную область каждого чата модерации. |
| `ai.morning_greeting.prompt.system` | `AI_MORNING_GREETING_PROMPT_SYSTEM` | string | Утреннее приветствие (системный) - Системный промпт для генерации утреннего приветствия (плейсхолдеры: `{{weekday}}`, `{{date}}`, `{{weather}}`, `{{weather_ru}}`, `{{holidays}}`, `{{events}}`) |
| `ai.morning_greeting.prompt.user` | `AI_MORNING_GREETING_PROMPT_USER` | string | Утреннее приветствие (пользовательский) - Пользовательский промпт для генерации утреннего приветствия (плейсхолдеры: `{{weekday}}`, `{{date}}`, `{{weather}}`, `{{weather_ru}}`, `{{holidays}}`, `{{events}}`) |

## ИИ - ежедневная сводка

| Ключ YAML | ENV | Тип | Описание |
|---|---|---|---|
| `ai.daily_summary.enabled` | `AI_DAILY_SUMMARY_ENABLED` | bool | Включено - Включить ежедневную сводку чата |
| `ai.daily_summary.time` | `AI_DAILY_SUMMARY_TIME` | string | Время - Время ежедневной сводки (формат ЧЧ:ММ, по умолч.: 02:00) |
| `ai.daily_summary.use_full_model` | `AI_DAILY_SUMMARY_USE_FULL_MODEL` | bool | Использовать полную модель - Использовать полную модель вместо лёгкой для ежедневной сводки |
| `ai.daily_summary.post_to` | `AI_DAILY_SUMMARY_POST_TO` | []chat_topic | Куда публиковать - Пары (чат, тема), куда отправлять сводку. Пусто = в основную область каждого чата модерации. Перечисление одного чата с разными темами публикует одну и ту же сводку в каждую тему. |
| `ai.daily_summary.prompt.system` | `AI_DAILY_SUMMARY_PROMPT_SYSTEM` | string | Ежедневная сводка (системный) - Системный промпт для генерации ежедневной сводки (плейсхолдеры: `{{messages}}`) |
| `ai.daily_summary.prompt.user` | `AI_DAILY_SUMMARY_PROMPT_USER` | string | Ежедневная сводка (пользовательский) - Пользовательский промпт для генерации ежедневной сводки (плейсхолдеры: `{{messages}}`) |

## ИИ - сводки сообщений

| Ключ YAML | ENV | Тип | Описание |
|---|---|---|---|
| `ai.message_summaries.enabled` | `AI_MESSAGE_SUMMARIES_ENABLED` | bool | Включено - Включить ИИ-сводки для длинных сообщений |
| `ai.message_summaries.use_full_model` | `AI_MESSAGE_SUMMARIES_USE_FULL_MODEL` | bool | Использовать полную модель - Использовать полную модель вместо лёгкой для сводок сообщений |
| `ai.message_summaries.light_model_threshold` | `AI_MESSAGE_SUMMARIES_LIGHT_MODEL_THRESHOLD` | int | Порог лёгкой модели - Принудительно использовать лёгкую модель, когда текст превышает это количество символов (0 = отключено) |
| `ai.message_summaries.min_length` | `AI_MESSAGE_SUMMARIES_MIN_LENGTH` | int | Мин. длина - Минимальная длина сообщения для сводки (по умолч.: 1000) |
| `ai.message_summaries.included_topics` | `AI_MESSAGE_SUMMARIES_INCLUDED_TOPICS` | []chat_topic | Включённые темы - Пары (чат, тема), где работает резюмирование длинных сообщений. Пусто = каждый чат модерации / любая тема. |
| `ai.message_summaries.excluded_topics` | `AI_MESSAGE_SUMMARIES_EXCLUDED_TOPICS` | []chat_topic | Исключённые темы - Пары (чат, тема), исключённые из резюмирования сообщений. |
| `ai.message_summaries.excluded_user_ids` | `AI_MESSAGE_SUMMARIES_EXCLUDED_USER_IDS` | []int64 | Исключённые ID пользователей - ID пользователей, исключённых из резюмирования |
| `ai.message_summaries.prompt.system` | `AI_MESSAGE_SUMMARIES_PROMPT_SYSTEM` | string | Сводка сообщения (системный) - Системный промпт для подготовки краткого пересказа сообщения (плейсхолдеры: `{{message}}`) |
| `ai.message_summaries.prompt.user` | `AI_MESSAGE_SUMMARIES_PROMPT_USER` | string | Сводка сообщения (пользовательский) - Пользовательский промпт для подготовки краткого пересказа сообщения (плейсхолдеры: `{{message}}`) |

## ИИ - сводки ссылок

| Ключ YAML | ENV | Тип | Описание |
|---|---|---|---|
| `ai.link_summaries.enabled` | `AI_LINK_SUMMARIES_ENABLED` | bool | Включено - Включить ИИ-сводки для опубликованных ссылок |
| `ai.link_summaries.use_full_model` | `AI_LINK_SUMMARIES_USE_FULL_MODEL` | bool | Использовать полную модель - Использовать полную модель вместо лёгкой для сводок ссылок и перевода |
| `ai.link_summaries.light_model_threshold` | `AI_LINK_SUMMARIES_LIGHT_MODEL_THRESHOLD` | int | Порог лёгкой модели - Принудительно использовать лёгкую модель, когда текст превышает это количество символов (0 = отключено) |
| `ai.link_summaries.excluded_domains` | `AI_LINK_SUMMARIES_EXCLUDED_DOMAINS` | []string | Исключённые домены - Домены, исключённые из резюмирования ссылок |
| `ai.link_summaries.excluded_extensions` | `AI_LINK_SUMMARIES_EXCLUDED_EXTENSIONS` | []string | Исключённые расширения - Расширения файлов для пропуска (напр. .pdf, .doc*), поддерживает подстановку |
| `ai.link_summaries.excluded_user_ids` | `AI_LINK_SUMMARIES_EXCLUDED_USER_IDS` | []int64 | Исключённые ID пользователей - ID пользователей, исключённых из резюмирования ссылок |
| `ai.link_summaries.included_topics` | `AI_LINK_SUMMARIES_INCLUDED_TOPICS` | []chat_topic | Включённые темы - Пары (чат, тема), где формируются сводки ссылок. Пусто = каждый чат модерации / любая тема. |
| `ai.link_summaries.excluded_topics` | `AI_LINK_SUMMARIES_EXCLUDED_TOPICS` | []chat_topic | Исключённые темы - Пары (чат, тема), исключённые из резюмирования ссылок. |
| `ai.link_summaries.extractor_api_key` | `AI_LINK_SUMMARIES_EXTRACTOR_API_KEY` | string 🔒 | Ключ Extractor API - API-ключ для сервиса ExtractorAPI |
| `ai.link_summaries.diffbot_api_key` | `AI_LINK_SUMMARIES_DIFFBOT_API_KEY` | string 🔒 | Ключ Diffbot API - API-ключ для сервиса Diffbot |
| `ai.link_summaries.cloudflare_account_id` | `AI_LINK_SUMMARIES_CLOUDFLARE_ACCOUNT_ID` | string | ID аккаунта Cloudflare - ID аккаунта Cloudflare для Browser Rendering API |
| `ai.link_summaries.cloudflare_api_token` | `AI_LINK_SUMMARIES_CLOUDFLARE_API_TOKEN` | string 🔒 | Токен Cloudflare API - API-токен с разрешением Browser Rendering - Edit |
| `ai.link_summaries.cookies` | `AI_LINK_SUMMARIES_COOKIES` | string 🔒 | Куки - Куки для передачи при извлечении контента ссылок (напр. name1=value1; name2=value2) |
| `ai.link_summaries.user_agent` | `AI_LINK_SUMMARIES_USER_AGENT` | string | User-Agent - Пользовательский заголовок User-Agent для Cloudflare Browser Rendering и ручного извлечения |
| `ai.link_summaries.content_language` | `AI_LINK_SUMMARIES_CONTENT_LANGUAGE` | string | Язык контента - Ожидаемый язык содержимого ссылок |
| `ai.link_summaries.max_extracted_content_length` | `AI_LINK_SUMMARIES_MAX_EXTRACTED_CONTENT_LENGTH` | int | Макс. длина извлечённого контента - Максимум символов для извлечения из ссылок (по умолч.: 4096) |
| `ai.link_summaries.max_download_size_bytes` | `AI_LINK_SUMMARIES_MAX_DOWNLOAD_SIZE_BYTES` | int | Макс. размер загрузки (байт) - Максимальный размер загрузки страницы в байтах (по умолч.: 1048576 = 1 МБ) |
| `ai.link_summaries.min_summary_length` | `AI_LINK_SUMMARIES_MIN_SUMMARY_LENGTH` | int | Мин. длина сводки - Отбрасывать ИИ-сводки короче указанного числа символов; ссылка обрабатывается как при ошибке извлечения (0 = отключено) |
| `ai.link_summaries.prompt.system` | `AI_LINK_SUMMARIES_PROMPT_SYSTEM` | string | Сводка по ссылке (системный) - Системный промпт для подготовки сводки по содержимому ссылок (плейсхолдеры: `{{title}}`, `{{url}}`, `{{content}}`, `{{truncated_suffix}}`) |
| `ai.link_summaries.prompt.user` | `AI_LINK_SUMMARIES_PROMPT_USER` | string | Сводка по ссылке (пользовательский) - Пользовательский промпт для подготовки сводки по содержимому ссылок (плейсхолдеры: `{{title}}`, `{{url}}`, `{{content}}`, `{{truncated_suffix}}`) |

## ИИ - внешние данные

| Ключ YAML | ENV | Тип | Описание |
|---|---|---|---|
| `ai.external_data.weather_latitude` | `AI_EXTERNAL_DATA_WEATHER_LATITUDE` | float64 | Широта для погоды - Широта для данных о погоде (по умолч.: 50.088 - Прага) |
| `ai.external_data.weather_longitude` | `AI_EXTERNAL_DATA_WEATHER_LONGITUDE` | float64 | Долгота для погоды - Долгота для данных о погоде (по умолч.: 14.4208 - Прага) |
| `ai.external_data.holidays_country` | `AI_EXTERNAL_DATA_HOLIDAYS_COUNTRY` | string | Страна праздников - ISO-код страны для праздников (по умолч.: CZ) |
| `ai.external_data.wikipedia_language` | `AI_EXTERNAL_DATA_WIKIPEDIA_LANGUAGE` | string | Язык Википедии - Язык для событий «В этот день» из Википедии (по умолч.: cs) |
| `ai.external_data.translate_wikipedia` | `AI_EXTERNAL_DATA_TRANSLATE_WIKIPEDIA` | bool | Переводить Википедию - Переводить события Википедии через ИИ (по умолч.: true) |

## RSS-ленты

| Ключ YAML | ENV | Тип | Описание |
|---|---|---|---|
| `ai.rss.use_full_model` | `AI_RSS_USE_FULL_MODEL` | bool | Использовать полную модель - Использовать полную модель вместо лёгкой для перевода и резюмирования RSS |
| `ai.rss.light_model_threshold` | `AI_RSS_LIGHT_MODEL_THRESHOLD` | int | Порог лёгкой модели - Принудительно использовать лёгкую модель, когда текст RSS превышает это количество символов (0 = отключено) |
| `ai.rss.translation_prompt.system` | `AI_RSS_TRANSLATION_PROMPT_SYSTEM` | string | Перевод RSS (системный) - Системный промпт для перевода RSS-лент (при отсутствии используется общий перевод) (плейсхолдеры: `{{text}}`) |
| `ai.rss.translation_prompt.user` | `AI_RSS_TRANSLATION_PROMPT_USER` | string | Перевод RSS (пользовательский) - Пользовательский промпт для перевода RSS-лент (при отсутствии используется общий перевод) (плейсхолдеры: `{{text}}`) |
| `ai.rss.summary_prompt.system` | `AI_RSS_SUMMARY_PROMPT_SYSTEM` | string | Сводка RSS (системный) - Системный промпт для подготовки сводки RSS-лент (плейсхолдеры: `{{text}}`) |
| `ai.rss.summary_prompt.user` | `AI_RSS_SUMMARY_PROMPT_USER` | string | Сводка RSS (пользовательский) - Пользовательский промпт для подготовки сводки RSS-лент (плейсхолдеры: `{{text}}`) |

## ИИ - профили пользователей

| Ключ YAML | ENV | Тип | Описание |
|---|---|---|---|
| `ai.user_profiles.enabled` | `AI_USER_PROFILES_ENABLED` | bool | Включено - Включить ежедневную генерацию ИИ-профилей поведения пользователей |
| `ai.user_profiles.time` | `AI_USER_PROFILES_TIME` | string | Время - Время запуска ежедневной задачи обновления ИИ-профилей (ЧЧ:ММ) |
| `ai.user_profiles.prompt.system` | `AI_USER_PROFILES_PROMPT_SYSTEM` | string | Новый профиль (системный) - Системный промпт для генерации новых профилей пользователей (плейсхолдеры: `{{username}}`, `{{messages}}`) |
| `ai.user_profiles.prompt.user` | `AI_USER_PROFILES_PROMPT_USER` | string | Новый профиль (пользовательский) - Пользовательский промпт для генерации новых профилей пользователей (плейсхолдеры: `{{username}}`, `{{messages}}`) |
| `ai.user_profiles.update_prompt.system` | `AI_USER_PROFILES_UPDATE_PROMPT_SYSTEM` | string | Обновление профиля (системный) - Системный промпт для обновления существующих профилей пользователей (плейсхолдеры: `{{username}}`, `{{messages}}`, `{{existing_profile}}`) |
| `ai.user_profiles.update_prompt.user` | `AI_USER_PROFILES_UPDATE_PROMPT_USER` | string | Обновление профиля (пользовательский) - Пользовательский промпт для обновления существующих профилей пользователей (плейсхолдеры: `{{username}}`, `{{messages}}`, `{{existing_profile}}`) |
| `ai.user_profiles.skip_forever_muted_users` | `AI_USER_PROFILES_SKIP_FOREVER_MUTED_USERS` | bool | Пропускать пользователей с бессрочным мутом - Не создавать и не обновлять ИИ-профили для пользователей с постоянным мутом |

## Профили пользователей (отслеживание)

| Ключ YAML | ENV | Тип | Описание |
|---|---|---|---|
| `user_profiles.enabled` | `USER_PROFILES_ENABLED` | bool | Включено - Отслеживать историю изменения имён, дату первого сообщения в каждом чате и активность по дням (независимо от ИИ-профилей) |
| `user_profiles.disable_username_reuse_alerts` | `USER_PROFILES_DISABLE_USERNAME_REUSE_ALERTS` | bool | Отключить уведомления о повторном использовании @username - Не отправлять в админ-чат уведомление о том, что новый user_id использует @username, ранее принадлежавший другому user_id (отслеживание профилей продолжает работать) |

