ALTER TABLE products
    ADD COLUMN price_amount BIGINT,
    ADD COLUMN price_currency TEXT,
    ADD COLUMN sales_scopes JSONB NOT NULL DEFAULT '["standalone", "offer_component"]'::jsonb;

ALTER TABLE products
    ADD CONSTRAINT products_price_valid CHECK (
        (price_amount IS NULL AND price_currency IS NULL)
        OR (price_amount >= 0 AND length(btrim(price_currency)) BETWEEN 1 AND 16)
    ),
    ADD CONSTRAINT products_sales_scopes_valid CHECK (
        jsonb_typeof(sales_scopes) = 'array'
        AND jsonb_array_length(sales_scopes) > 0
        AND sales_scopes <@ '["standalone", "offer_component"]'::jsonb
    );
