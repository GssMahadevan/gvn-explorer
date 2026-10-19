# k8s deployment (planned)

Placeholder following the repo-wide deployments layout. The container
image from `deployments/docker/Dockerfile` is already k8s-ready:
one stateless binary, config via env, everything mutable under `/data`.

Sketch of what goes here:

- `deployment.yaml` — 1 replica (single-writer SQLite), `env` from a
  ConfigMap (`ADDR`, `ROOT`, `DB_PATH`, `WEB_DIR`, `LOG_*`), `ADMIN`
  bootstrap from a Secret, liveness/readiness probes on `GET /`.
- two `PersistentVolumeClaim`s mounted at `/data/root` (file tree) and
  `/data` (SQLite + drafts). Not horizontally scalable until the DB
  moves to Postgres — then replicas > 1 and a `StatefulSet`/managed
  Postgres replace the SQLite PVC.
- `service.yaml` (ClusterIP) + optional Ingress; `secret.yaml` template
  for the bootstrap admin.

`docker compose` in `deployments/docker/` remains the reference for local
runs until these manifests are written.
