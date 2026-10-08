ALTER TABLE waybills
    ADD COLUMN in_transit_at TIMESTAMPTZ;

UPDATE waybills
SET in_transit_at = updated_at
WHERE current_state = 'IN_TRANSIT';

CREATE INDEX idx_waybills_in_transit_timeout
    ON waybills(in_transit_at)
    WHERE current_state = 'IN_TRANSIT';
