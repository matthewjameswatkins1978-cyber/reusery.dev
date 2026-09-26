-- +goose Up
-- Packet 10 adds a bounded, inspectable project-context layer.
--
-- Only derived manifest facts and explicit preferences are stored. No source
-- code, no README, no local filesystem path, no environment dump.

CREATE TABLE projects (
    id              text PRIMARY KEY,
    name            text NOT NULL,
    source_kind     text NOT NULL,
    source_locator  text NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL,
    updated_at      timestamptz NOT NULL,
    CONSTRAINT projects_source_kind_check CHECK (source_kind IN ('local', 'github_public')),
    -- A local project's identity comes from its module paths, so its
    -- filesystem location is never recorded. A public project may record only
    -- its public GitHub identity and optional subdirectory.
    CONSTRAINT projects_local_locator_empty CHECK (
        source_kind <> 'local' OR source_locator = ''
    )
);

CREATE TABLE project_fingerprints (
    id                 bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    project_id         text NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    schema_version     integer NOT NULL,
    fingerprint_sha256 text NOT NULL,
    fingerprint_json   jsonb NOT NULL,
    source_revision    text NOT NULL DEFAULT '',
    observed_at        timestamptz NOT NULL,
    CONSTRAINT project_fingerprints_unique UNIQUE (project_id, fingerprint_sha256)
);

-- Latest fingerprint for a project, newest first.
CREATE INDEX project_fingerprints_latest_idx
    ON project_fingerprints (project_id, observed_at DESC, id DESC);

CREATE TABLE project_preferences (
    id                   bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    project_id           text NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    kind                 text NOT NULL,
    primitive_id         text NOT NULL DEFAULT '',
    candidate_id         text NOT NULL DEFAULT '',
    text_value           text NOT NULL DEFAULT '',
    int_value            integer,
    source_reason        text NOT NULL DEFAULT '',
    source_resolution_id bigint REFERENCES resolutions (id) ON DELETE SET NULL,
    recorded_at          timestamptz NOT NULL,
    forgotten_at         timestamptz,
    CONSTRAINT project_preferences_kind_check CHECK (kind IN (
        'exclude_candidate',
        'max_direct_dependencies',
        'deny_licence',
        'avoid_dependency',
        'avoid_reference',
        'deny_archived'
    )),
    -- Provenance: which explicit Packet 7 feedback reason produced this
    -- memory, if any. Empty when the memory was recorded directly.
    CONSTRAINT project_preferences_source_reason_check CHECK (
        source_reason = '' OR source_reason IN (
            'not_quite',
            'too_many_dependencies',
            'licence_not_allowed',
            'avoid_dependency',
            'avoid_reference',
            'archived_project'
        )
    )
);

-- Active preferences are the hot path for every project-aware decision.
CREATE INDEX project_preferences_active_idx
    ON project_preferences (project_id, kind)
    WHERE forgotten_at IS NULL;

-- An immutable materialised snapshot of what a Resolution actually saw.
-- The hash is the SHA-256 of the canonical context JSON, so a row is never
-- rewritten in place and historical decisions keep resolving to their own
-- context after preferences or the fingerprint later change.
CREATE TABLE project_contexts (
    hash          text PRIMARY KEY,
    project_id    text NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    fingerprint_id bigint NOT NULL REFERENCES project_fingerprints (id),
    context_json  jsonb NOT NULL,
    created_at    timestamptz NOT NULL
);

ALTER TABLE resolutions
    ADD COLUMN project_id text REFERENCES projects (id) ON DELETE SET NULL;

ALTER TABLE resolutions
    ADD COLUMN project_context_hash text REFERENCES project_contexts (hash) ON DELETE SET NULL;

-- +goose Down
ALTER TABLE resolutions DROP COLUMN project_context_hash;
ALTER TABLE resolutions DROP COLUMN project_id;

DROP TABLE project_contexts;
DROP TABLE project_preferences;
DROP TABLE project_fingerprints;
DROP TABLE projects;
