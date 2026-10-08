CREATE TABLE receiver_refusal_reports (
    waybill_id VARCHAR(16) PRIMARY KEY REFERENCES waybills(id),
    agent_id UUID NOT NULL REFERENCES agents(id),
    reason TEXT NOT NULL CHECK (char_length(trim(reason)) BETWEEN 1 AND 500),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
