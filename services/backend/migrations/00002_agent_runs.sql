-- +goose Up
CREATE TABLE agent_runs (
    id uuid PRIMARY KEY,
    chat_id uuid NOT NULL REFERENCES chats (id) ON DELETE CASCADE,
    user_message_id bigint NOT NULL REFERENCES messages (id) ON DELETE CASCADE,
    state text NOT NULL DEFAULT 'pending' CHECK (state IN ('pending', 'completed', 'failed')),
    payload jsonb NOT NULL CHECK (jsonb_typeof(payload) = 'object'),
    artifacts jsonb NOT NULL DEFAULT '[]' CHECK (jsonb_typeof(artifacts) = 'array'),
    error text NOT NULL DEFAULT '',
    created_at timestamptz NOT NULL DEFAULT CURRENT_TIMESTAMP,
    completed_at timestamptz
);
CREATE UNIQUE INDEX agent_runs_active_chat_idx ON agent_runs (chat_id) WHERE state = 'pending';

-- +goose Down
DROP TABLE agent_runs;
