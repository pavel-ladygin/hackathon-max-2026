#!/usr/bin/env bash
set -Eeuo pipefail

SCRIPT_DIR=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
test "$(id -u)" -eq 0 || { echo "run as root" >&2; exit 1; }

certbot certonly --webroot --webroot-path /var/www/certbot \
  -d worknet.team -d www.worknet.team
install -m 0644 "$SCRIPT_DIR/nginx/worknet.team.conf" /etc/nginx/sites-available/worknet.team.conf
rm -f /etc/nginx/sites-enabled/worknet-bootstrap.conf
ln -sfn /etc/nginx/sites-available/worknet.team.conf /etc/nginx/sites-enabled/worknet.team.conf
nginx -t
systemctl reload nginx
systemctl enable --now certbot.timer
certbot renew --dry-run
