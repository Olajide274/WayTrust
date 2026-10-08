DROP TABLE waybill_resolutions;
DROP FUNCTION prevent_waybill_resolution_mutation();

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
        'UNCLAIMED_HOLD'
    ));
