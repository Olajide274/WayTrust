# WayTrust — Digital Waybill & Escrow

Production-oriented **digital waybill escrow** for Nigerian motor parks and inter-state corridors.

Park agents lock freight into escrow at booking. On delivery, the receiver releases a **4-digit PIN**. Funds are then eligible for split disbursement to **driver**, **agent**, **union**, and **platform** via Paystack.

> Not a hobby demo: real PostgreSQL, JWT auth, ledger constraints, state machine, and live Paystack resolve/transfer paths (transfer success depends on funded Paystack balance).

**WayTrust** is operator-neutral software for waybill escrow. It can serve NURTW and other park networks; it is not an official union product unless a formal agreement says otherwise.

---

## Problem

Informal inter-state parcel movement still relies on cash and paper waybills:

- Disputes at checkpoints and destination parks
- Unclear freight splits (driver / agent / union)
- No reliable proof of delivery before money moves

This system digitizes **booking → lock → deliver (PIN) → disburse** without forcing passengers or drivers to install an app (zero-app path via USSD/SMS is planned).

---

## Core flow

```text
Agent login (JWT)
    → Resolve driver account (Paystack)
    → Book waybill (wallet debit + escrow lock + fee split)
    → In transit
    → Verify PIN → DELIVERED
    → Disburse (Paystack transfer to driver; ledger updates)
```

### Waybill states

| State | Meaning |
|--------|---------|
| `CREATED` | Record opened |
| `ESCROW_LOCKED` | Freight reserved from agent wallet |
| `IN_TRANSIT` | Optional transit marker |
| `DELIVERED` | PIN verified; eligible for payout |
| `DISBURSED` | Driver transfer completed |
| `DISPUTE_HOLD` / `UNCLAIMED_HOLD` | Exception paths |

Disbursement is **only** allowed from `DELIVERED` (enforced in domain + service).

---

## Stack

| Layer | Technology |
|--------|------------|
| API | Go (`cmd/server`) — HTTP REST + gRPC + USSD hook |
| DB | PostgreSQL (agents, waybills, ledgers, payout splits, parks, corridor policies) |
| Auth | JWT (agent id + `is_admin`); secret required in production/staging |
| Payments | Paystack (name resolve + transfer) |
| Cache / rate limit | Redis optional; in-memory PIN limiter fallback |
| Thin agent UI | Static PWA under `web/` (served at `/app/`) |
| Sleek agent UI | Next.js + Tailwind + Framer Motion in `apps/agent` (in progress) |

---

## Repository layout

```text
waytrust/   # (repo may still be named nurtw-escrow until renamed)
├── cmd/server/          # Main process (REST, gRPC, static /app)
├── internal/
│   ├── domain/          # States, fee calculator, state helpers + tests
│   ├── gateway/         # HTTP handlers (REST, USSD)
│   ├── repository/      # Postgres transactions, ledgers
│   ├── resolver/        # Paystack resolve & payout
│   ├── security/        # JWT, middleware, rate limit
│   ├── service/         # Escrow orchestration
│   └── observability/   # Structured logs
├── migrations/          # SQL schema
├── proto/               # gRPC definitions
├── web/                 # Thin agent PWA (login, book, PIN, list)
└── apps/agent/          # Next.js enterprise UI (scaffold / build-out)
```

---

## Features (current)

### Backend

- Agent register / login (JWT)
- Admin-only park creation (`is_admin`)
- Corridor fee policies (proportional splits; unit-tested)
- Atomic book: wallet lock + waybill + payout split pre-allocation + ledger
- Bank account resolve (Paystack) with name confidence
- PIN verify → `DELIVERED`
- Disburse path (honest failure if Paystack balance insufficient)
- Ownership binding (agent only acts on own wallet / waybills)
- `JWT_SECRET` required when `APP_ENV=production` or `staging`
- Structured logging on login / create / disburse

### Thin PWA (`/app/`)

- Login + wallet balance
- Bank **name** dropdown (incl. Moniepoint) + Other (manual code)
- Resolve → Book & lock
- Verify PIN → DELIVERED
- My waybills list

> The one-page PWA is a **working lab shell**, not the final UX.

---

## API surface (agent v1)

Base URL (local): `http://localhost:8080`

