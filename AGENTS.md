# WayTrust

Formerly known as nurtw-escrow.

Escrow/waybill system for park clerks (agents).

## Stack
- Backend: Go gateway (cmd/server, internal/gateway, internal/security), port 8080.
  Env vars only: DATABASE_URL, JWT_SECRET, PAYSTACK_SECRET_KEY. Never commit or hardcode secrets.
- DB: Postgres 16. Migrations are in migrations/migrations/ (nested; do not move).
- Frontend: apps/agent, Next.js 16 + React + Tailwind + TypeScript, port 3000.
  Next.js has breaking changes: consult the docs in node_modules/next/dist/docs before writing code.

## API (all under /api/v1, protected routes need Authorization: Bearer <token>)
- POST /agent/login {phone_number, password} -> {message, token, agent, is_admin}
- GET /agent/profile -> agent incl. wallet_balance_kobo
- POST /agent/fund-wallet {amount_kobo} -> {message, new_balance_kobo}
- POST /bank/resolve {account_number, bank_code, declared_name}
- POST /waybills (sender/receiver/driver phones, vehicle reg, origin/destination park IDs,
  freight_kobo, driver account_number, bank_code, declared_name) -> waybill id, secret PIN, remaining balance
- GET /parks (public)
Amounts are in kobo. Token is stored in localStorage as nurtw_token.

## Rules
- Small incremental changes. Do not change backend contracts, auth logic, or the working login flow without asking.
- After each step: say how to test it against the real backend, then stop and wait for confirmation.
- All API calls go through a typed client in apps/agent/src/lib; no fetch calls scattered in components.
- Every screen handles loading, error, and empty states.
- No new dependencies without asking.
- Mobile-first: clerks use phones.

## Roadmap
1. Functionality: auth guard + profile/wallet on /home -> resolve/book flow -> logout/session expiry.
2. Design polish (only after phase 1 works): consistent green brand color, spacing, typography, cards, icons.
