#!/bin/sh
set -eu

test "$(id -u)" -ne 0
test -x /docker-entrypoint.d/10-admin-token.envsh

export BALANCER_ADMIN_TOKEN=container-startup-test-token
unset BALANCER_ADMIN_TOKEN_FILE BALANCER_MANAGEMENT_TLS_ENABLED
/docker-entrypoint.sh nginx -t
test -f /etc/nginx/conf.d/management-upstream-tls.inc
grep -Fq 'proxy_pass http://balancer:9090;' /etc/nginx/conf.d/default.conf
grep -Fq 'proxy_set_header Authorization "Bearer container-startup-test-token";' /etc/nginx/conf.d/default.conf

printf '%s' 'container-startup-file-token' > /tmp/admin-token
chmod 0600 /tmp/admin-token
export BALANCER_ADMIN_TOKEN_FILE=/tmp/admin-token
/docker-entrypoint.sh nginx -t
grep -Fq 'proxy_set_header Authorization "Bearer container-startup-file-token";' /etc/nginx/conf.d/default.conf

if BALANCER_MANAGEMENT_TLS_ENABLED=true /docker-entrypoint.sh nginx -t > /tmp/missing-tls.log 2>&1; then
    echo 'Management TLS started without the required certificate files' >&2
    exit 1
fi
grep -Fq 'management mTLS file is missing, unreadable, or has an unsafe path' /tmp/missing-tls.log

echo 'Frontend startup contract passed'
