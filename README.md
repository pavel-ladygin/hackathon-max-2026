# MAX Together

MAX Together — мини-приложение MAX для поиска событий и совместного выбора. Пользователь сохраняет события, создаёт приватную комнату, приглашает второго участника, задаёт предпочтения и голосует. Общий лайк одного события фиксирует совпадение.

## Архитектура и данные

- `frontend/`: React 19, TypeScript, Vite, MAX UI и адаптер MAX Bridge.
- `backend/`: HTTP API и фоновые обработчики на Go; PostgreSQL через pgx; версионируемые миграции Goose.
- `openapi/openapi.yaml`: основной контракт OpenAPI 3.1, по нему генерируются типы Go и TypeScript.
- `compose.yaml`: изолированный локальный стенд для проверки, миграции, загрузка тестового каталога, API и frontend. Синхронизация с внешними источниками включается отдельно.
- `compose.production.yaml`: PostgreSQL, миграции, API, frontend, синхронизация событий и ежедневные уведомления.
- `compose.maintenance.yaml`: ручное восстановление постеров Timepad; в обычный запуск не входит.

События импортируются из KudaGo и Timepad. Локальный стенд загружает два события из `backend/seed/submission.json` в базу `max_together_submission`. Записи с `source=submission-fixture` и `is_demo=false` проходят обычные фильтры выдачи и комнат. Отдельная команда `go run ./cmd/seed` создаёт 44 демо-события с `is_demo=true`, которые обычная выдача исключает.

## Локальный запуск для проверки

Для локального запуска нужны Docker Engine или Docker Desktop и Docker Compose v2. Для HTTP-сценария ниже также нужны `curl` и Python 3. Устанавливать Go, Node.js или PostgreSQL на хост не требуется. Команды выполняются из корня репозитория:

```sh
docker compose --env-file .env.submission.example up -d --build
```

API доступен по адресу `http://localhost:8080/api/v1`, frontend — `http://localhost:8081`. Проверить готовность сервисов:

```sh
curl --fail http://localhost:8080/api/v1/health/ready
curl --fail http://localhost:8081/health
```

### Ожидаемый результат проверки

- `GET /api/v1/health/ready` возвращает HTTP 200.
- Frontend открывается по адресу `http://localhost:8081`.
- Два fixture-события доступны через ленту, поиск и карточки API.
- Два синтетических пользователя проходят HTTP-сценарий комнаты: создание, вступление, предпочтения и общий список.
- Общий `like` одного события даёт `match`.
- После `down` без `-v` и повторного запуска состояние PostgreSQL сохраняется.
- Fixture ticket-click возвращает URL на `.invalid`, а не ссылку реального билетного оператора.

Compose ждёт PostgreSQL, применяет миграции и загружает тестовый каталог. Не добавляйте в репозиторий реальные токены, подписанную `initData`, токены сессий, пароли или production-конфигурацию.

Остановить контейнеры, сохранив локальную базу:

```sh
docker compose --env-file .env.submission.example down
```

Удалить и отдельный том базы:

```sh
docker compose --env-file .env.submission.example down -v
```

Compose-проект и база называются `max-together-submission` и `max_together_submission`; загрузчик тестового каталога откажется работать с другой базой. Том PostgreSQL изолирован от прежнего проекта. Порты по умолчанию — 5432, 8080 и 8081. Если порт занят, задайте другое значение `*_PORT` в копии env-файла и передайте её через `--env-file`.

### Необязательный импорт событий

Обычный проверочный стенд не обращается к поставщикам событий. Чтобы включить периодический импорт:

```sh
docker compose --env-file .env.submission.example --profile live up -d event-sync
docker compose --env-file .env.submission.example logs --since=1h event-sync
```

В профиле `live` импорт KudaGo запускается сразу и повторяется через `EVENT_SYNC_INTERVAL` (по умолчанию — 60 минут). Для Timepad задайте `TIMEPAD_TOKEN` в приватном env-файле.

### Необязательное восстановление постеров Timepad

Восстановление постеров не запускается при обычном локальном старте или production-развёртывании. Утилита `backend/cmd/backfill-timepad-posters` работает в режиме предварительного просмотра, если не передан `--apply`. На production-хосте, из папки с Compose-файлами и приватным env-файлом, сначала просмотрите найденные записи:

