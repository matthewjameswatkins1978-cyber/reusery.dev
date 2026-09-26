-- +goose Up
-- Packet 9 records what actually happened after a Resolution reached an agent
-- or a human. This is factual outcome feedback, not behavioural Evidence:
-- it never satisfies or fails a contract requirement, never changes a policy
-- and never re-runs a decision.
CREATE TABLE resolution_feedback (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    resolution_id bigint NOT NULL REFERENCES resolutions (id) ON DELETE CASCADE,
    kind         text NOT NULL,
    note         text NOT NULL DEFAULT '',
    recorded_at  timestamptz NOT NULL,
    CONSTRAINT resolution_feedback_kind_check CHECK (kind IN (
        'adopted',
        'rejected',
        'integration_succeeded',
        'integration_failed',
        'abandoned'
    )),
    CONSTRAINT resolution_feedback_note_check CHECK (char_length(note) <= 1000)
);

-- Chronological reads are always ordered by (resolution_id, recorded_at, id).
CREATE INDEX resolution_feedback_order_idx
    ON resolution_feedback (resolution_id, recorded_at, id);

-- +goose Down
DROP TABLE resolution_feedback;
