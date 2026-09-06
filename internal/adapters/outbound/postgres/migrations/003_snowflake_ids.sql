-- One durable Snowflake worker for this database, serialized by a row lock.
-- 2025-01-01 UTC epoch, 41 timestamp bits, worker 0 (10 bits), 12 sequence bits.
CREATE TABLE snowflake_state (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    last_ms BIGINT NOT NULL,
    sequence INTEGER NOT NULL CHECK (sequence BETWEEN 0 AND 4095)
);
INSERT INTO snowflake_state VALUES (TRUE, -1, 0);

CREATE FUNCTION next_snowflake_id() RETURNS TEXT AS $$
DECLARE
    previous_ms BIGINT;
    current_ms BIGINT;
    counter INTEGER;
BEGIN
    SELECT last_ms, sequence INTO STRICT previous_ms, counter
    FROM snowflake_state WHERE singleton FOR UPDATE;
    current_ms := floor(extract(epoch FROM clock_timestamp()) * 1000)::BIGINT - 1735689600000;
    -- Durable logical time handles clock rollback and bursts without duplicates.
    current_ms := greatest(current_ms, previous_ms);
    IF current_ms = previous_ms THEN
        counter := counter + 1;
        IF counter > 4095 THEN
            current_ms := current_ms + 1;
            counter := 0;
        END IF;
    ELSE
        counter := 0;
    END IF;
    IF current_ms < 1 OR current_ms >= 2199023255552 THEN
        RAISE EXCEPTION 'snowflake timestamp outside supported epoch';
    END IF;
    UPDATE snowflake_state SET last_ms = current_ms, sequence = counter WHERE singleton;
    RETURN ((current_ms << 22) | counter::BIGINT)::TEXT;
END;
$$ LANGUAGE plpgsql VOLATILE;

CREATE FUNCTION valid_snowflake_id(value TEXT) RETURNS BOOLEAN AS $$
    SELECT coalesce(value ~ '^[1-9][0-9]{0,18}$'
        AND length(value) <= 19
        AND (length(value) < 19 OR value COLLATE "C" <= '9223372036854775807' COLLATE "C"), FALSE);
$$ LANGUAGE sql IMMUTABLE;

LOCK TABLE products, offers, offer_versions IN ACCESS EXCLUSIVE MODE;
CREATE TEMP TABLE snowflake_mapping (
    kind TEXT NOT NULL,
    scope TEXT NOT NULL DEFAULT '',
    old_value TEXT NOT NULL,
    new_value TEXT NOT NULL DEFAULT next_snowflake_id(),
    PRIMARY KEY (kind, scope, old_value),
    UNIQUE (new_value)
) ON COMMIT DROP;
INSERT INTO snowflake_mapping(kind, old_value) SELECT 'offer', id FROM offers;
INSERT INTO snowflake_mapping(kind, old_value) SELECT 'version', id FROM offer_versions;
-- Include historical references to products that have not yet been registered.
INSERT INTO snowflake_mapping(kind, old_value)
SELECT 'product', value FROM (
    SELECT id AS value FROM products
    UNION
    SELECT item->>'canonicalProductId' FROM offers, jsonb_array_elements(draft->'components') item
    UNION
    SELECT item->>'canonicalProductId' FROM offer_versions, jsonb_array_elements(snapshot->'components') item
) refs WHERE value IS NOT NULL;
INSERT INTO snowflake_mapping(kind, scope, old_value)
SELECT 'group', offer_id, value FROM (
    SELECT id AS offer_id, item->>'choiceGroupId' AS value FROM offers, jsonb_array_elements(draft->'components') item
    UNION
    SELECT offer_id, item->>'choiceGroupId' FROM offer_versions, jsonb_array_elements(snapshot->'components') item
) refs WHERE value IS NOT NULL AND value <> '';

CREATE FUNCTION pg_temp.remap_offer_ids(payload JSONB, old_offer_id TEXT) RETURNS JSONB AS $$
DECLARE
    component JSONB;
    components JSONB := '[]'::JSONB;
    replacement TEXT;
