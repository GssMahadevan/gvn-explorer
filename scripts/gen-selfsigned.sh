#!/usr/bin/env bash
# gen-selfsigned.sh — make a self-signed server certificate for the
# server's -tls-cert/-tls-key flags (the quick path without any file is
# the server's own -tls-self flag, which generates one in memory).
#
# The certificate covers localhost, the machine hostname, 127.0.0.1 and
# ::1, and is its own CA, so clients can trust it explicitly:
#   curl --cacert tls/server.crt https://localhost:8080/healthz
# instead of -k. Browsers still warn until you add it to the system trust
# store (macOS hint printed below).
#
# Usage: ./scripts/gen-selfsigned.sh [outdir=tls] [days=825]
# 825 days is Apple's guidance for locally-trusted certs.
set -euo pipefail
cd "$(dirname "$0")/.."

OUT="${1:-tls}"
DAYS="${2:-825}"
HOST="$(hostname 2>/dev/null || echo localhost)"

command -v openssl >/dev/null || { echo "openssl not found" >&2; exit 1; }
mkdir -p "$OUT"

# A config file (not -addext) keeps this portable across OpenSSL and the
# LibreSSL that ships with macOS.
CFG="$(mktemp)"
trap 'rm -f "$CFG"' EXIT
cat >"$CFG" <<CFG
[req]
distinguished_name = dn
x509_extensions    = v3
prompt             = no
[dn]
CN = gvn-explorer
O  = gvn-explorer
[v3]
basicConstraints   = critical, CA:true
keyUsage           = critical, digitalSignature, keyCertSign
extendedKeyUsage   = serverAuth
subjectAltName     = @alt
[alt]
DNS.1 = localhost
DNS.2 = $HOST
IP.1  = 127.0.0.1
IP.2  = ::1
CFG

openssl req -x509 -newkey rsa:2048 -sha256 -days "$DAYS" -nodes \
  -config "$CFG" -keyout "$OUT/server.key" -out "$OUT/server.crt" 2>/dev/null
chmod 600 "$OUT/server.key"

echo "wrote $OUT/server.crt + $OUT/server.key (RSA 2048, $DAYS days, SAN: localhost, $HOST, 127.0.0.1, ::1)"
echo
echo "serve with:"
echo "  ./bin/server -tls-cert $OUT/server.crt -tls-key $OUT/server.key"
echo "verify without -k:"
echo "  curl --cacert $OUT/server.crt https://localhost:8080/healthz"
echo "trust in macOS (optional):"
echo "  sudo security add-trusted-cert -d -r trustRoot -k /Library/Keychains/System.keychain $OUT/server.crt"
