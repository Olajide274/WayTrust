#!/usr/bin/env bash
# Starts Postgres (Docker) and the Go backend. Run the frontend in a separate terminal.
set -e
cd "$(dirname "$0")"

# Secrets live in .dev.env (gitignored), not in this file
if [ -f .dev.env ]; then set -a; . ./.dev.env; set +a; fi

docker start nurtw-pg >/dev/null 2>&1 || {
  docker run -d --name nurtw-pg --restart unless-stopped \
    -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=nurtw_escrow \
    -p 5432:5432 postgres:16 >/dev/null
}
docker update --restart unless-stopped nurtw-pg >/dev/null

echo "Waiting for Postgres..."
until docker exec nurtw-pg pg_isready -U postgres >/dev/null 2>&1; do sleep 1; done

export JWT_SECRET="${JWT_SECRET:-nurtw-local-dev-secret-change-in-production}"
export DB_PASSWORD="${DB_PASSWORD:-postgres}"

echo "Starting backend on :8080..."
go run ./cmd/server
