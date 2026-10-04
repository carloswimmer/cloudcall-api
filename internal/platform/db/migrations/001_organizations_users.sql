CREATE TABLE IF NOT EXISTS schema_migrations (
    version TEXT PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE organizations (
    id UUID PRIMARY KEY,
    name TEXT NOT NULL,
    brand_color TEXT NOT NULL CHECK (brand_color ~ '^#[0-9A-Fa-f]{6}$'),
    timezone TEXT NOT NULL
);

CREATE TABLE users (
    id UUID PRIMARY KEY,
    organization_id UUID NOT NULL REFERENCES organizations(id),
    name TEXT NOT NULL,
    extension TEXT NOT NULL,
    presence TEXT NOT NULL CHECK (presence IN ('available', 'busy', 'offline')),
    presence_before_busy TEXT NULL CHECK (presence_before_busy IN ('available', 'busy', 'offline')),
    version INT NOT NULL CHECK (version >= 1),
    UNIQUE (organization_id, extension)
);
