#!/usr/bin/env sh
set -eu

repository_root="$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)"
environment_path="$repository_root/.env"
secret_directory="$repository_root/deploy/secrets"
admin_secret_path="$secret_directory/admin_token.txt"
metrics_secret_path="$secret_directory/metrics_token.txt"

public_port_explicit=false
frontend_port_explicit=false
edge_port_explicit=false
[ "${BALANCER_PUBLIC_PORT+x}" = x ] && public_port_explicit=true
[ "${FRONTEND_PORT+x}" = x ] && frontend_port_explicit=true
[ "${EDGE_HTTPS_PORT+x}" = x ] && edge_port_explicit=true
requested_public_port="${BALANCER_PUBLIC_PORT:-8080}"
requested_frontend_port="${FRONTEND_PORT:-3000}"
requested_edge_port="${EDGE_HTTPS_PORT:-8443}"

umask 077
mkdir -p "$secret_directory"
touch "$environment_path"

read_value() {
  sed -n "s/^$1=//p" "$environment_path" | head -n 1
}

has_key() {
  grep -q "^$1=" "$environment_path"
}

write_value() {
  key="$1"
  value="$2"
  replace="${3:-false}"
  current="$(read_value "$key")"
  if has_key "$key"; then
    if [ "$replace" = true ] || [ -z "$current" ]; then
      temporary="$(mktemp "${environment_path}.tmp.XXXXXX")"
      awk -v key="$key" -v value="$value" '
        index($0, key "=") == 1 && !updated { print key "=" value; updated=1; next }
        { print }
      ' "$environment_path" > "$temporary"
      mv "$temporary" "$environment_path"
      printf '%s' "$value"
    else
      printf '%s' "$current"
    fi
  else
    temporary="$(mktemp "${environment_path}.tmp.XXXXXX")"
    awk -v key="$key" -v value="$value" '{ print } END { print key "=" value }' "$environment_path" > "$temporary"
    mv "$temporary" "$environment_path"
    printf '%s' "$value"
  fi
}

random_secret() {
  openssl rand -hex 32
}

ensure_secret() {
  key="$1"
  current="$(read_value "$key")"
  if [ -n "$current" ]; then
    printf '%s' "$current"
  else
    write_value "$key" "$(random_secret)"
  fi
}

admin_token="$(ensure_secret BALANCER_ADMIN_TOKEN)"
ensure_secret BALANCER_VIEWER_TOKEN >/dev/null
ensure_secret BALANCER_OPERATOR_TOKEN >/dev/null
ensure_secret BALANCER_DISCOVERY_TOKEN >/dev/null
metrics_token="$(ensure_secret BALANCER_METRICS_TOKEN)"
write_value GRAFANA_ADMIN_USER admin >/dev/null
ensure_secret GRAFANA_ADMIN_PASSWORD >/dev/null
ensure_secret POSTGRES_PASSWORD >/dev/null

effective_public_port="$(write_value BALANCER_PUBLIC_PORT "$requested_public_port" "$public_port_explicit")"
write_value FRONTEND_PORT "$requested_frontend_port" "$frontend_port_explicit" >/dev/null
write_value EDGE_HTTPS_PORT "$requested_edge_port" "$edge_port_explicit" >/dev/null
write_value VITE_PUBLIC_URL "http://localhost:$effective_public_port/" >/dev/null

printf '%s' "$admin_token" > "$admin_secret_path"
printf '%s' "$metrics_token" > "$metrics_secret_path"
printf '%s\n' 'Missing local credentials were initialized; existing .env values were preserved.'
