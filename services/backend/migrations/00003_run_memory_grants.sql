-- +goose Up
ALTER TABLE agent_runs
    ADD COLUMN memory_grant_nonce text NOT NULL DEFAULT '',
    ADD COLUMN memory_grant_hash bytea,
    ADD COLUMN memory_grant_expires_at timestamptz;

CREATE TABLE run_memory_callbacks (
    run_id uuid NOT NULL REFERENCES agent_runs(id) ON DELETE CASCADE,
    expected_version bigint NOT NULL CHECK (expected_version >= 0),
    summary_hash bytea NOT NULL,
    version bigint NOT NULL,
    PRIMARY KEY (run_id, expected_version)
);

-- +goose Down
DROP TABLE run_memory_callbacks;
ALTER TABLE agent_runs
    DROP COLUMN memory_grant_nonce,
    DROP COLUMN memory_grant_hash,
    DROP COLUMN memory_grant_expires_at;
