# docker-compose example

A three-service example workspace for Nexus:

| Service | Port | Description |
|---------|------|-------------|
| `spa`   | 8080 | nginx serving a static SPA — proxies `/api/` to the API |
| `api`   | 3000 | Node.js Express REST API backed by PostgreSQL |
| `db`    | 5432 | PostgreSQL 16 with a seeded `items` table |

## Local dev (outside Nexus)

```bash
docker compose up --build
open http://localhost:8080
```

## Inside a Nexus workspace

The `Nexusfile` defines the full lifecycle:

```bash
nexus workspace start docker-compose-example
# SPA is reachable at the forwarded port shown by `nexus spotlight list`
```

For exported bundles, `docker compose build` is performed during one-time bake,
and runtime startup keeps `docker compose up` in the foreground for fast starts.
