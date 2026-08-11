CREATE TABLE IF NOT EXISTS ratelimit_buckets (
    key TEXT PRIMARY KEY,
    capacity INTEGER NOT NULL CHECK (capacity > 0),
    tokens DOUBLE PRECISION NOT NULL CHECK (tokens >= 0),
    refill_per_second DOUBLE PRECISION NOT NULL CHECK (refill_per_second > 0),
    last_refill TIMESTAMPTZ NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS ratelimit_buckets_last_seen_idx ON ratelimit_buckets(last_seen_at);

CREATE OR REPLACE FUNCTION ratelimit_take(p_key TEXT, p_capacity INTEGER, p_refill_per_second DOUBLE PRECISION)
RETURNS TABLE(allowed BOOLEAN, remaining DOUBLE PRECISION)
LANGUAGE plpgsql
AS $$
DECLARE
    v_now TIMESTAMPTZ := clock_timestamp();
    v_tokens DOUBLE PRECISION;
    v_last_refill TIMESTAMPTZ;
BEGIN
    PERFORM pg_advisory_xact_lock(hashtextextended(p_key, 0));

    SELECT bucket.tokens, bucket.last_refill
      INTO v_tokens, v_last_refill
      FROM ratelimit_buckets AS bucket
     WHERE bucket.key = p_key
     FOR UPDATE;

    IF NOT FOUND THEN
        v_tokens := p_capacity;
        v_last_refill := v_now;
    ELSE
        v_tokens := LEAST(
            p_capacity::DOUBLE PRECISION,
            v_tokens + GREATEST(0, EXTRACT(EPOCH FROM (v_now - v_last_refill))) * p_refill_per_second
        );
    END IF;

    allowed := v_tokens >= 1;
    IF allowed THEN
        v_tokens := v_tokens - 1;
    END IF;

    INSERT INTO ratelimit_buckets (key, capacity, tokens, refill_per_second, last_refill, last_seen_at)
    VALUES (p_key, p_capacity, v_tokens, p_refill_per_second, v_now, v_now)
    ON CONFLICT (key) DO UPDATE SET
        capacity = EXCLUDED.capacity,
        tokens = EXCLUDED.tokens,
        refill_per_second = EXCLUDED.refill_per_second,
        last_refill = EXCLUDED.last_refill,
        last_seen_at = EXCLUDED.last_seen_at,
        updated_at = v_now;

    remaining := v_tokens;
    RETURN NEXT;
END;
$$;
