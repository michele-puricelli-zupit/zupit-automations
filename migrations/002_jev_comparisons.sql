CREATE TABLE comparisons (
    id BIGSERIAL PRIMARY KEY,
    run_id BIGINT NOT NULL UNIQUE REFERENCES runs(id),
    status TEXT NOT NULL DEFAULT 'pending',
    attempts INT NOT NULL DEFAULT 0,
    next_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    result JSONB NOT NULL DEFAULT '{}',
    error TEXT NOT NULL DEFAULT '',
    reference TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX comparisons_ready ON comparisons(status,next_at);
