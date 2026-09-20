#!/usr/bin/env bash
set -Eeuo pipefail

DEPLOY_USER=${DEPLOY_USER:-deploy}
APP_DIR=${APP_DIR:-/opt/worknet}
SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
REPO_DIR=$(cd -- "$SCRIPT_DIR/.." && pwd)

test "$(id -u)" -eq 0 || { echo "run as root" >&2; exit 1; }

apt-get update
apt-get install -y ca-certificates curl docker.io docker-compose-v2 nginx certbot ufw
if ! id "$DEPLOY_USER" >/dev/null 2>&1; then
  adduser --disabled-password --gecos '' "$DEPLOY_USER"
fi
install -d -o "$DEPLOY_USER" -g "$DEPLOY_USER" -m 0750 "$APP_DIR"
install -d -m 0755 /var/www/certbot /etc/nginx/snippets
install -d -o "$DEPLOY_USER" -g "$DEPLOY_USER" -m 0750 /var/backups/worknet
usermod -aG docker "$DEPLOY_USER"

install -o "$DEPLOY_USER" -g "$DEPLOY_USER" -m 0640 "$REPO_DIR/compose.production.yaml" "$APP_DIR/compose.production.yaml"
install -o "$DEPLOY_USER" -g "$DEPLOY_USER" -m 0750 "$SCRIPT_DIR/deploy.sh" "$APP_DIR/deploy.sh"
install -o "$DEPLOY_USER" -g "$DEPLOY_USER" -m 0750 "$SCRIPT_DIR/rollback.sh" "$APP_DIR/rollback.sh"
install -m 0644 "$SCRIPT_DIR/nginx/worknet-limits.conf" /etc/nginx/conf.d/worknet-limits.conf
install -m 0644 "$SCRIPT_DIR/nginx/worknet-proxy.conf" /etc/nginx/snippets/worknet-proxy.conf
install -m 0644 "$SCRIPT_DIR/nginx/worknet-bootstrap.conf" /etc/nginx/sites-available/worknet-bootstrap.conf
rm -f /etc/nginx/sites-enabled/default /etc/nginx/sites-enabled/worknet.team.conf
ln -sfn /etc/nginx/sites-available/worknet-bootstrap.conf /etc/nginx/sites-enabled/worknet-bootstrap.conf
nginx -t
systemctl reload nginx

ufw allow OpenSSH
ufw allow 80/tcp
ufw allow 443/tcp
ufw --force enable

echo "Create $APP_DIR/.env.production as $DEPLOY_USER with mode 0600."
echo "After DNS points to this host, run as root: $SCRIPT_DIR/enable-https.sh"
