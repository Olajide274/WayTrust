#!/usr/bin/env bash
# Starts Postgres (Docker, data in .pgdata so it survives restarts) and the Go backend.
set -e
cd "$(dirname "$0")"
if [ -f .dev.env ]; then set -a; . ./.dev.env; set +a; fi

mkdir -p .pgdata
if ! docker start nurtw-pg >/dev/null 2>&1; then
  docker rm -f nurtw-pg >/dev/null 2>&1 || true
  docker run -d --name nurtw-pg \
    -v "$PWD/.pgdata":/var/lib/postgresql/data \
    -e PGDATA=/var/lib/postgresql/data/pgdata \
    -e POSTGRES_PASSWORD=postgres -e POSTGRES_DB=nurtw_escrow \
    -p 5432:5432 postgres:16 >/dev/null
fi

echo "Waiting for Postgres..."
until docker exec nurtw-pg pg_isready -h 127.0.0.1 -U postgres >/dev/null 2>&1; do sleep 1; done

HAS=$(docker exec nurtw-pg psql -U postgres -d nurtw_escrow -tAc "select to_regclass('public.agents') is not null")
if [ "$HAS" != "t" ]; then
  echo "Empty database, applying migrations..."
  for f in migrations/migrations/000001_init_escrow_ledger.up.sql \
           migrations/000002_corridor_policies.up.sql \
           migrations/migrations/000003_parks.up.sql \
           migrations/migrations/000004_agent_admin.up.sql \
           migrations/migrations/000005_wallet_credits.up.sql \
           migrations/migrations/000006_unclaimed_transit_timeout.up.sql \
           migrations/migrations/000007_receiver_refusal_reports.up.sql; do
    echo "  $f"
    docker exec -i nurtw-pg psql -v ON_ERROR_STOP=1 -q -U postgres -d nurtw_escrow < "$f"
  done
fi

export JWT_SECRET="${JWT_SECRET:-nurtw-local-dev-secret-change-in-production}"
export DB_PASSWORD="${DB_PASSWORD:-postgres}"
echo "Starting backend on :8080..."
go run ./cmd/server
