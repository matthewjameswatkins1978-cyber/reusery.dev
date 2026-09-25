-- +goose Up
-- Core Reusery domain tables. Textual IDs are preserved from internal/model;
-- only resolutions receive a storage-internal identity key.

CREATE TABLE primitives (
    id          text PRIMARY KEY,
    name        text NOT NULL,
    description text NOT NULL DEFAULT '',
    tags        text[] NOT NULL DEFAULT '{}',
    contract_id text NOT NULL DEFAULT ''
);

CREATE TABLE contracts (
    id           text PRIMARY KEY,
    primitive_id text NOT NULL,
    version      text NOT NULL DEFAULT '',
    summary      text NOT NULL DEFAULT ''
);

CREATE TABLE requirements (
    contract_id    text NOT NULL REFERENCES contracts (id) ON DELETE CASCADE,
    requirement_id text NOT NULL,
    position       integer NOT NULL,
    description    text NOT NULL DEFAULT '',
    kind           text NOT NULL DEFAULT '',
    required       boolean NOT NULL DEFAULT false,
    PRIMARY KEY (contract_id, requirement_id),
    CONSTRAINT requirements_contract_position_unique UNIQUE (contract_id, position)
);

CREATE TABLE specimens (
    id              text PRIMARY KEY,
    primitive_id    text NOT NULL,
    name            text NOT NULL DEFAULT '',
    source_url      text NOT NULL DEFAULT '',
    source_revision text NOT NULL DEFAULT '',
    source_path     text NOT NULL DEFAULT '',
    source_license  text NOT NULL DEFAULT '',
    reuse_modes     text[] NOT NULL DEFAULT '{}'
);

CREATE TABLE evidence (
    id              text PRIMARY KEY,
    subject_id      text NOT NULL,
    kind            text NOT NULL DEFAULT '',
    claim           text NOT NULL DEFAULT '',
    result          text NOT NULL,
    source_url      text NOT NULL DEFAULT '',
    source_revision text NOT NULL DEFAULT '',
    source_path     text NOT NULL DEFAULT '',
    source_license  text NOT NULL DEFAULT '',
    observed_at     timestamptz NOT NULL,
    applies_to      text NOT NULL DEFAULT '',
    methodology     text NOT NULL DEFAULT '',
    artifact        text NOT NULL DEFAULT '',
    CONSTRAINT evidence_result_check CHECK (result IN ('pass', 'fail', 'unknown', 'info'))
);

-- SubjectID is deliberately more general than SpecimenID, so there is no
-- foreign key to specimens here.
CREATE INDEX evidence_subject_idx ON evidence (subject_id);

CREATE TABLE resolutions (
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    primitive_id text NOT NULL,
    contract_id  text NOT NULL,
    outcome      text NOT NULL,
    specimen_id  text,
    reasons      text[] NOT NULL DEFAULT '{}',
    unknowns     text[] NOT NULL DEFAULT '{}',
    evidence_ids text[] NOT NULL DEFAULT '{}',
    resolved_at  timestamptz NOT NULL,
    CONSTRAINT resolutions_outcome_check CHECK (outcome IN ('reuse', 'adapt', 'depend', 'reference', 'build_locally'))
);

CREATE TABLE rejections (
    resolution_id bigint NOT NULL REFERENCES resolutions (id) ON DELETE CASCADE,
    position      integer NOT NULL,
    specimen_id   text NOT NULL,
    reasons       text[] NOT NULL DEFAULT '{}',
    PRIMARY KEY (resolution_id, position),
    CONSTRAINT rejections_specimen_not_empty CHECK (specimen_id <> '')
);

-- +goose Down
DROP TABLE rejections;
DROP TABLE resolutions;
DROP TABLE evidence;
DROP TABLE specimens;
DROP TABLE requirements;
DROP TABLE contracts;
DROP TABLE primitives;
