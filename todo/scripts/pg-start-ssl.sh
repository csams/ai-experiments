#!/bin/sh
set -e

# pg-start-ssl.sh — Entrypoint wrapper for the PostgreSQL container.
#
# Copies mounted TLS certs to a postgres-writable location with correct
# ownership, then exec's the standard docker-entrypoint.sh with SSL flags.
#
# This script runs as root (the default in the postgres image). The standard
# entrypoint later drops to the postgres user (uid 999) via gosu.

CERT_SRC=/etc/ssl/todo
CERT_DST=/var/run/postgresql/certs

mkdir -p "$CERT_DST"
cp "$CERT_SRC/server.crt" "$CERT_DST/server.crt"
cp "$CERT_SRC/server.key" "$CERT_DST/server.key"
chown -R 999:999 "$CERT_DST"
chmod 600 "$CERT_DST/server.key"
chmod 644 "$CERT_DST/server.crt"

exec docker-entrypoint.sh "$@" \
    -c ssl=on \
    -c ssl_cert_file="$CERT_DST/server.crt" \
    -c ssl_key_file="$CERT_DST/server.key"
