#!/usr/bin/env bash
# Проверяем защиту production-маршрутов с тестовыми серверами и доступами.
set -Eeuo pipefail
project_dir=$(cd "$(dirname "$0")/.." && pwd)
fixture_dir=$(mktemp -d)
fixture_container=""
fixture_native_started=""
cleanup() {
  if [[ -n "$fixture_native_started" ]]; then "$NGINX_BIN" -p "$fixture_dir/" -c "$fixture_dir/nginx.conf" -e "$fixture_dir/error.log" -s quit >/dev/null 2>&1 || true; fi
  if [[ -n "$fixture_container" ]]; then docker rm -f "$fixture_container" >/dev/null 2>&1 || true; fi
  rm -rf "$fixture_dir"
}
trap cleanup EXIT
mkdir -p "$fixture_dir/conf" "$fixture_dir/auth" "$fixture_dir/cert" "$fixture_dir/snippets"
cp "$project_dir/deploy/nginx/worknet.team.conf" "$fixture_dir/conf/production.conf"
cp "$project_dir/deploy/nginx/worknet-limits.conf" "$fixture_dir/conf/limits.conf"
cp "$project_dir/deploy/nginx/worknet-proxy.conf" "$fixture_dir/snippets/worknet-proxy.conf"
openssl req -x509 -newkey rsa:2048 -nodes -days 1 -subj '/CN=worknet.team' \
  -keyout "$fixture_dir/cert/privkey.pem" -out "$fixture_dir/cert/fullchain.pem" >/dev/null 2>&1
