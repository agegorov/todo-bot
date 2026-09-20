# Todo Bot — Claude Context

Персональный todo-инструмент: Telegram-бот + веб-канбан. Один VPS, один Docker Compose, один go-бинарник запускает и бота, и HTTP-сервер, и планировщик напоминаний.

Работа ведётся на русском. Технические имена — на английском.

---

## Цель проекта

- Быстро добавлять задачи с телефона голосом или текстом через Telegram-бота
- Управлять задачами на канбан-доске в вебе (Trello-подобный интерфейс)
- Русский естественный ввод: «купить молоко завтра в 18 #дом» → задача с дедлайном, тегом и проектом
- Мульти-пользовательский с Google OAuth, каждый пользователь видит только свою доску

## Стек

| Слой | Технология |
|------|------------|
| Язык | Go 1.26 |
| DB | PostgreSQL 16 + [sqlc](https://sqlc.dev) для type-safe кода |
| DB-драйвер | `pgx/v5` через `pgxpool` (пул, не одиночное соединение) |
| HTTP | `go-chi/chi/v5` + `go-chi/cors` |
| Telegram | `go-telegram-bot-api/telegram-bot-api/v5` (long polling) |
| Auth | `golang.org/x/oauth2` (Google), сессии в БД |
| Whisper | `whisper.cpp` HTTP-сервер как systemd-сервис вне Docker |
| Планировщик | `robfig/cron/v3` |
| Frontend | Vanilla HTML/CSS/JS + `timepicker-ui@4.3.0` (ESM через esm.sh) |
| Деплой | Docker Compose + GitHub Actions (SSH) |

## Домен

- Прод: `http://todo-board.duckdns.org:3000` (VPS Arsys, `212.227.40.199`, root)
- Google OAuth redirect: `http://todo-board.duckdns.org:3000/auth/callback`
- Whisper endpoint: `http://host.docker.internal:8080` (снаружи docker-сети через `extra_hosts: host-gateway`)

---

## Архитектура

```
cmd/bot/main.go            — entrypoint, инициализация всего
internal/
  bot/                     — Telegram-бот, парсинг сообщений, команды
  api/                     — HTTP API + отдача статики + auth-роуты
  auth/                    — Google OAuth, сессии, middleware
  db/                      — sqlc-сгенерированный код + SQL-миграции
    migrations/            — файлы 001_*.sql … 007_*.sql, порядок важен
    queries/               — .sql файлы для sqlc
  parser/                  — русский NLP-парсер (regex, без внешних AI)
  recurrence/              — периодические задачи: расчёт дедлайна + спавн копии
  scheduler/               — cron для напоминаний
  whisper/                 — HTTP-клиент к whisper.cpp + ffmpeg OGG→WAV
web/                       — статический фронтенд (index.html, login.html)
scripts/deploy.sh          — на сервере: git pull → миграции → пересборка бота
```

Один бинарник поднимает три вещи параллельно: Telegram-бот (`bot.Run`), HTTP-сервер (`http.Server`), scheduler (`sched.Start`).

---

## База данных

### Ключевые таблицы

| Таблица | Что хранит |
|---------|-----------|
| `users` | Google-аккаунты + опционально `telegram_id` для привязки |
| `sessions` | Веб-сессии (30 дней TTL), очищаются cron'ом |
| `link_tokens` | Одноразовые токены для привязки TG-аккаунта к веб-юзеру (15 мин TTL) |
| `projects` | 4 системных: Inbox / Work / Home / Personal |
| `board_columns` | Пользовательские колонки. Поле `system_kind`: `NULL` / `'todo'` / `'done'` |
| `tasks` | user_id + column_id + project_id + tags через `task_tags` |
| `tags`, `task_tags` | many-to-many |
| `reminders` | Напоминания за час до дедлайна, разбирает scheduler |
| `schema_migrations` | Отслеживает применённые миграции (создаётся deploy.sh) |

### Инварианты

- У каждого пользователя ровно **одна** колонка `system_kind='todo'` и **одна** `system_kind='done'` (уникальный индекс `idx_unique_system_kind_per_user`)
- Системные колонки нельзя удалить (DELETE фильтрует `system_kind IS NULL`), можно переименовать и менять цвет
- Новые задачи (веб и бот) всегда попадают в колонку `todo`
- Задача с `done_at IS NOT NULL` = завершённая. При переносе в Done-колонку `done_at` ставится автоматически (через CASE в `MoveTaskToColumn`), при переносе обратно — сбрасывается
- `ListTasksForBoard` возвращает **все** задачи (включая выполненные) — done видны внутри Done-колонки

### Периодические задачи

`is_recurring BOOLEAN` + `recur_rule TEXT`. Правила: `daily` / `weekly` / `biweekly` / `monthly` / `yearly`.

При закрытии периодической задачи (`recurrence.Spawn`):
- Якорь = **старый дедлайн** (не время завершения), чтобы день недели/время сохранялись
- Если следующий дедлайн получился в прошлом — доматывается период до попадания в будущее
- Копируются теги, приоритет, notes, project, telegram_user_id
- Новая копия кладётся в TO DO колонку с напоминанием за час

### Привязка Telegram

Многопользовательская схема:
1. Бот принимает от **любого** Telegram-юзера (после того как убрали ownerID)
2. При создании задачи бот ищет `users.telegram_id == msg.From.ID`
3. Если найден → задача сразу с `user_id` и в TO DO колонке
4. Если нет → «orphan» (`user_id=NULL`, `telegram_user_id=<tg>`), ждёт `/link`
5. `/link <token>` (токен генерится в вебе, живёт 15 мин) → `users.telegram_id = <tg>`, `ClaimTasksByTelegram` подцепляет все orphan

---

## Как деплоить

**Одна команда на сервере:**

```bash
/root/todo-bot/scripts/deploy.sh
```

Что она делает:
1. `git pull`
2. Создаёт `schema_migrations` если её нет (на новой БД)
3. Bootstrap: если БД уже жила ДО deploy.sh (есть `users`, но `schema_migrations` пуста), помечает все текущие миграции как применённые
4. Применяет только новые `.sql` из `internal/db/migrations/` по порядку
5. `docker compose build bot && docker compose up -d bot`
6. Показывает 15 строк логов

**Первичная настройка на новом сервере:**
```bash
cd /root/todo-bot && git pull && chmod +x scripts/deploy.sh
```

**Тонкости деплоя:**
- Статика (`web/*.html`) маунтится в контейнер через volume — правки в HTML/CSS применяются без пересборки, только `Ctrl+Shift+R` в браузере
- Го-код требует пересборки → `docker compose build bot`
- Миграции + пересборка = `scripts/deploy.sh`

---

## Environment переменные

В `.env` на сервере (**не в git**):

```
TELEGRAM_TOKEN=8705172620:...
DATABASE_URL=postgres://todobot:todobot@postgres:5432/todobot
WHISPER_ENDPOINT=http://host.docker.internal:8080
GOOGLE_CLIENT_ID=...apps.googleusercontent.com
GOOGLE_CLIENT_SECRET=GOCSPX-...
BASE_URL=http://todo-board.duckdns.org:3000
WEB_PORT=3000                       # опционально, по умолчанию 3000
```

---

## Соглашения по коду

### Go / sqlc

- **Не использовать `pgtype.Int8`/`pgtype.Text` в коде** — sqlc для наших схем генерит `*int64` и `*string`. `pgtype.*` только там, где sqlc сам их использует (`pgtype.Timestamptz` для timestamps)
- Работа с БД — **строго через `pgxpool`**, никаких `pgx.Conn` (были баги «conn busy» из-за конкурентного доступа бота, API и scheduler к одному соединению)
- `sqlc generate` после любых изменений `.sql`-файлов
- Ошибки оборачивать: `fmt.Errorf("save task: %w", err)`

### Миграции

- Имя файла: `NNN_snake_case.sql` (номер по порядку, 3 знака)
- **Только вперёд**: миграция накатывается один раз, ролбэков нет
- В миграции — только идемпотентные операции по возможности (`IF NOT EXISTS`, `IF EXISTS`)
- Изменения индексов / уникальностей нужно продумывать: старая уникальность может конфликтовать с новыми данными

### Frontend

- Vanilla JS. Никаких React/Vue/фреймворков
- Все цвета через CSS-переменные — есть тёмная (`:root`) и светлая (`[data-theme="light"]`) темы
- `data-theme` навешивается на `<html>`, значение читается из `localStorage.theme`
- При смене темы диспатчится `CustomEvent('themechange')` — timepicker-ui подписан и меняет свою тему

### Git

- Коммиты на английском, содержательные сообщения (что и зачем)
- Всегда добавлять `Co-Authored-By: Claude ...` в конце
- **НЕ push и НЕ commit без явной просьбы**
- HEREDOC для многострочных commit messages: `git commit -m "$(cat <<'EOF'...)"`

---

## Что уже сделано — фичи

- ✅ Telegram-бот с текстом и голосом (whisper.cpp + ffmpeg OGG→WAV)
- ✅ Русский NLP-парсер: даты (сегодня/завтра/`15 мая`/`в пятницу`), время (`в HH:MM`), теги (`#work`), приоритет (срочно=1), проекты (по ключевым словам), делегирование, периодичность
- ✅ Веб-канбан: dnd задач между колонками, dnd самих колонок, инлайн переименование, цвета
- ✅ Пользовательские колонки + системные `TO DO` и `Done` (не удаляются, но переименовываются)
- ✅ Google OAuth + мультипользователи, изоляция досок
- ✅ Привязка Telegram → веб через `/link <token>` (одноразовый, 15 мин)
- ✅ Кнопка ✓ на карточке = перенос в Done + `done_at`
- ✅ Автотег `#telegram` на задачи из бота
- ✅ Периодические задачи (daily/weekly/biweekly/monthly/yearly) — при закрытии спавнится следующая
- ✅ Редактирование задач: title, notes, priority, deadline, tags, recurrence
- ✅ Просмотр «По колонкам» / «По тегам», фильтр по тегам
- ✅ Тёмная / светлая тема с сохранением в localStorage
- ✅ timepicker-ui циферблат (Material 3, 24h, 5-мин шаги), реагирует на смену темы
- ✅ Дата на карточке серая по умолчанию, красная только если сегодня/завтра/просрочено, и всегда серая в Done
- ✅ Esc закрывает любое открытое модальное окно
- ✅ Клик по всей карточке = редактирование
- ✅ Deploy-скрипт со stateful миграциями (`schema_migrations` таблица)
- ✅ Бот реально мультипользовательский: owner-гейт снят, `/list`/`/today`/`/overdue` фильтруют по scope отправителя (веб-аккаунт либо собственные orphan-задачи), `/done` не даёт закрыть чужую orphan-задачу, напоминания и еженедельный дайджест адресные (в чат владельца задачи/пользователя, а не единственному owner'у)

## Что НЕ сделано, известные ограничения

- Нет поиска по задачам
- Нет календарного вида
- Нет подзадач/чеклистов
- Нет mobile-адаптива (доска на телефоне неудобна)
- Нет archive → Done растёт неограниченно
- Нет push-уведомлений (только напоминание в Telegram за 1 час до дедлайна, и только если у пользователя привязан Telegram)
- Нет ручной сортировки карточек внутри колонки (порядок = priority + deadline)
- Нет цветовой дифференциации тегов
- `CleanExpiredSessions` сгенерирован sqlc, но не вызывается — просроченные сессии не чистятся

---

## Известные грабли (что уже ловили)

- **`conn busy`** — если делать `pgx.Conn` вместо `pgxpool`, бот + API + scheduler конкурируют за одно соединение и падают. Использовать `pgxpool.New()`
- **Whisper 400 Invalid request** — Telegram шлёт OGG/Opus, whisper.cpp хочет WAV 16kHz mono. `internal/whisper` конвертит через ffmpeg перед отправкой. `ffmpeg` должен быть в контейнере (`apk add ffmpeg` в Dockerfile)
- **Orphan tasks невидимы на доске** — если бот создаёт задачу с `user_id=NULL`, `ListTasksForBoard` её не вернёт. Бот теперь ищет юзера по `telegram_id` при создании
- **`timepicker-ui` без UMD** — библиотека публикует только ESM/CJS. Подключаем через `<script type="module">` + `https://esm.sh/timepicker-ui@4.3.0`
- **Порядок в `WHERE ... IN` и подзапросах с `id`** — sqlc падает на `column reference "id" is ambiguous`. Явно алиасить (`tasks.id`, `bc.id`)
- **`docker compose exec` с `-T`** — без `-T` не работает если stdin не TTY: `docker compose exec -T postgres psql ...`
- **CSS `::after` съезжает** — timepicker-ui мутирует DOM вокруг input, псевдоэлементы уезжают. Вместо `::after` для иконки — background-image SVG на самом input
- **Прод-миграция при первом запуске deploy.sh** — если БД уже жила, скрипт bootstrap'ит `schema_migrations` всеми существующими файлами. Убедиться что руками ничего не забыто

---

## Полезные psql-запросы

```bash
# Вход в БД
docker compose -f /root/todo-bot/docker-compose.yml exec postgres psql -U todobot -d todobot
```

```sql
-- Что накатили
SELECT * FROM schema_migrations ORDER BY applied_at;

-- Пользователи + их привязка к TG
SELECT id, email, telegram_id FROM users;

-- Задачи с колонкой и юзером
SELECT t.id, t.title, u.email AS user, bc.name AS col, t.done_at
FROM tasks t
LEFT JOIN users u ON u.id = t.user_id
LEFT JOIN board_columns bc ON bc.id = t.column_id
ORDER BY t.id DESC LIMIT 20;

-- Осиротевшие задачи (не привязаны к юзеру)
SELECT id, title, telegram_user_id FROM tasks WHERE user_id IS NULL;

-- Починить осиротевшие задачи после первого /link
UPDATE tasks t SET user_id = u.id
FROM users u
WHERE t.telegram_user_id = u.telegram_id AND t.user_id IS NULL;

UPDATE tasks SET column_id = bc.id
FROM board_columns bc
WHERE bc.user_id = tasks.user_id AND bc.system_kind = 'todo'
  AND tasks.column_id IS NULL AND tasks.user_id IS NOT NULL;
```

---

## Предпочтения владельца

- Общение на русском
- Ответы краткие и по делу, никаких предисловий
- Перед `git commit` / `push` **спрашивать**, не делать самостоятельно (кроме случаев когда явно попросили)
- Не изобретать абстракции ради абстракций; минимально-необходимое изменение
- Для UI-изменений — не спамить пересборкой бота (статика через volume)
- Все секреты — в `.env` на сервере, не в репо
- Никаких OpenAI/Anthropic-API интеграций (парсер намеренно самописный regex)
