-- 0001_init — `prices_raw` and `prices_aggregated`.
--
-- See spec §3.3 for the schema rationale. Prices are stored as IEEE-754
-- doubles end-to-end in the off-chain pipeline; oracle-service converts
-- to int256 (Chainlink 8-decimal scale) when building fulfillPrice
-- calldata. Source is a text discriminator matching SourceKind.String().

CREATE TABLE prices_raw (
    id                 BIGSERIAL          PRIMARY KEY,
    asset_id           TEXT               NOT NULL,
    source             TEXT               NOT NULL,
    price              DOUBLE PRECISION   NOT NULL,
    fetched_at         TIMESTAMPTZ        NOT NULL,
    source_observed_at TIMESTAMPTZ,
    raw_payload        BYTEA
);

CREATE INDEX prices_raw_asset_source_fetched_idx
    ON prices_raw (asset_id, source, fetched_at DESC);

CREATE INDEX prices_raw_fetched_idx
    ON prices_raw (fetched_at DESC);

CREATE TABLE prices_aggregated (
    id                    BIGSERIAL          PRIMARY KEY,
    asset_id              TEXT               NOT NULL,
    median_price          DOUBLE PRECISION   NOT NULL,
    source_count          INTEGER            NOT NULL,
    source_breakdown_json JSONB              NOT NULL,
    aggregated_at         TIMESTAMPTZ        NOT NULL,
    window_start          TIMESTAMPTZ        NOT NULL,
    window_end            TIMESTAMPTZ        NOT NULL
);

CREATE INDEX prices_aggregated_asset_time_idx
    ON prices_aggregated (asset_id, aggregated_at DESC);

CREATE INDEX prices_aggregated_time_idx
    ON prices_aggregated (aggregated_at DESC);
