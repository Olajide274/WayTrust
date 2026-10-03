CREATE TABLE corridor_policies (
    id VARCHAR(64) PRIMARY KEY,
    corridor_name VARCHAR(120) NOT NULL,
    origin_park_id VARCHAR(64) NOT NULL,
    destination_park_id VARCHAR(64) NOT NULL,
    model VARCHAR(32) NOT NULL CHECK (model IN ('PROPORTIONAL', 'DYNAMIC_FLOOR')),
    threshold_kobo BIGINT NOT NULL DEFAULT 500000,
    platform_basis_points BIGINT NOT NULL DEFAULT 2000,
    platform_floor_kobo BIGINT NOT NULL DEFAULT 50000,
    agent_basis_points BIGINT NOT NULL DEFAULT 830,
    agent_fixed_kobo BIGINT NOT NULL DEFAULT 50000,
    union_basis_points BIGINT NOT NULL DEFAULT 500,
    union_fixed_kobo BIGINT NOT NULL DEFAULT 50000,
    is_active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_corridor_origin_dest ON corridor_policies(origin_park_id, destination_park_id);

-- Seed Real Nigerian Commercial Transport Corridors
INSERT INTO corridor_policies (
    id, corridor_name, origin_park_id, destination_park_id, model, 
    threshold_kobo, platform_basis_points, platform_floor_kobo, 
    agent_basis_points, agent_fixed_kobo, union_basis_points, union_fixed_kobo, is_active
) VALUES 
(
    'KADUNA_ABUJA', 'Kaduna Central - Abuja Jabi Corridor', 
    'PARK_KADUNA_CENTRAL', 'PARK_ABUJA_JABI', 'PROPORTIONAL',
    500000, 2000, 50000, 830, 0, 500, 0, TRUE
),
(
    'KANO_KADUNA', 'Kano Naibawa - Kaduna Central Corridor', 
    'PARK_KANO_NAIBAWA', 'PARK_KADUNA_CENTRAL', 'PROPORTIONAL',
    500000, 2000, 50000, 830, 0, 500, 0, TRUE
),
(
    'KADUNA_LAGOS', 'Kaduna Central - Lagos Ojota Long Distance', 
    'PARK_KADUNA_CENTRAL', 'PARK_LAGOS_OJOTA', 'DYNAMIC_FLOOR',
    800000, 1000, 50000, 0, 50000, 0, 50000, TRUE
)
ON CONFLICT (id) DO NOTHING;