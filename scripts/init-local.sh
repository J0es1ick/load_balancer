#!/usr/bin/env sh
set -eu

repository_root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
environment_path="$repository_root/.env"
secret_directory="$repository_root/deploy/secrets"
secret_path="$secret_directory/admin_token.txt"
balancer_public_port="${BALANCER_PUBLIC_PORT:-8080}"
frontend_port="${FRONTEND_PORT:-3000}"
edge_https_port="${EDGE_HTTPS_PORT:-8443}"

admin_token=""
grafana_password=""
if [ -f "$environment_path" ]; then
  admin_token="$(sed -n 's/^BALANCER_ADMIN_TOKEN=//p' "$environment_path" | head -n 1)"
  grafana_password="$(sed -n 's/^GRAFANA_ADMIN_PASSWORD=//p' "$environment_path" | head -n 1)"
fi

[ -n "$admin_token" ] || admin_token="$(openssl rand -hex 32)"
[ -n "$grafana_password" ] || grafana_password="$(openssl rand -hex 32)"

umask 077
mkdir -p "$secret_directory"
printf '%s\n' \
  "BALANCER_ADMIN_TOKEN=$admin_token" \
  "GRAFANA_ADMIN_USER=admin" \
  "GRAFANA_ADMIN_PASSWORD=$grafana_password" \
  "POSTGRES_PASSWORD=local-postgres-not-enabled" \
  "BALANCER_PUBLIC_PORT=$balancer_public_port" \
  "FRONTEND_PORT=$frontend_port" \
  "EDGE_HTTPS_PORT=$edge_https_port" \
  "VITE_PUBLIC_URL=http://localhost:$balancer_public_port/" > "$environment_path"
printf '%s' "$admin_token" > "$secret_path"
printf '%s\n' 'Local credentials initialized in .env and deploy/secrets/.'