BEGIN
    SELECT new_value INTO STRICT replacement FROM snowflake_mapping WHERE kind='offer' AND old_value=old_offer_id;
    payload := jsonb_set(payload, '{id}', to_jsonb(replacement));
    FOR component IN SELECT * FROM jsonb_array_elements(payload->'components') LOOP
        SELECT new_value INTO STRICT replacement FROM snowflake_mapping
            WHERE kind='product' AND old_value=component->>'canonicalProductId';
        component := jsonb_set(component, '{canonicalProductId}', to_jsonb(replacement));
        IF coalesce(component->>'choiceGroupId', '') <> '' THEN
            SELECT new_value INTO STRICT replacement FROM snowflake_mapping
                WHERE kind='group' AND scope=old_offer_id AND old_value=component->>'choiceGroupId';
            component := jsonb_set(component, '{choiceGroupId}', to_jsonb(replacement));
        END IF;
        components := components || jsonb_build_array(component);
    END LOOP;
    RETURN jsonb_set(payload, '{components}', components);
END;
$$ LANGUAGE plpgsql;

-- The surrounding migration transaction restores both protections on failure.
ALTER TABLE offer_versions DISABLE TRIGGER offer_versions_immutable;
ALTER TABLE offer_versions ALTER CONSTRAINT offer_versions_offer_id_fkey DEFERRABLE INITIALLY DEFERRED;
UPDATE products p SET id=m.new_value FROM snowflake_mapping m WHERE m.kind='product' AND p.id=m.old_value;
UPDATE offers o SET id=m.new_value, draft=pg_temp.remap_offer_ids(o.draft, o.id)
    FROM snowflake_mapping m WHERE m.kind='offer' AND o.id=m.old_value;
UPDATE offer_versions v SET id=vm.new_value, offer_id=om.new_value,
    snapshot=jsonb_set(pg_temp.remap_offer_ids(v.snapshot, v.offer_id), '{versionId}', to_jsonb(vm.new_value))
    FROM snowflake_mapping vm, snowflake_mapping om
    WHERE vm.kind='version' AND vm.old_value=v.id AND om.kind='offer' AND om.old_value=v.offer_id;
UPDATE offers o SET draft=jsonb_set(o.draft, '{versionId}', to_jsonb(v.id))
    FROM offer_versions v WHERE v.offer_id=o.id AND v.version=o.version;
SET CONSTRAINTS ALL IMMEDIATE;
ALTER TABLE offer_versions ALTER CONSTRAINT offer_versions_offer_id_fkey NOT DEFERRABLE;
ALTER TABLE offer_versions ENABLE TRIGGER offer_versions_immutable;

CREATE FUNCTION valid_offer_snowflake_ids(payload JSONB) RETURNS BOOLEAN AS $$
    SELECT valid_snowflake_id(payload->>'id')
        AND (NOT payload ? 'versionId' OR valid_snowflake_id(payload->>'versionId'))
        AND coalesce((SELECT bool_and(
            valid_snowflake_id(item->>'canonicalProductId')
            AND (coalesce(item->>'choiceGroupId','') = '' OR valid_snowflake_id(item->>'choiceGroupId'))
        ) FROM jsonb_array_elements(payload->'components') item), TRUE);
$$ LANGUAGE sql IMMUTABLE;
ALTER TABLE products ADD CONSTRAINT products_snowflake_id CHECK (valid_snowflake_id(id));
ALTER TABLE offers ADD CONSTRAINT offers_snowflake_ids CHECK (
    valid_snowflake_id(id) AND valid_offer_snowflake_ids(draft) AND draft->>'id'=id);
ALTER TABLE offer_versions ADD CONSTRAINT offer_versions_snowflake_ids CHECK (
    valid_snowflake_id(id) AND valid_snowflake_id(offer_id) AND valid_offer_snowflake_ids(snapshot)
    AND snapshot->>'id'=offer_id AND snapshot->>'versionId' IS NOT NULL AND snapshot->>'versionId'=id);
