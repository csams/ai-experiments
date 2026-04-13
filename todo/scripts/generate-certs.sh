#!/usr/bin/env bash
set -eu

# generate-certs.sh — Generate a local CA and per-service TLS serving certificates.
#
# Usage:
#   ./scripts/generate-certs.sh [--force] [CERT_DIR]
#
# CERT_DIR defaults to ~/.config/todo/certs.
# Without --force, existing files are skipped (idempotent).
# With --force, serving certs are regenerated but the CA is preserved.

if ! command -v openssl >/dev/null 2>&1; then
    echo "ERROR: openssl is required but not found in PATH" >&2
    exit 1
fi

FORCE=0
CERT_DIR=""

for arg in "$@"; do
    case "$arg" in
        --force) FORCE=1 ;;
        *)       CERT_DIR="$arg" ;;
    esac
done

CERT_DIR="${CERT_DIR:-$HOME/.config/todo/certs}"

CA_KEY="$CERT_DIR/ca.key"
CA_CRT="$CERT_DIR/ca.crt"

CA_DAYS=3650    # 10 years
SVC_DAYS=365    # 1 year

# Services and their SANs
SERVICES="postgres mcp"
postgres_SANS="DNS:todo-postgres,DNS:postgres,DNS:localhost,IP:127.0.0.1"
mcp_SANS="DNS:todo-mcp,DNS:localhost,DNS:host.docker.internal,IP:127.0.0.1"
# Append environment-specific SANs (e.g., Tailscale hostname, LAN IP):
#   MCP_EXTRA_SANS="DNS:myhost.tail12345.ts.net,IP:192.168.1.100" make certs
if [ -n "${MCP_EXTRA_SANS:-}" ]; then
    mcp_SANS="$mcp_SANS,$MCP_EXTRA_SANS"
fi

# --- helpers ---

log() { printf "  %-10s %s\n" "$1" "$2"; }

generate_ca() {
    if [ -f "$CA_KEY" ] && [ -f "$CA_CRT" ]; then
        log "[skip]" "CA already exists: $CA_CRT"
        return
    fi

    log "[create]" "CA key: $CA_KEY"
    openssl genrsa -out "$CA_KEY" 4096
    chmod 600 "$CA_KEY"

    log "[create]" "CA cert: $CA_CRT (valid ${CA_DAYS}d)"
    openssl req -x509 -new -nodes \
        -key "$CA_KEY" \
        -sha256 \
        -days "$CA_DAYS" \
        -subj "/CN=Todo Local CA" \
        -out "$CA_CRT"
    chmod 644 "$CA_CRT"
}

generate_service_cert() {
    svc="$1"
    sans="$2"
    svc_dir="$CERT_DIR/$svc"
    svc_key="$svc_dir/server.key"
    svc_crt="$svc_dir/server.crt"
    svc_csr="$svc_dir/server.csr"

    mkdir -p "$svc_dir"

    if [ "$FORCE" -eq 0 ] && [ -f "$svc_key" ] && [ -f "$svc_crt" ]; then
        log "[skip]" "$svc: certs already exist"
        return
    fi

    log "[create]" "$svc: key $svc_key"
    openssl genrsa -out "$svc_key.tmp" 2048
    chmod 600 "$svc_key.tmp"

    log "[create]" "$svc: CSR"
    openssl req -new \
        -key "$svc_key.tmp" \
        -subj "/CN=$svc" \
        -out "$svc_csr"

    log "[create]" "$svc: cert $svc_crt (valid ${SVC_DAYS}d)"
    openssl x509 -req \
        -in "$svc_csr" \
        -CA "$CA_CRT" \
        -CAkey "$CA_KEY" \
        -CAcreateserial \
        -days "$SVC_DAYS" \
        -sha256 \
        -extfile <(printf "subjectAltName=%s\nbasicConstraints=CA:FALSE\nkeyUsage=digitalSignature,keyEncipherment\nextendedKeyUsage=serverAuth\n" "$sans") \
        -out "$svc_crt.tmp"
    chmod 644 "$svc_crt.tmp"

    # Atomically replace key and cert so running services never see a mismatched pair
    mv "$svc_key.tmp" "$svc_key"
    mv "$svc_crt.tmp" "$svc_crt"

    rm -f "$svc_csr"
}

# --- main ---

echo "Generating TLS certificates in $CERT_DIR"
mkdir -p "$CERT_DIR"
chmod 700 "$CERT_DIR"

generate_ca

for svc in $SERVICES; do
    var="${svc}_SANS"
    sans="${!var}"
    generate_service_cert "$svc" "$sans"
done

# Clean up serial file left by openssl
rm -f "$CERT_DIR/ca.srl"

echo "Done."