printf 'fixture:%s\n' "$(openssl passwd -apr1 'fixture-password')" > "$fixture_dir/auth/worknet-analytics.htpasswd"
cat > "$fixture_dir/conf/upstreams.conf" <<'CONFIG'
map "$request_method|$http_x_admin_request|$http_origin" $invalid_admin_mutation {
  default 0;
  ~^(POST|PATCH|DELETE)\|(?!1\|https://worknet\.team$) 1;
}
server {
  listen 8080;
  location = /api/v1/internal/event-sources { return 200 'fixture backend'; }
  location ^~ /api/v1/internal/event-sources/ {
    if ($invalid_admin_mutation) { return 403; }
    return 200 'fixture backend';
  }
  location / { return 200 'fixture backend'; }
}
server { listen 8081; location / { return 200 'fixture frontend'; } }
CONFIG
# NGINX_BIN позволяет проверить ту же production-конфигурацию без Docker.
# Во временной копии меняются только пути к файлам и порты серверов.
if [[ -n "${NGINX_BIN:-}" ]]; then
  fixture_port=18443
  sed -e 's/listen 80;/listen 18082;/' -e 's/\[::\]:80;/[::]:18082;/' \
      -e 's/listen 443 /listen 18443 /' -e 's/\[::\]:443 /[::]:18443 /' \
      -e 's/127.0.0.1:8080/127.0.0.1:18080/g' -e 's/127.0.0.1:8081/127.0.0.1:18081/g' \
      -e "s|/etc/nginx/snippets|$fixture_dir/snippets|g" \
      -e "s|/etc/nginx/auth|$fixture_dir/auth|g" \
      -e "s|/etc/letsencrypt/live/worknet.team|$fixture_dir/cert|g" \
      "$fixture_dir/conf/production.conf" > "$fixture_dir/conf/production.native"
  mv "$fixture_dir/conf/production.native" "$fixture_dir/conf/production.conf"
  sed -e 's/listen 8080;/listen 18080;/' -e 's/listen 8081;/listen 18081;/' \
      "$fixture_dir/conf/upstreams.conf" > "$fixture_dir/conf/upstreams.native"
  mv "$fixture_dir/conf/upstreams.native" "$fixture_dir/conf/upstreams.conf"
  cat > "$fixture_dir/nginx.conf" <<CONFIG
worker_processes 1;
pid $fixture_dir/nginx.pid;
error_log $fixture_dir/error.log;
events { worker_connections 128; }
http {
  access_log off;
  client_body_temp_path $fixture_dir/client_temp;
  proxy_temp_path $fixture_dir/proxy_temp;
  include $fixture_dir/conf/*.conf;
}
CONFIG
  "$NGINX_BIN" -p "$fixture_dir/" -c "$fixture_dir/nginx.conf" -e "$fixture_dir/error.log" -t
  "$NGINX_BIN" -p "$fixture_dir/" -c "$fixture_dir/nginx.conf" -e "$fixture_dir/error.log"
  fixture_native_started=1
else
fixture_container=$(docker run -d --entrypoint nginx -p 127.0.0.1::443 \
  -v "$fixture_dir/conf:/etc/nginx/conf.d:ro" \
  -v "$fixture_dir/snippets:/etc/nginx/snippets:ro" \
  -v "$fixture_dir/auth:/etc/nginx/auth:ro" \
  -v "$fixture_dir/cert:/etc/letsencrypt/live/worknet.team:ro" \
  "${FRONTEND_IMAGE:-max-together-frontend:local}" -g 'daemon off;')
docker exec "$fixture_container" nginx -t
fixture_port=$(docker port "$fixture_container" 443/tcp | sed -n 's/127.0.0.1://p' | head -1)
fi
for attempt in $(seq 1 30); do
  if curl --http1.1 --noproxy '*' -ks --resolve "worknet.team:$fixture_port:127.0.0.1" "https://worknet.team:$fixture_port/" >/dev/null; then break; fi
  sleep 0.2
done
for fixture_path in /internal /internal/event-sources /internal/unregistered /api/v1/internal /api/v1/internal/event-sources /api/v1/internal/event-sources/test /api/v1/internal/event-sources/source-id/domains /api/v1/internal/event-sources/source-id/domains/domain-id /api/v1/internal/unregistered; do
  for fixture_method in GET POST PATCH DELETE; do
    status=$(curl --http1.1 --noproxy '*' -ks --resolve "worknet.team:$fixture_port:127.0.0.1" -X "$fixture_method" -o /dev/null -w '%{http_code}' "https://worknet.team:$fixture_port$fixture_path")
    [[ "$status" == 401 ]] || { echo "Unguarded internal namespace: $fixture_method $fixture_path returned $status" >&2; exit 1; }
  done
done
for mutation_headers in none origin_only admin_only; do
  header_args=(-H 'Accept: application/json')
  case "$mutation_headers" in
    origin_only) header_args=(-H 'Origin: https://worknet.team') ;;
    admin_only) header_args=(-H 'X-Admin-Request: 1') ;;
  esac
  status=$(curl --http1.1 --noproxy '*' -ks --resolve "worknet.team:$fixture_port:127.0.0.1" -u 'fixture:fixture-password' "${header_args[@]}" -X POST -o /dev/null -w '%{http_code}' "https://worknet.team:$fixture_port/api/v1/internal/event-sources/source-id/domains")
  [[ "$status" == 403 ]] || { echo "Mutation without both same-origin headers returned $status ($mutation_headers)" >&2; exit 1; }
done
status=$(curl --http1.1 --noproxy '*' -ks --resolve "worknet.team:$fixture_port:127.0.0.1" -u 'fixture:fixture-password' -H 'Origin: https://worknet.team' -H 'X-Admin-Request: 1' -X POST -o /dev/null -w '%{http_code}' "https://worknet.team:$fixture_port/api/v1/internal/event-sources/source-id/domains")
[[ "$status" == 200 ]] || { echo "Mutation with same-origin headers returned $status" >&2; exit 1; }
headers=$(curl --http1.1 --noproxy '*' -ks --resolve "worknet.team:$fixture_port:127.0.0.1" -u 'fixture:fixture-password' -D - "https://worknet.team:$fixture_port/api/v1/internal/event-sources")
[[ "$headers" == *'200 OK'* && "$headers" == *'Content-Security-Policy:'* && "$headers" == *'X-Content-Type-Options: nosniff'* ]] || { echo 'Authenticated response or inherited security headers missing' >&2; exit 1; }
public_image_headers=$(curl --http1.1 --noproxy '*' -ks --resolve "worknet.team:$fixture_port:127.0.0.1" -D - "https://worknet.team:$fixture_port/api/v1/event-images/550e8400-e29b-41d4-a716-446655440000/content")
[[ "$public_image_headers" == *'200 OK'* && "$public_image_headers" == *'Content-Security-Policy:'* && "$public_image_headers" == *'X-Content-Type-Options: nosniff'* ]] || { echo 'Public same-origin image API or inherited security headers missing' >&2; exit 1; }
[[ "$public_image_headers" == *"img-src 'self'"* && "$public_image_headers" != *'img-src https:'* && "$public_image_headers" != *'images.example.test'* ]] || { echo 'CSP must allow same-origin images without broad HTTPS or Generic domains' >&2; exit 1; }
echo 'Production Nginx internal guards and same-origin image/CSP smoke passed'
