-- Core Gateway audit foundation: PostgreSQL-only audit foundation.
-- This migration intentionally creates only audit_interactions and
-- audit_content_parts. The audit admission layer connects them to Gateway traffic.

CREATE TABLE audit_interactions (
    id UUID PRIMARY KEY,
    gateway_request_id UUID NOT NULL UNIQUE,
    subject_user_id BIGINT NULL,
    subject_email_snapshot VARCHAR(255) NULL,
    api_key_id BIGINT NULL,
    api_key_fingerprint VARCHAR(64) NULL,
    profile_version VARCHAR(64) NOT NULL,
    protocol VARCHAR(16) NOT NULL,
    endpoint VARCHAR(512) NOT NULL,
    method VARCHAR(16) NOT NULL,
    transport VARCHAR(16) NOT NULL,
    requested_model VARCHAR(100) NULL,
    resolved_model VARCHAR(100) NULL,
    request_outcome VARCHAR(32) NOT NULL DEFAULT 'processing',
    request_outcome_version BIGINT NOT NULL DEFAULT 0,
    content_state VARCHAR(32) NOT NULL DEFAULT 'recording',
    content_state_version BIGINT NOT NULL DEFAULT 0,
    downstream_status SMALLINT NULL,
    downstream_write_result VARCHAR(32) NOT NULL DEFAULT 'not_applicable',
    admitted_at TIMESTAMPTZ NOT NULL,
    completed_at TIMESTAMPTZ NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    last_activity_at TIMESTAMPTZ NOT NULL,
    request_sha256 BYTEA NULL,
    response_sha256 BYTEA NULL,
    request_part_count INTEGER NOT NULL DEFAULT 0,
    response_part_count INTEGER NOT NULL DEFAULT 0,
    safe_error_summary VARCHAR(512) NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT audit_interactions_protocol_check
        CHECK (protocol IN ('anthropic', 'openai')),
    CONSTRAINT audit_interactions_transport_check
        CHECK (transport IN ('http', 'sse')),
    CONSTRAINT audit_interactions_request_outcome_check
        CHECK (request_outcome IN ('processing', 'rejected_pre_upstream', 'completed', 'upstream_failed', 'interrupted')),
    CONSTRAINT audit_interactions_content_state_check
        CHECK (content_state IN ('recording', 'complete', 'incomplete', 'expired')),
    CONSTRAINT audit_interactions_downstream_status_check
        CHECK (downstream_status IS NULL OR downstream_status BETWEEN 100 AND 599),
    CONSTRAINT audit_interactions_downstream_write_result_check
        CHECK (downstream_write_result IN ('not_applicable', 'pending', 'succeeded', 'failed', 'unknown')),
    CONSTRAINT audit_interactions_request_version_check
        CHECK (request_outcome_version >= 0),
    CONSTRAINT audit_interactions_content_version_check
        CHECK (content_state_version >= 0),
    CONSTRAINT audit_interactions_part_counts_check
        CHECK (request_part_count >= 0 AND response_part_count >= 0),
    CONSTRAINT audit_interactions_request_hash_check
        CHECK (request_sha256 IS NULL OR octet_length(request_sha256) = 32),
    CONSTRAINT audit_interactions_response_hash_check
        CHECK (response_sha256 IS NULL OR octet_length(response_sha256) = 32),
    CONSTRAINT audit_interactions_retention_anchor_check
        CHECK (expires_at = admitted_at + INTERVAL '180 days'),
    CONSTRAINT audit_interactions_activity_time_check
        CHECK (last_activity_at >= admitted_at),
    CONSTRAINT audit_interactions_completion_time_check
        CHECK (completed_at IS NULL OR completed_at >= admitted_at)
);

CREATE INDEX audit_interactions_subject_admitted_idx
    ON audit_interactions(subject_user_id, admitted_at DESC)
    WHERE subject_user_id IS NOT NULL;
CREATE INDEX audit_interactions_outcome_state_activity_idx
    ON audit_interactions(request_outcome, content_state, last_activity_at);
CREATE INDEX audit_interactions_expires_idx
    ON audit_interactions(expires_at);

