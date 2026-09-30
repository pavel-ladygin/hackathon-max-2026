# Локальная HTTP-проверка двух участников

Сценарий проверяет локальный submission-стенд: bootstrap двух синтетических MAX-пользователей, ленту и поиск, комнату, общий пул, совпадение и безопасную fixture-ссылку на билет. Он обращается только к `127.0.0.1`; не используйте его с production API.

Нужны Docker Compose v2, `curl`, Python 3 и `psql` внутри контейнера PostgreSQL. Из каталога `hackathon-max-2026` запустите стенд, если он ещё не запущен:

```sh
docker compose --env-file .env.submission.example up -d --build
```

Затем выполните весь блок ниже в Bash из того же каталога. Скрипт ограничивает ожидание readiness и пула, а ошибки сообщает без тел ответов. Не включайте `set -x`, не копируйте ответы bootstrap, заголовки Authorization или приглашение в логи и отчёты.

```bash
set -Eeuo pipefail
set +x

API=http://127.0.0.1:8080/api/v1
CITY_ID=a0f625ee-2154-5a45-8afe-37adf955ec24
FIXTURE_EVENT_ID=ce8f2695-324f-52b0-abba-4de56e9114c1
POOL_ATTEMPTS=30
POOL_RETRY_SECONDS=2
POOL_TIMEOUT_SECONDS=120

fail() {
  printf 'ERROR: %s\n' "$1" >&2
  exit 1
}

cleanup() {
  unset A_TOKEN B_TOKEN A_RESPONSE B_RESPONSE CREATE_RESPONSE JOIN_RESPONSE \
    A_VOTE B_VOTE SNAPSHOT_A SNAPSHOT_B TICKET_RESPONSE INVITE_TOKEN ROOM_ID \
    POOL_A POOL_B RESPONSE BODY BOOTSTRAP_BODY
}
trap cleanup EXIT

wait_http_200() {
  local url="$1" label="$2" code='' deadline remaining request_timeout connect_timeout
  deadline=$((SECONDS + 60))
  while (( SECONDS < deadline )); do
    remaining=$((deadline - SECONDS))
    request_timeout=2
    (( request_timeout > remaining )) && request_timeout=$remaining
    connect_timeout=1
    (( connect_timeout > request_timeout )) && connect_timeout=$request_timeout
    if code=$(curl --silent --connect-timeout "$connect_timeout" --max-time "$request_timeout" \
      --output /dev/null --write-out '%{http_code}' "$url") && [[ "$code" == 200 ]]; then
      return 0
    fi
    remaining=$((deadline - SECONDS))
    (( remaining <= 0 )) && break
    sleep_seconds=2
    (( sleep_seconds > remaining )) && sleep_seconds=$remaining
    sleep "$sleep_seconds"
  done
  fail "$label не стал доступен за 60 секунд"
}

wait_http_200 "$API/health/ready" 'API readiness'
wait_http_200 'http://127.0.0.1:8081/health' 'Frontend health'

api_call() {
  local expected_status="$1" method="$2" path="$3" token="$4"
  local response status body
  shift 4
  local -a args=(--silent --connect-timeout 3 --max-time 10 \
    --write-out $'\n%{http_code}' --request "$method" "$API$path")
  [[ -z "$token" ]] || args+=(--header "Authorization: Bearer $token")
  args+=("$@")
  response=$(curl "${args[@]}") || fail 'Локальный HTTP-запрос не завершился'
  status=${response##*$'\n'}
  body=${response%$'\n'*}
  [[ "$status" == "$expected_status" ]] || fail 'Локальный API вернул неожиданный HTTP-статус'
  printf '%s' "$body"
}

json_field() {
  local expression="$1"
  python3 -c "import json,sys; value=json.load(sys.stdin)$expression; print(value)"
}

bootstrap_body() {
  python3 - "$1" "${2:-}" <<'PY'
import hashlib, hmac, json, sys, time, urllib.parse

user_id, start_param = sys.argv[1], sys.argv[2]
fields = {
    "auth_date": str(int(time.time())),
    "user": json.dumps({"id": int(user_id), "first_name": "Submission"}, separators=(",", ":")),
}
if start_param:
    fields["start_param"] = start_param
key = hmac.new(b"WebAppData", b"submission-only-bot-token", hashlib.sha256).digest()
payload = "\n".join(f"{name}={fields[name]}" for name in sorted(fields)).encode()
fields["hash"] = hmac.new(key, payload, hashlib.sha256).hexdigest()
body = {"init_data": urllib.parse.urlencode(fields)}
if start_param:
    body["start_param"] = start_param
print(json.dumps(body, separators=(",", ":")))
PY
}

RUN_ID=$(python3 -c 'import secrets; print(secrets.randbelow(800000000000) + 100000000000)')
USER_A="$RUN_ID"
USER_B=$((RUN_ID + 1))

# Оба ID новые при каждом запуске, чтобы прежняя комната не мешала повторной проверке.
BOOTSTRAP_BODY=$(bootstrap_body "$USER_A")
A_RESPONSE=$(api_call 200 POST /auth/max/bootstrap '' \
  --header 'Content-Type: application/json' --data "$BOOTSTRAP_BODY") \
  || fail 'Не удалось открыть локальную сессию A'
unset BOOTSTRAP_BODY
A_TOKEN=$(printf '%s' "$A_RESPONSE" | json_field '["access_token"]') \
  || fail 'Не удалось разобрать локальную сессию A'
unset A_RESPONSE

BOOTSTRAP_BODY=$(bootstrap_body "$USER_B")
B_RESPONSE=$(api_call 200 POST /auth/max/bootstrap '' \
  --header 'Content-Type: application/json' --data "$BOOTSTRAP_BODY") \
  || fail 'Не удалось открыть локальную сессию B'
unset BOOTSTRAP_BODY
B_TOKEN=$(printf '%s' "$B_RESPONSE" | json_field '["access_token"]') \
  || fail 'Не удалось разобрать локальную сессию B'
unset B_RESPONSE

# Эти запросы должны вернуть HTTP 200; тела остаются в памяти shell.
FEED=$(api_call 200 GET "/feed/home?city_id=$CITY_ID" "$A_TOKEN") \
  || fail 'Не удалось получить главную ленту'
SEARCH=$(api_call 200 GET "/events/search?city_id=$CITY_ID&limit=20" "$A_TOKEN") \
  || fail 'Не удалось выполнить поиск событий'
DETAIL=$(api_call 200 GET "/events/$FIXTURE_EVENT_ID" "$A_TOKEN") \
  || fail 'Не удалось получить карточку fixture-события'
unset FEED SEARCH DETAIL

CREATE_BODY=$(python3 - "$CITY_ID" <<'PY'
import json, sys
print(json.dumps({"name": "Local HTTP walkthrough", "city_id": sys.argv[1]}, separators=(",", ":")))
PY
)
CREATE_RESPONSE=$(api_call 201 POST /rooms "$A_TOKEN" \
  --header 'Content-Type: application/json' \
  --header "Idempotency-Key: $(python3 -c 'import uuid; print(uuid.uuid4())')" \
  --data "$CREATE_BODY") || fail 'Не удалось создать комнату'
unset CREATE_BODY
ROOM_ID=$(printf '%s' "$CREATE_RESPONSE" | json_field '["room"]["id"]') \
  || fail 'Не удалось прочитать ID комнаты'
INVITE_TOKEN=$(printf '%s' "$CREATE_RESPONSE" | json_field '["invite"]["token"]') \
  || fail 'Не удалось прочитать приглашение'
unset CREATE_RESPONSE

# B подписывает тот же invite token как start_param; значения не выводятся.
BOOTSTRAP_BODY=$(bootstrap_body "$USER_B" "$INVITE_TOKEN")
B_RESPONSE=$(api_call 200 POST /auth/max/bootstrap '' \
  --header 'Content-Type: application/json' --data "$BOOTSTRAP_BODY") \
  || fail 'Не удалось обновить локальную сессию B с контекстом приглашения'
unset BOOTSTRAP_BODY
B_TOKEN=$(printf '%s' "$B_RESPONSE" | json_field '["access_token"]') \
  || fail 'Не удалось разобрать локальную сессию B'
unset B_RESPONSE

JOIN_RESPONSE=$(api_call 200 POST "/room-invites/$INVITE_TOKEN/join" "$B_TOKEN" \
  --header "Idempotency-Key: $(python3 -c 'import uuid; print(uuid.uuid4())')") \
  || fail 'Не удалось присоединить B к комнате'
python3 -c 'import json,sys; d=json.load(sys.stdin); assert len(d["participants"]) == 2' \
  <<<"$JOIN_RESPONSE" || fail 'После вступления в комнате не оказалось двух участников'
unset JOIN_RESPONSE INVITE_TOKEN

# Берём дату из той же БД, что и fixture-записи, и задаём одинаковые условия A и B.
EVENT_DAY=$(docker compose --env-file .env.submission.example exec -T postgres sh -ec \
  'psql -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Atc "SELECT CURRENT_DATE + 7"') \
  || fail 'Не удалось получить дату из локальной БД'
[[ "$EVENT_DAY" =~ ^[0-9]{4}-[0-9]{2}-[0-9]{2}$ ]] || fail 'БД вернула некорректную дату'
INTENT_BODY=$(python3 - "$EVENT_DAY" <<'PY'
import json, sys
intent = {
    "dates": [sys.argv[1]], "day_types": [], "time_slots": [],
    "category_slugs": ["concerts"], "budget_max_minor": 300000,
    "exclusion_slugs": [],
}
print(json.dumps(intent, separators=(",", ":")))
PY
)
api_call 200 PUT "/rooms/$ROOM_ID/intent/me" "$A_TOKEN" \
  --header 'Content-Type: application/json' --data "$INTENT_BODY" >/dev/null \
  || fail 'Не удалось сохранить условия A'
api_call 200 PUT "/rooms/$ROOM_ID/intent/me" "$B_TOKEN" \
  --header 'Content-Type: application/json' --data "$INTENT_BODY" >/dev/null \
  || fail 'Не удалось сохранить условия B'
unset INTENT_BODY

wait_for_pool() {
  local token="$1" label="$2" response status body error_code deadline remaining request_timeout connect_timeout sleep_seconds attempt
  deadline=$((SECONDS + POOL_TIMEOUT_SECONDS))
  attempt=0
  while (( attempt < POOL_ATTEMPTS && SECONDS < deadline )); do
    attempt=$((attempt + 1))
    remaining=$((deadline - SECONDS))
    request_timeout=5
    (( request_timeout > remaining )) && request_timeout=$remaining
    connect_timeout=2
    (( connect_timeout > request_timeout )) && connect_timeout=$request_timeout
    response=$(curl --silent --connect-timeout "$connect_timeout" --max-time "$request_timeout" \
      --write-out $'\n%{http_code}' --request GET \
      "$API/rooms/$ROOM_ID/events?limit=50" \
      --header "Authorization: Bearer $token") \
      || fail "Сетевой запрос пула для $label завершился ошибкой"
    status=${response##*$'\n'}
    body=${response%$'\n'*}
    if [[ "$status" == 200 ]]; then
      python3 -c 'import json,sys; d=json.load(sys.stdin); assert isinstance(d.get("items"), list) and len(d["items"]) > 0' \
        <<<"$body" || fail "Пул для $label пуст"
      if ! python3 -c 'import json,sys; fixture_id=sys.argv[1]; pool=json.load(sys.stdin); raise SystemExit(0 if any(item.get("event", {}).get("id") == fixture_id for item in pool["items"]) else 1)' \
        "$FIXTURE_EVENT_ID" <<<"$body"
      then
        fail "Fixture-событие отсутствует в готовом пуле для $label"
      fi
      printf '%s' "$body"
      return 0
    fi
    if [[ "$status" == 409 ]]; then
      error_code=$(printf '%s' "$body" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("error",{}).get("code", ""))') \
        || fail "Не удалось разобрать ошибку пула для $label"
      [[ "$error_code" == POOL_NOT_READY ]] || fail "Пул для $label вернул неожиданный HTTP 409"
    else
      fail "Получен неожиданный HTTP-статус при чтении пула для $label"
    fi
    remaining=$((deadline - SECONDS))
    (( remaining <= 0 )) && break
    sleep_seconds=$POOL_RETRY_SECONDS
    (( sleep_seconds > remaining )) && sleep_seconds=$remaining
    sleep "$sleep_seconds"
  done
  fail "Пул для $label не подготовился за $POOL_TIMEOUT_SECONDS секунд после $attempt запросов"
}

POOL_A=$(wait_for_pool "$A_TOKEN" A) || fail 'Не удалось получить пул для A'
POOL_B=$(wait_for_pool "$B_TOKEN" B) || fail 'Не удалось получить пул для B'
python3 -c 'import json,sys; a,b=(json.loads(value) for value in sys.argv[1:]); ids=lambda pool:[item["event"]["id"] for item in pool["items"]]; assert a["pool_version"] == b["pool_version"] and ids(a) == ids(b)' \
  "$POOL_A" "$POOL_B" || fail 'A и B получили разные версии или порядок общего пула'
POOL_VERSION=$(printf '%s' "$POOL_A" | json_field '["pool_version"]') \
  || fail 'Не удалось прочитать версию пула'
unset POOL_B

# Голосуем за одну и ту же fixture-карточку; совпадение ожидается только после голоса B.
VOTE_BODY=$(printf '{"pool_version":%s,"vote":"like"}' "$POOL_VERSION")
A_VOTE=$(api_call 200 PUT "/rooms/$ROOM_ID/events/$FIXTURE_EVENT_ID/vote" "$A_TOKEN" \
  --header 'Content-Type: application/json' --data "$VOTE_BODY") \
  || fail 'Не удалось сохранить голос A'
python3 -c 'import json,sys; assert json.load(sys.stdin).get("match") is None' \
  <<<"$A_VOTE" || fail 'До голоса B сервер неожиданно сообщил совпадение'
B_VOTE=$(api_call 200 PUT "/rooms/$ROOM_ID/events/$FIXTURE_EVENT_ID/vote" "$B_TOKEN" \
  --header 'Content-Type: application/json' --data "$VOTE_BODY") \
  || fail 'Не удалось сохранить голос B'
python3 -c 'import json,sys; d=json.load(sys.stdin); m=d.get("match"); assert m and m["event"]["id"] == sys.argv[1] and len(m["participants"]) == 2' \
  "$FIXTURE_EVENT_ID" <<<"$B_VOTE" \
  || fail 'Совпадение B не содержит fixture-событие и двух участников'
unset VOTE_BODY A_VOTE B_VOTE POOL_A

# Оба участника должны видеть один и тот же сохранённый match в снимке комнаты.
SNAPSHOT_A=$(api_call 200 GET "/rooms/$ROOM_ID" "$A_TOKEN") \
  || fail 'Не удалось прочитать снимок комнаты для A'
SNAPSHOT_B=$(api_call 200 GET "/rooms/$ROOM_ID" "$B_TOKEN") \
  || fail 'Не удалось прочитать снимок комнаты для B'
MATCH_ID_A=$(printf '%s' "$SNAPSHOT_A" | python3 -c 'import json,sys; snapshot=json.load(sys.stdin); match=snapshot.get("match"); assert snapshot["state"] == "matched" and match and match["event_id"] == sys.argv[1] and len(match["participants"]) == 2; print(match["id"])' "$FIXTURE_EVENT_ID") \
  || fail 'Снимок A не содержит совпадение по fixture-событию'
MATCH_ID_B=$(printf '%s' "$SNAPSHOT_B" | python3 -c 'import json,sys; snapshot=json.load(sys.stdin); match=snapshot.get("match"); assert snapshot["state"] == "matched" and match and match["event_id"] == sys.argv[1] and len(match["participants"]) == 2; print(match["id"])' "$FIXTURE_EVENT_ID") \
  || fail 'Снимок B не содержит совпадение по fixture-событию'
[[ "$MATCH_ID_A" == "$MATCH_ID_B" ]] || fail 'Снимки A и B показывают разные совпадения'
unset SNAPSHOT_A SNAPSHOT_B MATCH_ID_A MATCH_ID_B

# Ticket-click возвращает только fixture URL на зарезервированном домене .invalid.
TICKET_BODY=$(python3 - "$ROOM_ID" <<'PY'
import json, sys
print(json.dumps({"source": "match", "room_id": sys.argv[1]}, separators=(",", ":")))
PY
)
TICKET_RESPONSE=$(api_call 200 POST "/events/$FIXTURE_EVENT_ID/ticket-click" "$A_TOKEN" \
  --header 'Content-Type: application/json' --data "$TICKET_BODY") \
  || fail 'Ticket-click для fixture-события завершился ошибкой'
python3 -c 'import json,sys,urllib.parse; value=json.load(sys.stdin)["external_url"]; u=urllib.parse.urlsplit(value); assert u.scheme == "https" and u.hostname == "tickets.example.invalid" and u.path == "/submission/concert-1"' \
  <<<"$TICKET_RESPONSE" || fail 'Ticket-click вернул неожиданный URL'

printf 'PASS: локальные readiness, discovery, две сессии, вступление, общий непустой пул, match в обоих снимках и fixture ticket-click.\n'
```

При успехе печатается только одна строка `PASS`; токены, подписанные данные, приглашение и тела API-ответов не выводятся. Синтетические пользователи и комната остаются в локальной базе до её отдельной очистки.