```sh
docker compose --env-file .env.production -f compose.production.yaml -f compose.maintenance.yaml --profile maintenance run --build --rm timepad-image-recovery
```

После проверки результата можно выполнить разовое исправление:

```sh
docker compose --env-file .env.production -f compose.production.yaml -f compose.maintenance.yaml --profile maintenance run --build --rm timepad-image-recovery /app/backfill-timepad-posters --apply --limit 50
```

Команде нужен `TIMEPAD_TOKEN`; без него она завершается до обращения к провайдеру. Образ восстановления собирается отдельно и не входит в обычный deploy.

## Авторизация MAX и работа с данными

Для каждого пользователя Mini App отправляет актуальную подписанную MAX строку `initData` в `POST /api/v1/auth/max/bootstrap`. Backend проверяет подпись и срок действия по `MAX_BOT_TOKEN`; `initDataUnsafe` не используется для авторизации. При успешной проверке приложение обновляет запись пользователя и выдаёт случайный токен сессии на 24 часа. API-запросы передают его как Bearer-токен. Клиент хранит токен только в памяти. Исходная `initData` не сохраняется и не используется как постоянный ключ для входа. Не помещайте её в README, DATA-API, логи, скриншоты или публичные отчёты. Двух участников проверяйте отдельно, используя реальные аккаунты MAX A и B.

Приложение сохраняет идентификатор и поля профиля пользователя. Координаты отправляются только с его согласия. Билетные ссылки проверяются по allowlist.

### Просмотр bootstrap-запроса MAX в Network

В авторизованной сессии MAX Web найдите `POST /api/v1/auth/max/bootstrap` во вкладке Network. Запрос содержит `init_data` из MAX Bridge и необязательный `start_param`; ответ — `access_token` и `expires_in`. Скопируйте свежий токен для HTTP-проверок A или B; по истечении сессии повторите вход. `start_param` принимается только после проверки подписанной `init_data`. Не передавайте HAR-файлы и скриншоты с `init_data`, токеном или заголовком Authorization без удаления этих значений.

### Локальная авторизация и HTTP-сценарий

Пример использует только тестовый `submission-only-bot-token` из `.env.submission.example` и обычную HMAC-проверку подписи. Токены сессий хранятся в shell-переменных; закройте сеанс после проверки. Не подставляйте сюда production-токен или настоящую `init_data`.

Из корня репозитория после запуска стенда выполните:

```sh
API=http://127.0.0.1:8080/api/v1
CITY_ID=a0f625ee-2154-5a45-8afe-37adf955ec24
FIXTURE_EVENT_ID=ce8f2695-324f-52b0-abba-4de56e9114c1

bootstrap_body() {
  python3 - "$1" "${2:-}" <<'PY'
import hashlib, hmac, json, sys, time, urllib.parse
user_id, start = sys.argv[1], sys.argv[2]
fields = {
    "auth_date": str(int(time.time())),
    "user": json.dumps({"id": int(user_id), "first_name": "Submission"}, separators=(",", ":")),
}
if start:
    fields["start_param"] = start
key = hmac.new(b"WebAppData", b"submission-only-bot-token", hashlib.sha256).digest()
payload = "\n".join(f"{name}={fields[name]}" for name in sorted(fields)).encode()
fields["hash"] = hmac.new(key, payload, hashlib.sha256).hexdigest()
body = {"init_data": urllib.parse.urlencode(fields)}
if start:
    body["start_param"] = start
print(json.dumps(body, separators=(",", ":")))
PY
}

# Свежая локальная сессия пользователя A; ответ и токен остаются в памяти shell.
A_RESPONSE=$(curl --fail --silent --show-error -X POST "$API/auth/max/bootstrap" \
  -H 'Content-Type: application/json' --data "$(bootstrap_body 900000001)")
A_TOKEN=$(printf '%s' "$A_RESPONSE" | python3 -c 'import json,sys; print(json.load(sys.stdin)["access_token"])')

# Главная лента, поиск и карточка события требуют авторизации. ID события в fixture постоянный.
curl --fail --silent --show-error "$API/feed/home?city_id=$CITY_ID" -H "Authorization: Bearer $A_TOKEN"
curl --fail --silent --show-error "$API/events/search?city_id=$CITY_ID&limit=20" -H "Authorization: Bearer $A_TOKEN"
curl --fail --silent --show-error "$API/events/$FIXTURE_EVENT_ID" -H "Authorization: Bearer $A_TOKEN"

# Создаём комнату от имени A; ID комнаты и токен приглашения храним только в этом shell.
ROOM_RESPONSE=$(curl --fail --silent --show-error -X POST "$API/rooms" \
  -H "Authorization: Bearer $A_TOKEN" -H 'Content-Type: application/json' \
  -H "Idempotency-Key: $(python3 -c 'import uuid; print(uuid.uuid4())')" \
  --data "{\"name\":\"Submission walkthrough\",\"city_id\":\"$CITY_ID\"}")
ROOM_ID=$(printf '%s' "$ROOM_RESPONSE" | python3 -c 'import json,sys; print(json.load(sys.stdin)["room"]["id"])')
INVITE_TOKEN=$(printf '%s' "$ROOM_RESPONSE" | python3 -c 'import json,sys; print(json.load(sys.stdin)["invite"]["token"])')

# В реальном UI приглашение отправляется через MAX share API. Здесь B получает
# свежую сессию с той же подписанной start_param, чтобы проверить локальный API.
B_RESPONSE=$(curl --fail --silent --show-error -X POST "$API/auth/max/bootstrap" \
  -H 'Content-Type: application/json' --data "$(bootstrap_body 900000002 "$INVITE_TOKEN")")
B_TOKEN=$(printf '%s' "$B_RESPONSE" | python3 -c 'import json,sys; print(json.load(sys.stdin)["access_token"])')
curl --fail --silent --show-error -X POST "$API/room-invites/$INVITE_TOKEN/join" \
  -H "Authorization: Bearer $B_TOKEN" -H 'Content-Type: application/json' \
  -H "Idempotency-Key: $(python3 -c 'import uuid; print(uuid.uuid4())')" --data '{}'

# Оба участника задают одинаковые предпочтения на будущую дату, читают список и лайкают первую карточку.
EVENT_DAY=$(docker compose --env-file .env.submission.example exec -T postgres psql -U max_together_submission -d max_together_submission -Atc 'SELECT CURRENT_DATE + 7')
INTENT="{\"dates\":[\"$EVENT_DAY\"],\"day_types\":[],\"time_slots\":[],\"category_slugs\":[\"concerts\"],\"budget_max_minor\":300000,\"exclusion_slugs\":[]}"
for TOKEN in "$A_TOKEN" "$B_TOKEN"; do
  curl --fail --silent --show-error -X PUT "$API/rooms/$ROOM_ID/intent/me" \
    -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' --data "$INTENT"
done
POOL=$(curl --fail --silent --show-error "$API/rooms/$ROOM_ID/events" -H "Authorization: Bearer $A_TOKEN")
POOL_VERSION=$(printf '%s' "$POOL" | python3 -c 'import json,sys; print(json.load(sys.stdin)["pool_version"])')
EVENT_ID=$(printf '%s' "$POOL" | python3 -c 'import json,sys; print(json.load(sys.stdin)["items"][0]["event"]["id"])')
VOTE="{\"pool_version\":$POOL_VERSION,\"vote\":\"like\"}"
for TOKEN in "$A_TOKEN" "$B_TOKEN"; do
  curl --fail --silent --show-error -X PUT "$API/rooms/$ROOM_ID/events/$EVENT_ID/vote" \
    -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' --data "$VOTE"
done
curl --fail --silent --show-error "$API/rooms/$ROOM_ID" -H "Authorization: Bearer $A_TOKEN"
curl --fail --silent --show-error -X POST "$API/events/$EVENT_ID/ticket-click" \
  -H "Authorization: Bearer $A_TOKEN" -H 'Content-Type: application/json' \
  --data "{\"source\":\"match\",\"room_id\":\"$ROOM_ID\"}"
```

Дата fixture вычисляется как дата базы плюс семь дней. Если список ещё не готов (`409 POOL_NOT_READY`), повторите GET через несколько секунд. Ссылка билета fixture ведёт только на зарезервированный домен `.invalid`.

### Отправка приглашения из клиента MAX

Экран приглашения отправляет одинаковые `{text, link}` через `shareMaxContent`, если метод доступен, иначе пробует `shareContent`, затем Web Share API браузера. Отдельно доступно копирование ссылки. В URL находится токен приглашения, а MAX deep link передаёт его в `startapp`. При открытии ссылки вне MAX интерфейс пробует `openMaxLink`, затем `openLink`, если методы доступны, и использует открытие внешнего окна как запасной вариант. Пустые данные обычного браузера не считаются авторизацией MAX.

