#!/usr/bin/env bash
set -Eeuo pipefail

APP_DIR=${APP_DIR:-/opt/worknet}
COMPOSE_FILE=${COMPOSE_FILE:-compose.production.yaml}
ENV_FILE=${ENV_FILE:-.env.production}
RELEASE_FILE=${RELEASE_FILE:-.release.env}
BACKUP_DIR=${BACKUP_DIR:-/var/backups/worknet}
PUBLIC_URL=${PUBLIC_URL:-https://worknet.team}
IMAGE_PREFIX=${IMAGE_PREFIX:-ghcr.io/pavel-ladygin/hackathon-max-2026}

test "$#" -eq 1 || { echo "usage: $0 <40-character-commit-sha>" >&2; exit 2; }
release_sha=$1
[[ "$release_sha" =~ ^[0-9a-f]{40}$ ]] || { echo "invalid commit SHA" >&2; exit 2; }
test -s /etc/nginx/auth/worknet-analytics.htpasswd || { echo "missing or empty /etc/nginx/auth/worknet-analytics.htpasswd" >&2; exit 1; }

analytics_auth_is_enforced() {
  local path status
  for path in \
    "/internal/analytics" \
    "/api/v1/internal/analytics/dashboard?days=7"; do
    status=$(curl --silent --output /dev/null --write-out '%{http_code}' \
      --connect-timeout 5 --max-time 15 "$PUBLIC_URL$path" || true)
    if [[ "$status" != "401" ]]; then
      echo "analytics access guard failed: unauthenticated $PUBLIC_URL$path returned HTTP ${status:-unknown}, expected 401" >&2
      return 1
    fi
  done
}

# Fail before migrations, backups, or image changes if the public endpoints are
# reachable without the Nginx Basic Auth challenge.
analytics_auth_is_enforced || {
  echo "deployment aborted; configure Nginx Basic Auth for both analytics paths" >&2
  exit 1
}

cd "$APP_DIR"
test -f "$ENV_FILE" || { echo "missing $APP_DIR/$ENV_FILE" >&2; exit 1; }

exec 9>"$APP_DIR/.deploy.lock"
flock -n 9 || { echo "another deployment is running" >&2; exit 1; }

previous_sha=""
if test -f "$RELEASE_FILE"; then
  # shellcheck disable=SC1090
  source "$RELEASE_FILE"
  previous_sha=${RELEASE_SHA:-}
fi

compose=(docker compose --env-file "$ENV_FILE" -f "$COMPOSE_FILE")
export BACKEND_IMAGE="$IMAGE_PREFIX/backend:$release_sha"
export FRONTEND_IMAGE="$IMAGE_PREFIX/frontend:$release_sha"
"${compose[@]}" config --quiet

mkdir -p "$BACKUP_DIR"
backup="$BACKUP_DIR/postgres-$(date -u +%Y%m%dT%H%M%SZ).sql.gz"
if "${compose[@]}" ps --status running postgres --format '{{.Names}}' | grep -q .; then
  "${compose[@]}" exec -T postgres sh -c 'pg_dump -U "$POSTGRES_USER" "$POSTGRES_DB"' | gzip >"$backup"
  find "$BACKUP_DIR" -type f -name 'postgres-*.sql.gz' -printf '%T@ %p\n' | sort -nr | awk 'NR > 7 {print $2}' | xargs -r rm -f
fi

"${compose[@]}" pull
"${compose[@]}" run --rm migrate
"${compose[@]}" up -d postgres backend frontend event-sync timepad-image-recovery

healthy=false
for attempt in $(seq 1 30); do
  if curl --fail --silent --show-error "$PUBLIC_URL/api/v1/health/ready" >/dev/null \
    && curl --fail --silent --show-error "$PUBLIC_URL/" >/dev/null \
    && curl --fail --silent --show-error "$PUBLIC_URL/open/nonexistent-spa-route" >/dev/null \
    && analytics_auth_is_enforced; then
    healthy=true
    break
  fi
  sleep 2
done

if "$healthy"; then
  umask 077
  printf 'RELEASE_SHA=%s\n' "$release_sha" >"$RELEASE_FILE"
  echo "deployed $release_sha"
  exit 0
fi

echo "smoke checks failed" >&2
if [[ "$previous_sha" =~ ^[0-9a-f]{40}$ ]]; then
  echo "restoring application images for $previous_sha" >&2
  export BACKEND_IMAGE="$IMAGE_PREFIX/backend:$previous_sha"
  export FRONTEND_IMAGE="$IMAGE_PREFIX/frontend:$previous_sha"
  "${compose[@]}" pull
  "${compose[@]}" up -d postgres backend frontend event-sync timepad-image-recovery || true
fi
exit 1
