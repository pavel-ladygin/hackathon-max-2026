# Проверка коллизии миграции 000032 перед объединением dev и main

Предоставленный оператором read-only снимок production от 21.10.2026 подтверждает:
версия 31 применена в 13:37:57.954471, версия 32 — в 19:32:52.188984;
`event_image_uploads` существует, три `analytics_recommendation_*` view отсутствуют,
версии 33 нет. Это состояние main с применённой uploads-миграцией. Codex не
подключался к production и не запускал там миграции.

Итоговая последовательность объединённой ветки:

| Версия | Файл |
|---|---|
| 32 | `000032_event_image_uploads.sql` — SQL без изменений относительно main |
| 33 | `000033_recommendation_analytics.sql` — прежний SQL аналитики dev |
| 34 | `000034_saint_petersburg_city.sql` — прежний SQL metadata СПб dev |

Наличие номера 32 в Goose само по себе не различает прежние конкурирующие файлы:
его смысл подтверждён одновременно историей и объектами production. Проверку
уникальности `ExpectedVersions`/Goose не ослаблять и историю Goose не редактировать.

## Снимок состояния: только чтение, выполняет оператор

На VPS использовать уже настроенный способ доступа к PostgreSQL. Не передавать
Codex DSN, пароли, `.env`, данные пользователей или полный `docker inspect`.
Следующий SQL ничего не изменяет; не запускать `cmd/migrate`, `goose up/down`,
ручные DDL или UPDATE/INSERT в `goose_db_version`.

```sql
BEGIN READ ONLY;
SET LOCAL statement_timeout = '5s';
SET LOCAL lock_timeout = '2s';
SELECT clock_timestamp() AS captured_at,
       current_setting('transaction_read_only') AS read_only;

-- Все записи, включая откаты и повторные применения: важен порядок по id.
SELECT id, version_id, is_applied, tstamp
FROM public.goose_db_version ORDER BY id;

SELECT n.nspname AS schema_name, c.relname, c.relkind
FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = 'public' AND c.relname IN (
  'event_image_uploads', 'analytics_recommendation_rounds',
  'analytics_recommendation_room_events', 'analytics_recommendation_rooms'
) ORDER BY c.relname;

SELECT column_name, data_type, is_nullable, column_default
FROM information_schema.columns
WHERE table_schema = 'public' AND table_name = 'event_image_uploads'
ORDER BY ordinal_position;

SELECT conname, pg_get_constraintdef(oid) AS definition
FROM pg_constraint WHERE conrelid = to_regclass('public.event_image_uploads')
ORDER BY conname;
SELECT indexname, indexdef FROM pg_indexes
WHERE schemaname = 'public' AND tablename = 'event_image_uploads'
ORDER BY indexname;

SELECT c.relname, pg_get_viewdef(c.oid, true) AS definition
FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname = 'public' AND c.relkind = 'v' AND c.relname IN (
  'analytics_recommendation_rounds', 'analytics_recommendation_room_events',
  'analytics_recommendation_rooms'
) ORDER BY c.relname;

SELECT id, name, timezone, center_lat, center_lng FROM public.cities
WHERE id IN ('3d3c83de-691b-510e-85e5-79685c4012ee',
             'bf61fbf5-8c5b-53da-96c7-3560df3f8177')
   OR name IN ('Казань', 'Санкт-Петербург') ORDER BY id;
COMMIT;
```

Передать вывод и идентификатор реально запущенного backend/migration image
(immutable digest или release commit), время последнего успешного запуска
миграций. Логи успешного CI или merge PR сами по себе не подтверждают состояние
production. Отдельно сообщить, применялись ли ветки к другим сохраняемым БД.

## Upgrade и проверки

Сопоставить историю Goose и фактические определения объектов. Отсутствие одной
таблицы или view при наличии версии 32 требует отдельного разбора, а не вывода
«версия свободна». Если номер уже применён, простая перенумерация может привести
к пропуску нужного DDL, повторному CREATE или неверному Down.

Для подтверждённого production-состояния 32 обычный Up применяет только 33, затем
34; uploads не создаётся повторно. Проверять на disposable PostgreSQL: чистая БД,
upgrade 32→34 с существующими uploads/заявками/комнатами/голосами, повторный Up без
новых записей истории и потери данных, наличие всех analytics view и metadata СПб.
Не заполнять Goose вручную и не использовать IF NOT EXISTS как замену проверке
существующей схемы. Down-тесты выполнять только на disposable БД.

Если иная сохраняемая БД ранее применяла аналитику под номером 32 или СПб под
номером 33 из dev, её история отличается от production. Не запускать этот Up
вслепую: отдельно сопоставить историю/объекты и согласовать переход. Production
снимок не является доказательством состояния других окружений.

После проверок backend/frontend/Compose и актуальности веток разрешён merge commit
локального `origin/main → dev` и обычный push в dev. PR dev → main и деплой
выполняются отдельно человеком. Оба Timepad worker сохраняют upsert-only=true до
операторского подтверждения полноты реального импорта; см. PROVIDER_SYNC_RUNBOOK.md.