В production Mini App проверьте отправку, копирование и открытие приглашения в мобильном MAX и MAX Web. Убедитесь, что B вступает в нужную комнату и видит общий список. Отдельно проверьте доступные способы отправки: `shareMaxContent`, `shareContent`, Web Share и копирование ссылки.

## Известные ограничения

- Локальный submission-стенд не авторизуется в настоящем MAX; HTTP-сценарий использует синтетические подписи.
- Полный native invite/deep-link сценарий проверяется в production Mini App двумя реальными MAX-аккаунтами.
- Live-импорт Timepad требует `TIMEPAD_TOKEN`.
- Наличие и актуальность live-каталога зависят от внешних провайдеров KudaGo и Timepad.
- Проект не продаёт билеты и не получает подтверждение покупки.
- Fixture ticket URL использует зарезервированный домен `.invalid`.
- Официальный machine-format DATA-API нужно подтвердить у организаторов; `DATA-API.yaml` — локальный чеклист.
- Способ предоставления A/B MAX-сессий для проверки production API и допустимость интерактивного входа нужно согласовать.

## Требования к сдаче и известные блокеры

В черновике первого технического слайда пока оставлены заглушки для ссылки на бота, репозиторий и commit SHA; также не хватает коротких шагов запуска/проверки и полного списка необходимых env-переменных. Перед сдачей заполните и проверьте:

- рабочую ссылку на MAX-бота/Mini App;
- ссылку на репозиторий и точный SHA итогового коммита из 40 символов;
- HTTPS-адрес API и внешнюю проверку его доступности;
- короткие инструкции для запуска Mini App и основного сценария с двумя пользователями;
- имена/роли тестовых пользователей A и B и способ доступа к ним, если этого требуют организаторы;
- названия нужных env-переменных и безопасный способ передать реальные ключи/токены для проверки.

В приложении нет входа по логину и паролю. Для production HTTP-проверок нужны свежие Bearer-токены двух MAX-аккаунтов. Согласование доступа жюри и официального формата DATA-API остаётся блокером сдачи (см. ограничения выше).

## API и сценарий сдачи

`DATA-API.yaml` содержит последовательность HTTP-проверок, `openapi/openapi.yaml` — методы, параметры и ответы API. Production-сценарий выполняется через штатный bootstrap двух MAX-аккаунтов; постоянные тестовые credentials не предусмотрены.

Рекомендуемый ручной прогон в MAX:

1. Откройте приложение как A, пройдите первичную настройку, найдите событие через ленту или поиск и откройте его.
2. Сохраните событие или уберите его из сохранённых, создайте комнату и отправьте приглашение из MAX.
3. Откройте deep link как B и вступите в комнату.
4. Задайте каждому участнику разные личные предпочтения, затем просмотрите общий список на обоих устройствах.
5. Поставьте лайк одному событию от обоих пользователей, проверьте совпадение и откройте страницу билетного оператора.
6. Откройте комнату повторно и проверьте сохранность состояния. При наличии условий проверьте также пустой список и интерфейс ошибки провайдера.

## Настройка окружения и сервисов

`.env.submission.example` содержит только временные значения для изолированного локального стенда; используйте его напрямую для команд выше либо скопируйте в приватный env-файл. **Не используйте его в production.** `.env.example` — общий шаблон локальной конфигурации. Не заменяйте существующий `.env`, не сохранив его локальные секреты. Production-секреты должны храниться в `/opt/worknet/.env.production` с правами `0600`, а не в репозитории.

Основные настройки API: `APP_ENV`, `HTTP_ADDR`, `DATABASE_URL`, `LOG_LEVEL`, `MAX_BOT_TOKEN`, `INVITE_ENCRYPTION_KEY`, `INVITE_URL_TEMPLATE` и `MAX_DEEP_LINK_TEMPLATE`. Ключ приглашений — base64-представление 32 байт; его нужно сохранять, пока в базе есть приглашения. Оба HTTPS-шаблона должны содержать `{token}` ровно один раз. В локальном примере используются домены `.invalid`. Необязательные настройки: `TIMEPAD_TOKEN`, `VITE_YANDEX_MAPS_API_KEY`, адреса и таймауты синхронизации, интервал её запуска. Списки разрешённых доменов frontend и backend должны включать домены билетных ссылок сценария. Для fixture оба разрешают `tickets.example.invalid`; production-список ниже этот домен исключает.