| Method | Path | Auth | Purpose |
|--------|------|------|---------|
| `POST` | `/api/v1/agent/login` | No | JWT |
| `POST` | `/api/v1/agent/register` | No* | Onboard agent |
| `GET` | `/api/v1/agent/profile` | JWT | Profile + wallet |
| `POST` | `/api/v1/agent/fund-wallet` | JWT | Fund **own** wallet |
| `GET` | `/api/v1/parks` | Public/JWT | List parks |
| `POST` | `/api/v1/parks/create` | Admin JWT | Create park |
| `POST` | `/api/v1/bank/resolve` | JWT | Resolve NUBAN |
| `POST` | `/api/v1/waybills` | JWT | Book + lock |
| `GET` | `/api/v1/agent/waybills` | JWT | List mine |
| `GET` | `/api/v1/waybills/status` | JWT | Status by id |
| `POST` | `/api/v1/waybills/verify-pin` | JWT | PIN → DELIVERED |
| `POST` | `/api/v1/waybills/start-transit` | JWT | Start transit and unclaimed timeout |
| `POST` | `/api/v1/waybills/report-refusal` | JWT | Record receiver refusal; hold escrow |
| `POST` | `/api/v1/waybills/disburse` | JWT | Payout |
| `POST` | `/api/v1/admin/waybills/resolve` | Admin JWT | Release held funds to driver or refund booking agent |
| `POST` | `/api/v1/ussd` | Gateway | USSD entry |
| `GET` | `/app/` | No | Thin agent PWA |

\* Tighten public register for production.

Amounts are always **integer kobo**.

Receiver refusal reports are limited to the owning agent while a waybill is `IN_TRANSIT`. Reporting a refusal changes its state to `DISPUTE_HOLD` and keeps funds held until an administrator resolves the waybill to the driver or refunds the booking agent.

---

## Local development

### Prerequisites

- Go 1.22+ (toolchain per `go.mod`)
- PostgreSQL 16/18
- Paystack **test** secret (resolve / transfer)
- Optional: Redis

### Environment

```bash
export JWT_SECRET=waytrust-local-dev-secret-change-in-production
export PAYSTACK_SECRET_KEY=sk_test_xxxxxxxx
# optional:
# export APP_ENV=development
# export WEB_DIR=web
```

Windows CMD:

```cmd
set JWT_SECRET=waytrust-local-dev-secret-change-in-production
set PAYSTACK_SECRET_KEY=sk_test_xxxxxxxx
```

**Never commit real secrets.** Use `.env` locally (gitignored) or host env vars.

### Database

Apply migrations under `migrations/` to a database (e.g. `waytrust` or existing `nurtw_escrow` during transition).

To apply the wallet-credit migration to the local Docker database:

```bash
docker exec -i nurtw-pg psql -v ON_ERROR_STOP=1 -U postgres -d nurtw_escrow < migrations/migrations/000005_wallet_credits.up.sql
```

### Run API

```bash
go build ./...
go test ./internal/domain/ -v
go run ./cmd/server
```

Expected:

```text
Connected to PostgreSQL
[HTTP REST & USSD] Gateway listening on 0.0.0.0:8080
```

Thin UI: http://localhost:8080/app/

### Domain tests

```bash
go test ./internal/domain/ -v
```

Covers fee split invariants and `CanDisburse` / `AssertCanDisburse`.

---

## Money model (high level)

1. Sender pays freight to the **park agent** (cash or transfer into agent float).
2. On book, system **debits agent wallet** and locks escrow; computes splits from **corridor policy** (not a hard-coded amount).
3. On PIN verify → `DELIVERED`.
4. On disburse → Paystack pays **driver** share; agent commission / union / platform recorded per policy and ledger.

Driver payout is the **escrowed driver share**, aligned with park SOP—not an undocumented side cash deal unless you define one operationally.

---

## Security notes

- JWT on protected routes; agent id taken from token, not trusted from body where ownership matters
- Admin gate for park creation
- PIN rate limiting (Redis or memory)
- Production/staging **panic** if `JWT_SECRET` unset
- Paystack errors surface honestly; failed transfer must not mark `DISBURSED`

---

## USSD / SMS

- HTTP USSD handler exists for aggregator callbacks.
- **National dial codes** require a telco/USSD VAS partner — not enabled by UI work alone.
- Priority: finish **sleek agent web app** for smartphone clerks; USSD for feature-phone receivers once a provider is contracted.

---

## Roadmap

- [x] Escrow book / PIN / state machine
- [x] Paystack resolve + disburse path
- [x] Thin PWA proof
- [ ] Sleek Next.js agent UI (white enterprise, multi-step, Framer Motion)
- [ ] Funded Paystack test → full disburse success in staging
- [ ] Exception workflows (cancel, dispute, unclaimed, refund)
- [ ] USSD aggregator integration
- [ ] SMS PIN delivery
- [ ] CI, staging parity, runbooks

---

## License / operators

Intended for authorized park operators and integrators.
Deploy only with proper agreements, KYC, and payment-provider compliance.
WayTrust is not affiliated with any union unless a written contract establishes that relationship.

---

## Author

Built as a real-world corridor escrow system for Nigerian motor parks.
