-- 000001_init_escrow_ledger.up.sql
-- Production schema for NURTW Digital Waybill & Escrow System
-- Uses gen_random_uuid() (built-in since PostgreSQL 13) — no uuid-ossp extension required

-- 1. Park Agents (Pre-funded digital wallet holder at motor parks)
CREATE TABLE agents (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    full_name VARCHAR(120) NOT NULL,
    phone_number VARCHAR(15) UNIQUE NOT NULL,
    park_id VARCHAR(64) NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    wallet_balance_kobo BIGINT NOT NULL DEFAULT 0 CHECK (wallet_balance_kobo >= 0),
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 2. Waybill Escrow Records
CREATE TABLE waybills (
    id VARCHAR(16) PRIMARY KEY,
    agent_id UUID NOT NULL REFERENCES agents(id),
    sender_phone VARCHAR(15) NOT NULL,
    receiver_phone VARCHAR(15) NOT NULL,
    driver_phone VARCHAR(15) NOT NULL,
    vehicle_reg_number VARCHAR(20) NOT NULL,
    origin_park_id VARCHAR(64) NOT NULL,
    destination_park_id VARCHAR(64) NOT NULL,
    total_freight_kobo BIGINT NOT NULL CHECK (total_freight_kobo > 0),
    pin_hash VARCHAR(72) NOT NULL,
    failed_pin_attempts INT NOT NULL DEFAULT 0 CHECK (failed_pin_attempts >= 0),
    driver_bank_code VARCHAR(10) NOT NULL,
    driver_account_number VARCHAR(10) NOT NULL,
    driver_verified_name VARCHAR(150) NOT NULL,
    current_state VARCHAR(32) NOT NULL DEFAULT 'CREATED'
        CHECK (current_state IN (
            'CREATED',
            'ESCROW_LOCKED',
            'IN_TRANSIT',
            'DELIVERED',
            'DISBURSED',
            'DISPUTE_HOLD',
            'UNCLAIMED_HOLD'
        )),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 3. Immutable Financial Audit Ledger
CREATE TABLE escrow_ledgers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    waybill_id VARCHAR(16) NOT NULL REFERENCES waybills(id),
    transaction_type VARCHAR(32) NOT NULL
        CHECK (transaction_type IN (
            'AGENT_DEDUCTION',
            'AGENT_COMMISSION',
            'DISBURSEMENT_SPLIT',
            'REFUND'
        )),
    amount_kobo BIGINT NOT NULL,
    balance_after_kobo BIGINT NOT NULL,
    description TEXT NOT NULL,
    idempotency_key VARCHAR(128) UNIQUE NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- 4. Four-Way Stakeholder Payout Splits
CREATE TABLE payout_splits (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    waybill_id VARCHAR(16) UNIQUE NOT NULL REFERENCES waybills(id),
    driver_payout_kobo BIGINT NOT NULL CHECK (driver_payout_kobo >= 0),
    agent_commission_kobo BIGINT NOT NULL CHECK (agent_commission_kobo >= 0),
    union_dues_kobo BIGINT NOT NULL CHECK (union_dues_kobo >= 0),
    platform_treasury_kobo BIGINT NOT NULL CHECK (platform_treasury_kobo >= 0),
    driver_transfer_ref VARCHAR(128),
    disbursed_at TIMESTAMPTZ
);

-- Production Indexes for fast lookup by phone numbers and state machine filters
CREATE INDEX idx_waybills_receiver_phone ON waybills(receiver_phone);
CREATE INDEX idx_waybills_driver_phone ON waybills(driver_phone);
CREATE INDEX idx_waybills_current_state ON waybills(current_state);
CREATE INDEX idx_escrow_ledgers_waybill ON escrow_ledgers(waybill_id);
CREATE INDEX idx_agents_phone ON agents(phone_number);
CREATE INDEX idx_agents_park ON agents(park_id);