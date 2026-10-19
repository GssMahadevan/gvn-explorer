# swarm deployment (planned, placeholder)

Not written yet. The compose file in `deployments/docker/` is close to
swarm-compatible already; to deploy here, extract the `app` service into
`stack.yml`, swap the bind mounts for named volumes (`fx-root`, `fx-db`)
so swarm keeps them across node placement, and pin a replica of 1 while
the database is SQLite. Tracked along with the Postgres work: after that
cutover the app can run with replicas > 1.
