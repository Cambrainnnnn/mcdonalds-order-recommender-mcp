CREATE TABLE offer_business_keys (
 business_key TEXT PRIMARY KEY,
 offer_id TEXT NOT NULL UNIQUE REFERENCES offers(id)
);
-- migrate:split
CREATE TABLE offer_ingestion_requests (
 request_key TEXT PRIMARY KEY,
 payload_hash TEXT NOT NULL,
 response JSONB NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
