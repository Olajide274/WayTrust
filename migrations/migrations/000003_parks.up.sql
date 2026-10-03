-- Production parks table for NURTW motor parks nationwide
-- Empty by design. Real parks are added later via admin/SQL.
CREATE TABLE IF NOT EXISTS parks (
    id          VARCHAR(64) PRIMARY KEY,
    name        VARCHAR(160) NOT NULL,
    city        VARCHAR(80)  NOT NULL,
    state       VARCHAR(80)  NOT NULL,
    is_active   BOOLEAN      NOT NULL DEFAULT TRUE,
    created_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW(),
    updated_at  TIMESTAMPTZ  NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_parks_state  ON parks(state);
CREATE INDEX IF NOT EXISTS idx_parks_active ON parks(is_active);