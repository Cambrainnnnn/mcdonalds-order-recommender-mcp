CREATE TABLE IF NOT EXISTS offers (
    id TEXT PRIMARY KEY,
    status TEXT NOT NULL CHECK (status IN ('draft', 'reviewed', 'published', 'disabled')),
    version INTEGER NOT NULL DEFAULT 0,
    draft JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

-- migrate:split

CREATE TABLE IF NOT EXISTS offer_versions (
    id TEXT PRIMARY KEY,
    offer_id TEXT NOT NULL REFERENCES offers(id),
    version INTEGER NOT NULL,
    snapshot JSONB NOT NULL,
    published_at TIMESTAMPTZ NOT NULL,
    valid_from TIMESTAMPTZ,
    valid_until TIMESTAMPTZ,
    valid_time_rules JSONB NOT NULL DEFAULT '[]'::jsonb,
    verified_at TIMESTAMPTZ,
    UNIQUE (offer_id, version)
);

-- migrate:split

CREATE INDEX IF NOT EXISTS offer_versions_eligibility_idx
    ON offer_versions (offer_id, version, valid_from, valid_until);
-- migrate:split
CREATE INDEX IF NOT EXISTS offers_status_idx ON offers (status);

-- migrate:split

CREATE OR REPLACE FUNCTION reject_offer_version_mutation() RETURNS trigger AS $$
BEGIN
    RAISE EXCEPTION 'offer versions are immutable';
END;
$$ LANGUAGE plpgsql;

-- migrate:split

DROP TRIGGER IF EXISTS offer_versions_immutable ON offer_versions;
-- migrate:split
CREATE TRIGGER offer_versions_immutable
    BEFORE UPDATE OR DELETE ON offer_versions
    FOR EACH ROW EXECUTE FUNCTION reject_offer_version_mutation();
