#!/usr/bin/env bash
set -Eeuo pipefail

APP_DIR=${APP_DIR:-/opt/worknet}
exec "$APP_DIR/deploy.sh" "$@"