Настройки production Compose задаются только в `/opt/worknet/.env.production`:

| Переменные | Production-значение / источник |
| --- | --- |
| `POSTGRES_DB`, `POSTGRES_USER`, `POSTGRES_PASSWORD` | Отдельная production-база и пользователь, сложный приватный пароль. |
| `BACKEND_IMAGE`, `FRONTEND_IMAGE` | Образы GHCR с тегом SHA; CI deploy-скрипт подставляет SHA выбранного релиза. |
| `MAX_BOT_TOKEN`, `MAX_APP_URL` | Настоящий токен MAX-бота и публичный адрес Mini App/бота. |
| `INVITE_ENCRYPTION_KEY`, `INVITE_ENCRYPTION_KEY_VERSION` | Стабильный base64-ключ из 32 секретных байт и его версия; не меняйте ключ, пока он нужен сохранённым приглашениям. |
| `INVITE_URL_TEMPLATE`, `MAX_DEEP_LINK_TEMPLATE` | Публичные HTTPS-шаблоны приглашения и MAX deep link; в каждом ровно один `{token}`. |
| `TICKET_PROVIDER_ALLOWLIST` | `kudago.com,*.kudago.com,timepad.ru,*.timepad.ru`; домен `tickets.example.invalid` для fixture не добавляйте. |
| `TIMEPAD_TOKEN` | Необязательный реальный токен поставщика; без него используется только KudaGo. |
| `TRUSTED_PROXY_CIDRS`, `LOG_LEVEL`, `MAX_INIT_DATA_MAX_AGE` | Значения для реальной сети прокси и эксплуатационной политики; Compose сам задаёт `APP_ENV=production`. |

Для сборки frontend переменную `YANDEX_MAPS_API_KEY` можно передать из production-секрета GitHub как аргумент Docker-сборки. 

## Существующие проверки

CI (`.github/workflows/ci-cd.yml`) выполняет текущие проверки. Результаты последней проверки перед сдачей приведены ниже.

Backend: команды из `backend/`; PostgreSQL 17 должен быть запущен, миграции применены, `TEST_DATABASE_URL` задан:

```sh
go run ./cmd/migrate
go test -p 1 ./...
go vet ./...
go build ./...
```

CI использует `-p 1`, потому что пакеты backend-тестов работают с общей базой. Сервис Compose `integration-tests` тоже запускает пакеты последовательно. E2E с настоящим каталогом включаются отдельно: `RUN_KUDAGO_REAL_E2E=1` требует импортированных подходящих событий KudaGo; `RUN_REAL_PROVIDER_ROOM_E2E=1` требует подходящих будущих событий и из KudaGo, и из Timepad. Для локальной проверки на fixture они не нужны.

Контракты и генерируемые типы:

```sh
python -m pip install -r backend/tests/contract/requirements.txt
python -m unittest discover -s backend/tests/contract -v
(cd backend && go generate ./internal/httpapi/openapi)
(cd frontend && npm ci && npm run generate:api)
```

Frontend: команды выполняются из `frontend/`:

```sh
npm ci
npm run check
npx playwright install chromium
npm run test:e2e
```

`npm run check` запускает проверку политики поставщиков, typecheck, lint, unit-тесты и production-сборку. `npm run test:motion` — дополнительный существующий набор проверок анимации, вне стандартного CI. CI также задаёт `RUN_GENERIC_BROWSER_E2E=1` для `go test ./internal/eventsources -run '^TestGenericEventSourcesBrowserAgainstLiveHandler$' -count=1`.

Docker/release workflow дополнительно проверяет обе Compose-конфигурации, собирает образы backend/frontend, запускает smoke-стенд с миграциями, fixture, API и frontend, проверяет доступность API/frontend, выполняет `deploy/test-internal-nginx.sh`, сканирует образы через Trivy и репозиторий через Trufflehog. Публикация и deploy запускаются при push в `main` и при merge PR с меткой `deploy-dev` в ветку `dev` этого же репозитория; обычные push в `dev` запускают CI, но не публикуют и не развёртывают проект. Откат запускается вручную и использует полный commit SHA опубликованной версии.

