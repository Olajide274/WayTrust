CREATE TABLE wallet_credits (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    agent_id UUID NOT NULL REFERENCES agents(id),
    credited_by_admin_id UUID NOT NULL REFERENCES agents(id),
    amount_kobo BIGINT NOT NULL CHECK (amount_kobo > 0),
    balance_after_kobo BIGINT NOT NULL CHECK (balance_after_kobo >= 0),
    reason TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_wallet_credits_agent_created
    ON wallet_credits(agent_id, created_at DESC);
