ALTER TABLE waybills
    DROP CONSTRAINT waybills_current_state_check;

ALTER TABLE waybills
    ADD CONSTRAINT waybills_current_state_check
    CHECK (current_state IN (
        'CREATED',
        'ESCROW_LOCKED',
        'IN_TRANSIT',
        'DELIVERED',
        'DISBURSED',
        'DISPUTE_HOLD',
        'UNCLAIMED_HOLD',
        'CANCELLED',
        'REFUNDED'
    ));

CREATE TABLE waybill_resolutions (
    waybill_id VARCHAR(16) PRIMARY KEY REFERENCES waybills(id),
    resolved_by UUID NOT NULL REFERENCES agents(id),
    outcome VARCHAR(32) NOT NULL CHECK (outcome IN ('RELEASE_TO_DRIVER', 'REFUND_TO_AGENT')),
    reason TEXT NOT NULL CHECK (char_length(trim(reason)) BETWEEN 1 AND 500),
    resolved_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE FUNCTION prevent_waybill_resolution_mutation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'waybill resolution records are append-only';
END;
$$;

CREATE TRIGGER waybill_resolutions_append_only
    BEFORE UPDATE OR DELETE ON waybill_resolutions
    FOR EACH ROW
    EXECUTE FUNCTION prevent_waybill_resolution_mutation();

CREATE TRIGGER waybill_resolutions_no_truncate
    BEFORE TRUNCATE ON waybill_resolutions
    FOR EACH STATEMENT
    EXECUTE FUNCTION prevent_waybill_resolution_mutation();