### Результаты проверки перед сдачей

- Backend: полный последовательный `go test -p 1 -count=1 ./...`, `go vet ./...` и `go build ./...` прошли на отдельной тестовой базе. Текущая версия миграций — 24.
- Генерация контрактов: генерация OpenAPI для Go и sqlc v1.29.0 прошла; после явных приведений SQL к типу `text` не осталось расхождений типов Go-моделей. Проверка OpenAPI прошла 4 из 4 случаев. Локальный сценарий `DATA-API.yaml` совпал с 13 существующими маршрутами/методами/кодами ответа OpenAPI.
- Frontend: `npm run check` прошёл, 159 проверок; браузерные E2E — 46 тестов. Motion E2E со второй попытки прошёл 4 из 4 после первого нестабильного результата 3 из 4.
- Запуск: браузерная проверка обработчика Generic-источников прошла (119,658 с); проверка защиты маршрутов Nginx прошла. После перезапуска сохранились обе fixture-записи и запись совпавшей комнаты; readiness оставался успешным.
- Docker: проверки локальной и production Compose-конфигураций и сборки образов приложения для `linux/amd64` прошли. Trivy 0.70.0 проверил все четыре итоговых ARM64/AMD64-образа backend/frontend по политике CI (HIGH/CRITICAL, ignore-unfixed); совпадений с уязвимостями не найдено. Замеры ARM64 приведены ниже; время отдельной production-сборки AMD64 не измерялось.
- Поиск секретов: TruffleHog проверил файлы проекта без сети и проверки учётных данных; 8 совпадений — синтетические примеры PostgreSQL и тестовые URI. Зависимости, результаты сборки и `.git` исключены. Перед merge обязателен CI-скан с проверкой учётных данных и полной историей.

### Замер сборки Docker

Для отдельного замера cold/warm использовалась команда:

```sh
docker compose --env-file .env.submission.example build
```

- Cold: **78,642 с**; warm: **7,116 с**; обе сборки завершились успешно.
- Для финального cold-замера создан новый Buildx `docker-container` builder; заранее подготовлены только базовые образы. Загрузка Go-модулей, `npm ci`, компиляция и экспорт образов вошли в замер. Проверялась итоговая версия исходников, включая приведения типов в SQL.
- Среда: Docker 28.5.1, Linux `aarch64` / `linux/arm64`, 8 CPU, около 3,83 ГиБ RAM.
- Измеренная сборка ARM64 укладывается в лимит 300 секунд. Время отдельной сборки production `linux/amd64` не измерялось.

Для диагностики после отдельной сборки можно запустить готовые образы без пересборки:

```sh
docker compose --env-file .env.submission.example up -d --no-build
```

## Production-развёртывание

Заявленный адрес сервиса — `https://worknet.team`, API: `https://worknet.team/api/v1`. Перед релизом нужно проверить доступность, TLS, настройки бота, синхронизацию поставщиков и сценарий двух пользователей именно в целевом окружении. Production Nginx завершает TLS и проксирует frontend/API на loopback-порты; PostgreSQL не публикуется на хосте.

`deploy/deploy.sh <40-character-commit-sha>` создаёт резервную копию PostgreSQL, применяет миграции, обновляет образы и запускает внешние smoke-проверки; при ошибке возвращает прежние образы. Скрипт развёртывает backend, frontend, `event-sync` и `daily-notifications`; восстановление постеров остаётся отдельной операцией. Обязательные секреты GitHub для production: `DEPLOY_HOST`, `DEPLOY_USER`, `DEPLOY_SSH_KEY`, `DEPLOY_HOST_KEY`; `YANDEX_MAPS_API_KEY` необязателен. Настройте production env в закрытом файле на сервере и разрешите Docker скачивать образы релиза из GHCR.

Если после прежнего релиза остался постоянно работающий recovery-контейнер, его удаление — отдельное ручное действие на хосте; обычный deploy-скрипт его не удаляет. Сначала проверьте соответствие Compose-проекта и сервиса, затем остановите и удалите только найденные контейнеры:

```sh
legacy_recovery_ids=$(docker ps -q --filter label=com.docker.compose.project=max-together-production --filter label=com.docker.compose.service=timepad-image-recovery)
if [ -n "$legacy_recovery_ids" ]; then
  docker stop $legacy_recovery_ids
  docker rm $legacy_recovery_ids
fi
```
