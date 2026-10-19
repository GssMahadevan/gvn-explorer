#!/usr/bin/env bash
# Build docs/demo-standalone.mp4 and docs/demo-docker.mp4: starts the REAL
# app for each scenario (the release binary for one, the published ghcr
# image for the other), records scripted GUI footage + terminal + narration
# via record.js, then tears everything down. Requires: docker, ffmpeg,
# macOS `say`, a built dist/ (make dist) and frontend deps for puppeteer.
set -eu
cd "$(dirname "$0")/../.."
T=/tmp/fx-demo
BIN=$(ls dist/gvn-explorer-*-darwin-arm64 2>/dev/null | tail -1)
[ -n "$BIN" ] || { echo "no darwin-arm64 binary in dist/ — run: make dist"; exit 1; }

seed_files() { # seed_files <rootdir>
  local r=$1
  mkdir -p "$r/projects" "$r/photos"
  printf '# Release notes\n\n## 0.1\n- per-path permissions with inheritance\n- draft + publish editing\n- multi-arch container, single-file binaries\n\nOpen *any* file — this view is rendered markdown.\n' > "$r/projects/release-notes.md"
  printf 'apiVersion: v1\nkind: ConfigMap\nmetadata:\n  name: explorer # comments survive editing\n' > "$r/projects/deploy.yml"
  printf 'buy more disks\n' > "$r/notes.md"
}

wait_up() { for _ in $(seq 1 60); do curl -sf "$1/healthz" >/dev/null && return 0; sleep 0.5; done; echo "app at $1 never came up"; return 1; }

# ---------- standalone: the actual release binary, embedded GUI ----------
# Free the demo ports first: pkill matches our (absolute-path) command
# line, and the lsof kill catches anything else LISTENing on the port.
pkill -f "$T/standalone-run/gvn-explorer" 2>/dev/null || true
lsof -ti tcp:8186 -sTCP:LISTEN | xargs kill 2>/dev/null || true
sleep 0.5
rm -rf $T/standalone-run; mkdir -p $T/standalone-run
cp "$BIN" $T/standalone-run/gvn-explorer
seed_files $T/standalone-run/data
(cd $T/standalone-run && ADDR=:8186 ROOT=$T/standalone-run/data DB_PATH=$T/standalone-run/perms.db exec "$T/standalone-run/gvn-explorer" >server.log 2>&1) &
trap 'pkill -f "$T/standalone-run/gvn-explorer" 2>/dev/null || true; docker rm -f fx-demo >/dev/null 2>&1 || true' EXIT
wait_up http://localhost:8186
grep -q 'address already in use' $T/standalone-run/server.log && { echo "port 8186 busy — stale demo server?"; exit 1; }
grep -q '(embedded)' $T/standalone-run/server.log && echo "standalone server uses the embedded GUI ✔"
FX_DEMO_URL=http://localhost:8186 node scripts/demo/record.js standalone
pkill -f "$T/standalone-run/gvn-explorer" 2>/dev/null || true

# ---------- docker: the published multi-arch image ----------
docker rm -f fx-demo >/dev/null 2>&1 || true
rm -rf $T/docker-root $T/docker-state; seed_files $T/docker-root; mkdir -p $T/docker-state
docker run -d --name fx-demo --pull always -p 8187:8080 \
  -v $T/docker-root:/data/root -v $T/docker-state:/data \
  ghcr.io/gssmahadevan/gvn-explorer:latest >/dev/null
wait_up http://localhost:8187
FX_DEMO_URL=http://localhost:8187 node scripts/demo/record.js docker
docker rm -f fx-demo >/dev/null

echo "done:"; ls -lh docs/demo-*.mp4