CREATE TABLE audit_content_parts (
    id UUID PRIMARY KEY,
    interaction_id UUID NOT NULL,
    direction VARCHAR(16) NOT NULL,
    sequence INTEGER NOT NULL,
    nonce BYTEA NOT NULL,
    ciphertext BYTEA NOT NULL,
    auth_tag BYTEA NOT NULL,
    key_version VARCHAR(64) NOT NULL,
    aad_format_version VARCHAR(64) NOT NULL,
    plaintext_length BIGINT NOT NULL,
    ciphertext_length BIGINT NOT NULL,
    plaintext_sha256 BYTEA NOT NULL,
    ciphertext_sha256 BYTEA NOT NULL,
    downstream_write_result VARCHAR(32) NOT NULL DEFAULT 'not_applicable',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT audit_content_parts_interaction_fk
        FOREIGN KEY (interaction_id) REFERENCES audit_interactions(id) ON DELETE RESTRICT,
    CONSTRAINT audit_content_parts_direction_check
        CHECK (direction IN ('request', 'response')),
    CONSTRAINT audit_content_parts_sequence_check
        CHECK (sequence >= 0),
    CONSTRAINT audit_content_parts_nonce_check
        CHECK (octet_length(nonce) = 12),
    CONSTRAINT audit_content_parts_auth_tag_check
        CHECK (octet_length(auth_tag) = 16),
    CONSTRAINT audit_content_parts_key_version_check
        CHECK (length(key_version) > 0),
    CONSTRAINT audit_content_parts_aad_version_check
        CHECK (aad_format_version = 'core-gateway-aad-v1'),
    CONSTRAINT audit_content_parts_lengths_check
        CHECK (plaintext_length >= 0 AND ciphertext_length >= 0 AND ciphertext_length = octet_length(ciphertext)),
    CONSTRAINT audit_content_parts_plaintext_hash_check
        CHECK (octet_length(plaintext_sha256) = 32),
    CONSTRAINT audit_content_parts_ciphertext_hash_check
        CHECK (octet_length(ciphertext_sha256) = 32),
    CONSTRAINT audit_content_parts_downstream_write_result_check
        CHECK (downstream_write_result IN ('not_applicable', 'pending', 'succeeded', 'failed', 'unknown')),
    CONSTRAINT audit_content_parts_interaction_direction_sequence_key
        UNIQUE (interaction_id, direction, sequence)
);

CREATE INDEX audit_content_parts_interaction_created_idx
    ON audit_content_parts(interaction_id, created_at);

ALTER TABLE usage_logs
    ADD COLUMN gateway_request_id UUID NULL;

CREATE INDEX usage_logs_gateway_request_id_idx
    ON usage_logs(gateway_request_id)
    WHERE gateway_request_id IS NOT NULL;

CREATE OR REPLACE FUNCTION enforce_core_audit_interaction_state()
RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'INSERT' THEN
        IF NEW.request_outcome <> 'processing'
           OR NEW.content_state <> 'recording'
           OR NEW.request_outcome_version <> 0
           OR NEW.content_state_version <> 0 THEN
            RAISE EXCEPTION 'audit interaction must start in processing/recording at version zero';
        END IF;
        RETURN NEW;
    END IF;

    IF NEW.id IS DISTINCT FROM OLD.id
       OR NEW.gateway_request_id IS DISTINCT FROM OLD.gateway_request_id
       OR NEW.admitted_at IS DISTINCT FROM OLD.admitted_at
       OR NEW.expires_at IS DISTINCT FROM OLD.expires_at THEN
        RAISE EXCEPTION 'immutable audit interaction identity or retention anchor changed';
    END IF;

    IF NEW.request_outcome IS DISTINCT FROM OLD.request_outcome THEN
        IF OLD.request_outcome <> 'processing'
           OR NEW.request_outcome NOT IN ('rejected_pre_upstream', 'completed', 'upstream_failed', 'interrupted')
           OR NEW.request_outcome_version <> OLD.request_outcome_version + 1 THEN
            RAISE EXCEPTION 'illegal audit request outcome transition';
        END IF;
    ELSIF NEW.request_outcome_version <> OLD.request_outcome_version THEN
        RAISE EXCEPTION 'audit request outcome version changed without state transition';
    END IF;

    IF NEW.content_state IS DISTINCT FROM OLD.content_state THEN
        IF NOT (
            (OLD.content_state = 'recording' AND NEW.content_state IN ('complete', 'incomplete'))
            OR (OLD.content_state IN ('complete', 'incomplete') AND NEW.content_state = 'expired')
        ) OR NEW.content_state_version <> OLD.content_state_version + 1 THEN
            RAISE EXCEPTION 'illegal audit content state transition';
        END IF;
    ELSIF NEW.content_state_version <> OLD.content_state_version THEN
        RAISE EXCEPTION 'audit content state version changed without state transition';
    END IF;

    NEW.updated_at := NOW();
    RETURN NEW;
END;
$$;

CREATE TRIGGER audit_interactions_state_guard
BEFORE INSERT OR UPDATE ON audit_interactions
FOR EACH ROW EXECUTE FUNCTION enforce_core_audit_interaction_state();
