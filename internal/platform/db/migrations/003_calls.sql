CREATE TABLE calls (
    id UUID PRIMARY KEY,
    organization_id UUID NOT NULL REFERENCES organizations(id),
    owner_user_id UUID NOT NULL REFERENCES users(id),
    contact_id UUID NULL REFERENCES contacts(id) ON DELETE SET NULL,
    peer_name_snapshot TEXT NOT NULL,
    peer_phone_snapshot TEXT NOT NULL,
    direction TEXT NOT NULL CHECK (direction IN ('inbound', 'outbound')),
    status TEXT NOT NULL CHECK (status IN ('dialing', 'ringing', 'active', 'ended', 'rejected', 'missed', 'failed')),
    version INT NOT NULL CHECK (version >= 1),
    created_at TIMESTAMPTZ NOT NULL,
    started_at TIMESTAMPTZ,
    ended_at TIMESTAMPTZ,
    failure_reason TEXT CHECK (failure_reason IN ('no_answer', 'ring_timeout', 'network_error', 'simulation_interrupted'))
);

CREATE UNIQUE INDEX calls_one_active_per_owner
    ON calls (owner_user_id)
    WHERE status IN ('dialing', 'ringing', 'active');

CREATE TABLE call_transitions (
    id UUID PRIMARY KEY,
    call_id UUID NOT NULL REFERENCES calls(id),
    from_status TEXT,
    to_status TEXT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    reason TEXT
);

CREATE TABLE call_notes (
    call_id UUID PRIMARY KEY REFERENCES calls(id),
    text TEXT NOT NULL CHECK (char_length(text) <= 2000),
    updated_at TIMESTAMPTZ NOT NULL
);
