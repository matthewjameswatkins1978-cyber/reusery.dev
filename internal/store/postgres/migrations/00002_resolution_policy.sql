-- +goose Up
-- Packet 7 makes the policy that justified an engineering decision a
-- first-class part of that decision. Existing Packet 1-6 resolutions keep
-- loading with an empty policy_id: they were produced without a policy layer,
-- and that absence is recorded honestly rather than backfilled.
ALTER TABLE resolutions
    ADD COLUMN policy_id text NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE resolutions
    DROP COLUMN policy_id;
