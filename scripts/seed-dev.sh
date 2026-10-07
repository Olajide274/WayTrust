#!/usr/bin/env bash
# DEV ONLY: seeds a local database with test clerks, parks and corridor policies.
# Needs the backend running (./start-dev.sh). Safe to re-run.
set -euo pipefail

API="${API_URL:-http://localhost:8080}"
PASS="AgentPass123"          # dev-only password for the test accounts
ADMIN_PHONE="08031234567"
CLERK_PHONE="08039999999"

if [ "${APP_ENV:-}" = "production" ]; then
  echo "Refusing to run: APP_ENV=production"; exit 1
fi
case "$API" in
  http://localhost*|http://127.0.0.1*) ;;
  *) echo "Refusing to run against $API (dev seed is local-only)"; exit 1 ;;
esac

sql() { docker exec -i nurtw-pg psql -v ON_ERROR_STOP=1 -q -U postgres -d nurtw_escrow "$@"; }
post() { curl -s -X POST "$API$1" -H "Content-Type: application/json" "${@:2}"; }
jget() { python3 -c "import sys,json; print(json.load(sys.stdin).get('$1',''))"; }

curl -sf "$API/api/v1/parks" >/dev/null || { echo "Backend not reachable at $API. Run ./start-dev.sh first."; exit 1; }

echo "1/5 Registering test agents (existing ones are skipped)..."
post /api/v1/agent/register -d "{\"full_name\":\"Test Clerk\",\"phone_number\":\"$ADMIN_PHONE\",\"park_id\":\"KAD-01\",\"password\":\"$PASS\"}" >/dev/null
post /api/v1/agent/register -d "{\"full_name\":\"Plain Clerk\",\"phone_number\":\"$CLERK_PHONE\",\"park_id\":\"KAD-01\",\"password\":\"$PASS\"}" >/dev/null

echo "2/5 Bootstrapping the first admin and starting balances (SQL; no endpoint can do this)..."
sql -c "update agents set is_admin = true, wallet_balance_kobo = 5000000 where phone_number = '$ADMIN_PHONE';"
sql -c "update agents set wallet_balance_kobo = 1000000 where phone_number = '$CLERK_PHONE';"

echo "3/5 Logging in as admin..."
TOKEN=$(post /api/v1/agent/login -d "{\"phone_number\":\"$ADMIN_PHONE\",\"password\":\"$PASS\"}" | jget token)
[ -n "$TOKEN" ] || { echo "Admin login failed"; exit 1; }

echo "4/5 Creating parks (existing ones are skipped)..."
post /api/v1/parks/create -H "Authorization: Bearer $TOKEN" \
  -d '{"id":"KAD-01","name":"Kaduna Central Park","city":"Kaduna","state":"Kaduna"}' >/dev/null
post /api/v1/parks/create -H "Authorization: Bearer $TOKEN" \
  -d '{"id":"ABJ-01","name":"Jabi Motor Park","city":"Abuja","state":"FCT"}' >/dev/null

echo "5/5 Corridor policies..."
sql -c "insert into corridor_policies
 (id, corridor_name, origin_park_id, destination_park_id, model, threshold_kobo,
  platform_basis_points, platform_floor_kobo, agent_basis_points, agent_fixed_kobo,
  union_basis_points, union_fixed_kobo, is_active)
 values
 ('TEST_KAD_ABJ','Test Kaduna to Abuja','KAD-01','ABJ-01','PROPORTIONAL',500000,2000,50000,830,0,500,0,true),
 ('TEST_ABJ_KAD','Test Abuja to Kaduna','ABJ-01','KAD-01','PROPORTIONAL',500000,2000,50000,830,0,500,0,true)
 on conflict (id) do nothing;"

echo
echo "Done. Test accounts (password: $PASS):"
echo "  admin: $ADMIN_PHONE  (wallet reset to NGN 50,000)"
echo "  clerk: $CLERK_PHONE  (wallet reset to NGN 10,000)"
echo "Test bank for bookings: code 001, account 0000000000, name TEST ACCOUNT 0000000000"
